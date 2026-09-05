/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package httpmask

import (
	"container/list"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tunnelEarlyDataQueryKey   = "ed"
	tunnelEarlyDataHeader     = "X-Sudoku-Early"
	tunnelStreamEOFHeader     = "X-Sudoku-Stream-EOF"
	tunnelUploadSequenceQuery = "seq"
	tunnelUploadSequenceCap   = "upload-seq"
	tunnelPreconnectCount     = 3
	tunnelTLSHandshakeTimeout = 10 * time.Second
)

type authorizeResponse struct {
	token        string
	earlyPayload []byte
}

func canonicalHeaderHost(urlHost, scheme string) string {
	host, port, err := net.SplitHostPort(urlHost)
	if err != nil {
		return urlHost
	}

	defaultPort := ""
	switch scheme {
	case "https":
		defaultPort = "443"
	case "http":
		defaultPort = "80"
	}
	if defaultPort == "" || port != defaultPort {
		return urlHost
	}

	// If we strip the port from an IPv6 literal, re-add brackets to keep the Host header valid.
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func sessionPreconnectCount() int {
	// Three connections are enough to overlap authorize, pull and the first
	// upload. Mux used to wait for a fourth "spare" connection before it was
	// usable, which added a full WAN RTT to every split-tunnel session.
	return tunnelPreconnectCount
}

func parseAuthorizeResponse(body []byte) (*authorizeResponse, error) {
	s := strings.TrimSpace(string(body))
	idx := strings.Index(s, "token=")
	if idx < 0 {
		return nil, errors.New("missing token")
	}
	s = s[idx+len("token="):]
	if s == "" {
		return nil, errors.New("empty token")
	}
	// Token is base64.RawURLEncoding (A-Z a-z 0-9 - _). Strip any trailing bytes (e.g. from CDN compression).
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
			continue
		}
		break
	}
	token := b.String()
	if token == "" {
		return nil, errors.New("empty token")
	}
	out := &authorizeResponse{token: token}
	if earlyLine := findAuthorizeField(body, "ed="); earlyLine != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(earlyLine)
		if err != nil {
			return nil, fmt.Errorf("decode early authorize payload failed: %w", err)
		}
		out.earlyPayload = decoded
	}
	if findAuthorizeField(body, "cap=") != tunnelUploadSequenceCap {
		return nil, errors.New("server does not support HTTPMask v0.5 upload sequencing")
	}
	return out, nil
}

func findAuthorizeField(body []byte, prefix string) string {
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func setEarlyDataQuery(rawURL string, payload []byte) (string, error) {
	if len(payload) == 0 {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(tunnelEarlyDataQueryKey, base64.RawURLEncoding.EncodeToString(payload))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseEarlyDataQuery(u *url.URL) ([]byte, error) {
	if u == nil {
		return nil, nil
	}
	val := strings.TrimSpace(u.Query().Get(tunnelEarlyDataQueryKey))
	if val == "" {
		return nil, nil
	}
	return base64.RawURLEncoding.DecodeString(val)
}

type sessionDialInfo struct {
	client     *http.Client
	pushURL    string
	pullURL    string
	finURL     string
	closeURL   string
	headerHost string
}

func uploadURL(rawURL string, sequence uint64) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(tunnelUploadSequenceQuery, strconv.FormatUint(sequence, 10))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type transportKey struct {
	scheme     string
	urlHost    string
	dialAddr   string
	serverName string
}

type transportCacheEntry struct {
	key       transportKey
	transport *tunnelHTTPTransport
}

type tunnelHTTPTransport struct {
	transport *http.Transport
	dialer    *preconnectDialer
}

func (t *tunnelHTTPTransport) close() {
	if t == nil {
		return
	}
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
	if t.dialer != nil {
		t.dialer.close()
	}
}

type tunnelHTTPClient struct {
	client    *http.Client
	transport *tunnelHTTPTransport
}

func (c *tunnelHTTPClient) preconnect(ctx context.Context, req *http.Request, count int) context.CancelFunc {
	if c == nil || c.transport == nil || c.transport.transport == nil || c.transport.dialer == nil ||
		req == nil || req.URL == nil || count <= 0 {
		return func() {}
	}

	if proxy := c.transport.transport.Proxy; proxy != nil {
		proxyURL, err := proxy(req)
		if err != nil || proxyURL != nil {
			return func() {}
		}
	}
	return c.transport.dialer.preconnect(ctx, req.URL.Scheme == "https", count)
}

func (c *tunnelHTTPClient) directPreconnectTarget(rawURL string) (*preconnectDialer, bool, bool) {
	if c == nil || c.transport == nil || c.transport.transport == nil || c.transport.dialer == nil ||
		strings.TrimSpace(rawURL) == "" {
		return nil, false, false
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, nil)
	if err != nil {
		return nil, false, false
	}
	if proxy := c.transport.transport.Proxy; proxy != nil {
		proxyURL, err := proxy(req)
		if err != nil || proxyURL != nil {
			return nil, false, false
		}
	}
	return c.transport.dialer, req.URL.Scheme == "https", true
}

func (c *tunnelHTTPClient) maintainPreconnect(ctx context.Context, rawURL string, count int) {
	dialer, tlsEnabled, ok := c.directPreconnectTarget(rawURL)
	if !ok || count <= 0 {
		return
	}
	dialer.maintainPreconnect(ctx, tlsEnabled, count)
}

func (c *tunnelHTTPClient) waitPreconnect(ctx context.Context, closed <-chan struct{}, rawURL string, count int) error {
	dialer, _, ok := c.directPreconnectTarget(rawURL)
	if !ok || count <= 0 {
		return nil
	}
	return dialer.pool.waitReady(ctx, closed, count)
}

// transportCache bounds the memory footprint of globally reused HTTP transports and dialers.
//
// Each dial creates its own http.Client, but clients can share Transports to reuse
// underlying TCP/TLS connections (keep-alive / HTTP/2) when Multiplex is enabled.
type transportCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[transportKey]*list.Element
}

func newTransportCache(maxEntries int) *transportCache {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &transportCache{
		max: maxEntries,
		ll:  list.New(),
		m:   make(map[transportKey]*list.Element),
	}
}

func (c *transportCache) getOrCreate(
	key transportKey,
	build func() *tunnelHTTPTransport,
) *tunnelHTTPTransport {
	if c == nil {
		return build()
	}

	c.mu.Lock()
	if el := c.m[key]; el != nil {
		c.ll.MoveToFront(el)
		ent := el.Value.(*transportCacheEntry)
		transport := ent.transport
		c.mu.Unlock()
		return transport
	}
	c.mu.Unlock()

	transport := build()

	c.mu.Lock()
	// Another goroutine might have inserted while we were building.
	if el := c.m[key]; el != nil {
		c.ll.MoveToFront(el)
		ent := el.Value.(*transportCacheEntry)
		existing := ent.transport
		c.mu.Unlock()
		transport.close()
		return existing
	}
	el := c.ll.PushFront(&transportCacheEntry{key: key, transport: transport})
	c.m[key] = el
	for c.max > 0 && c.ll.Len() > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		ent := back.Value.(*transportCacheEntry)
		delete(c.m, ent.key)
		c.ll.Remove(back)
		ent.transport.close()
	}
	c.mu.Unlock()
	return transport
}

var globalTransportCache = newTransportCache(128)

func newHTTPClient(
	urlHost, dialAddr, serverName, scheme string,
	maxIdleConns int,
	reuseTransport bool,
) *tunnelHTTPClient {
	build := func() *tunnelHTTPTransport {
		var transportTLSConfig, dialerTLSConfig *tls.Config
		if scheme == "https" {
			transportTLSConfig = &tls.Config{
				ServerName: serverName,
				MinVersion: tls.VersionTLS12,
				NextProtos: []string{"h2", "http/1.1"},
			}
			dialerTLSConfig = transportTLSConfig.Clone()
		}
		dialer := newPreconnectDialer(urlHost, dialAddr, serverName, dialerTLSConfig)
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     scheme == "https",
			DisableCompression:    true,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConns,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			TLSHandshakeTimeout:   tunnelTLSHandshakeTimeout,
			DialContext:           dialer.dialContext,
		}
		if scheme == "https" {
			transport.TLSClientConfig = transportTLSConfig
			transport.DialTLSContext = dialer.dialTLSContext
		}
		return &tunnelHTTPTransport{transport: transport, dialer: dialer}
	}

	var transport *tunnelHTTPTransport
	if !reuseTransport {
		transport = build()
	} else {
		key := transportKey{
			scheme:     scheme,
			urlHost:    urlHost,
			dialAddr:   dialAddr,
			serverName: serverName,
		}
		transport = globalTransportCache.getOrCreate(key, build)
	}

	return &tunnelHTTPClient{
		client:    &http.Client{Transport: transport.transport},
		transport: transport,
	}
}

func dialSession(ctx context.Context, serverAddress string, opts TunnelDialOptions, mode TunnelMode) (*sessionDialInfo, error) {
	scheme, urlHost, dialAddr, serverName, err := normalizeHTTPDialTarget(serverAddress, opts.TLSEnabled, opts.HostOverride)
	if err != nil {
		return nil, err
	}
	headerHost := canonicalHeaderHost(urlHost, scheme)
	httpClient := newHTTPClient(urlHost, dialAddr, serverName, scheme, 32, multiplexEnabled(opts.Multiplex))

	authorizeURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/session")}).String()
	if opts.EarlyHandshake != nil && len(opts.EarlyHandshake.RequestPayload) > 0 {
		authorizeURL, err = setEarlyDataQuery(authorizeURL, opts.EarlyHandshake.RequestPayload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Host = headerHost
	applyTunnelHeaders(req.Header, headerHost, mode)

	// Overlap authorization, initial pull, and initial push connection handshakes.
	// Keeping this to three avoids adding an extra WAN RTT before mux can open
	// its first logical stream.
	cancelPreconnect := httpClient.preconnect(ctx, req, sessionPreconnectCount())
	keepPreconnected := false
	defer func() {
		if !keepPreconnected {
			cancelPreconnect()
		}
	}()

	resp, err := httpClient.client.Do(req)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s authorize bad status: %s (%s)", mode, resp.Status, strings.TrimSpace(string(bodyBytes)))
	}

	authResp, err := parseAuthorizeResponse(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%s authorize failed: %w", mode, err)
	}
	token := authResp.token
	if token == "" {
		return nil, fmt.Errorf("%s authorize empty token", mode)
	}
	if opts.EarlyHandshake != nil && len(authResp.earlyPayload) > 0 && opts.EarlyHandshake.HandleResponse != nil {
		if err := opts.EarlyHandshake.HandleResponse(authResp.earlyPayload); err != nil {
			return nil, err
		}
	}

	pushURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: "token=" + url.QueryEscape(token)}).String()
	pullURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/stream"), RawQuery: "token=" + url.QueryEscape(token)}).String()
	finURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: "token=" + url.QueryEscape(token) + "&fin=1"}).String()
	closeURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: "token=" + url.QueryEscape(token) + "&close=1"}).String()
	keepPreconnected = true

	return &sessionDialInfo{
		client:     httpClient.client,
		pushURL:    pushURL,
		pullURL:    pullURL,
		finURL:     finURL,
		closeURL:   closeURL,
		headerHost: headerHost,
	}, nil
}

func sendSessionControl(client *http.Client, controlURL, headerHost string, mode TunnelMode) error {
	const maxAttempts = 3

	if client == nil {
		return errors.New("session control client is nil")
	}
	if controlURL == "" || headerHost == "" {
		return errors.New("session control endpoint is empty")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(closeCtx, http.MethodPost, controlURL, nil)
		if err != nil {
			return err
		}
		req.Host = headerHost
		applyTunnelHeaders(req.Header, headerHost, mode)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if closeCtx.Err() == nil && (isDialError(err) || isRetryableHTTPTransportError(err)) {
				continue
			}
			return err
		}
		if resp == nil {
			lastErr = io.ErrUnexpectedEOF
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if attempt > 0 && (resp.StatusCode == http.StatusForbidden ||
				resp.StatusCode == http.StatusNotFound ||
				resp.StatusCode == http.StatusGone) {
				return nil
			}
			return fmt.Errorf("session control bad status: %s", resp.Status)
		}
		return nil
	}
	return lastErr
}

func bestEffortCloseSession(client *http.Client, closeURL, headerHost string, mode TunnelMode) {
	_ = sendSessionControl(client, closeURL, headerHost, mode)
}

func normalizeHTTPDialTarget(serverAddress string, tlsEnabled bool, hostOverride string) (scheme, urlHost, dialAddr, serverName string, err error) {
	host, port, err := net.SplitHostPort(serverAddress)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid server address %q: %w", serverAddress, err)
	}

	if hostOverride != "" {
		// Allow "example.com" or "example.com:443"
		if h, p, splitErr := net.SplitHostPort(hostOverride); splitErr == nil {
			if h != "" {
				hostOverride = h
			}
			if p != "" {
				port = p
			}
		}
		serverName = hostOverride
		urlHost = net.JoinHostPort(hostOverride, port)
	} else {
		serverName = host
		urlHost = net.JoinHostPort(host, port)
	}

	if tlsEnabled {
		scheme = "https"
	} else {
		scheme = "http"
	}

	dialAddr = net.JoinHostPort(host, port)
	return scheme, urlHost, dialAddr, trimPortForHost(serverName), nil
}
