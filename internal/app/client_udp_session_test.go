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
package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SUDOKU-ASCII/sudoku/internal/config"
	"github.com/SUDOKU-ASCII/sudoku/internal/protocol"
	"github.com/SUDOKU-ASCII/sudoku/internal/tunnel"
	"github.com/SUDOKU-ASCII/sudoku/pkg/dnsutil"
)

// uotRecorder stands in for the tunnel connection; it keeps written bytes only when keep is set so
// benchmarks stay allocation-free.
type uotRecorder struct {
	net.Conn
	keep    bool
	written atomic.Int64
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (r *uotRecorder) Write(p []byte) (int, error) {
	r.written.Add(int64(len(p)))
	if r.keep {
		r.mu.Lock()
		r.buf.Write(p)
		r.mu.Unlock()
	}
	return len(p), nil
}

func (r *uotRecorder) Close() error { return nil }

func (r *uotRecorder) frames(t testing.TB) []udpFrame {
	t.Helper()
	r.mu.Lock()
	data := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	var out []udpFrame
	rd := bytes.NewReader(data)
	for rd.Len() > 0 {
		addr, payload, err := tunnel.ReadUoTDatagram(rd)
		if err != nil {
			t.Fatalf("decode recorded UoT frame: %v", err)
		}
		out = append(out, udpFrame{addr: addr, payload: string(payload)})
	}
	return out
}

type udpFrame struct{ addr, payload string }

// udpSocket is a loopback socket that counts what it receives and, when keep is set, queues the datagrams.
type udpSocket struct {
	*net.UDPConn
	received atomic.Int64
	packets  chan []byte
}

func newUDPSocket(t testing.TB, keep bool) *udpSocket {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	s := &udpSocket{UDPConn: conn}
	if keep {
		s.packets = make(chan []byte, 64)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			s.received.Add(1)
			if s.packets != nil {
				s.packets <- append([]byte(nil), buf[:n]...)
			}
		}
	}()
	return s
}

func (s *udpSocket) udpAddr() netip.AddrPort { return s.LocalAddr().(*net.UDPAddr).AddrPort() }

func (s *udpSocket) next(t testing.TB) []byte {
	t.Helper()
	select {
	case p := <-s.packets:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a datagram")
		return nil
	}
}

func (s *udpSocket) expectNothing(t testing.TB) {
	t.Helper()
	select {
	case p := <-s.packets:
		t.Fatalf("unexpected datagram: %x", p)
	case <-time.After(100 * time.Millisecond):
	}
}

type udpSessionHarness struct {
	sess   *uotClientSession
	uot    *uotRecorder
	sink   *udpSocket // a direct destination
	client *udpSocket // where replies for the SOCKS5 client land
}

func newUDPSessionHarness(t testing.TB, cfg *config.Config, mgrs *routeManagers, keep bool) *udpSessionHarness {
	t.Helper()
	return newUDPSessionHarnessOn(t, cfg, mgrs, keep, "udp4", net.IPv4(127, 0, 0, 1))
}

func newUDPSessionHarnessOn(t testing.TB, cfg *config.Config, mgrs *routeManagers, keep bool, network string, bindIP net.IP) *udpSessionHarness {
	t.Helper()
	relay, err := net.ListenUDP(network, &net.UDPAddr{IP: bindIP})
	if err != nil {
		t.Fatalf("listen relay (%s): %v", network, err)
	}
	h := &udpSessionHarness{
		uot:    &uotRecorder{keep: keep},
		sink:   newUDPSocket(t, keep),
		client: newUDPSocket(t, keep),
	}
	h.sess = newUoTClientSession(NewMockConn(nil), relay, h.uot, network, newTestClient(t, cfg.ProxyMode, mgrs, nil), []string{"node", groupProxy})
	t.Cleanup(h.sess.close)
	return h
}

// clientAddr is the SOCKS5 client's source address. It is deliberately not a loopback address so that
// loopback sockets in the harness count as peers rather than as the client.
var clientAddr = netip.MustParseAddrPort("10.9.9.9:5000")

func socks5UDPRequest(t testing.TB, dest, payload string) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0})
	if err := protocol.WriteAddress(&b, dest); err != nil {
		t.Fatalf("encode %q: %v", dest, err)
	}
	b.WriteString(payload)
	return b.Bytes()
}

func (h *udpSessionHarness) fromClient(t testing.TB, dest, payload string) {
	t.Helper()
	if err := h.sess.handlePacket(clientAddr, socks5UDPRequest(t, dest, payload)); err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
}

// pointRepliesAtClientSocket redirects replies to a socket we can read, since clientAddr is not routable.
func (h *udpSessionHarness) pointRepliesAtClientSocket() {
	addr := h.client.udpAddr()
	h.sess.clientAddr.Store(&addr)
}

func (h *udpSessionHarness) waitForFrames(t testing.TB, n int) []udpFrame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if frames := h.uot.frames(t); len(frames) >= n {
			return frames
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d UoT frames", n)
	return nil
}

func TestUDPSessionProxyPath(t *testing.T) {
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "global"}, nil, true)
	h.fromClient(t, "8.8.8.8:53", "ip-payload")
	h.fromClient(t, "example.org:443", "domain-payload")

	got := h.waitForFrames(t, 2)
	want := []udpFrame{{"8.8.8.8:53", "ip-payload"}, {"example.org:443", "domain-payload"}}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("frames = %+v, want %+v", got, want)
	}
	h.sink.expectNothing(t)
}

func TestUDPSessionRejectPath(t *testing.T) {
	mgrs := &routeManagers{reject: newRuleManagerForTest(t, "payload:\n  - DOMAIN-SUFFIX,ads.example\n")}
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "global"}, mgrs, true)
	h.fromClient(t, "track.ads.example:443", "dropped")
	h.fromClient(t, "8.8.8.8:53", "kept")

	if got := h.waitForFrames(t, 1); len(got) != 1 || got[0].payload != "kept" {
		t.Fatalf("frames = %+v, want only the unrejected datagram", got)
	}
}

func TestUDPSessionDirectPathAndReply(t *testing.T) {
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "direct"}, nil, true)
	h.fromClient(t, h.sink.udpAddr().String(), "to-peer")
	if got := string(h.sink.next(t)); got != "to-peer" {
		t.Fatalf("peer received %q", got)
	}
	if n := len(h.uot.frames(t)); n != 0 {
		t.Fatalf("direct datagram was also tunneled (%d frames)", n)
	}

	h.pointRepliesAtClientSocket()
	if err := h.sess.handlePacket(h.sink.udpAddr(), []byte("from-peer")); err != nil {
		t.Fatalf("reply: %v", err)
	}
	port := h.sink.udpAddr().Port()
	want := append([]byte{0, 0, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}, "from-peer"...)
	if got := h.client.next(t); !bytes.Equal(got, want) {
		t.Fatalf("client received %x, want %x", got, want)
	}
}

func TestUDPSessionDropsRepliesFromUnknownPeers(t *testing.T) {
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "global"}, nil, true)
	h.fromClient(t, "8.8.8.8:53", "hello")
	h.pointRepliesAtClientSocket()

	if err := h.sess.handlePacket(h.sink.udpAddr(), []byte("spoofed")); err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
	h.client.expectNothing(t)
}

func TestUDPSessionIgnoresMalformedRequests(t *testing.T) {
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "global"}, nil, true)
	valid := socks5UDPRequest(t, "8.8.8.8:53", "x")
	fragmented := append([]byte(nil), valid...)
	fragmented[2] = 1
	truncated := valid[:6]
	for name, pkt := range map[string][]byte{"too short": {0, 0, 0}, "fragmented": fragmented, "truncated address": truncated, "unknown atyp": {0, 0, 0, 9, 1, 2, 3}} {
		if err := h.sess.handlePacket(clientAddr, pkt); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if h.sess.allowedClient.IsValid() {
		t.Fatalf("a malformed request pinned the client IP to %v", h.sess.allowedClient)
	}
	if h.uot.written.Load() != 0 {
		t.Fatal("malformed request was tunneled")
	}
}

func TestUDPSessionPinsFirstClientIP(t *testing.T) {
	h := newUDPSessionHarness(t, &config.Config{ProxyMode: "global"}, nil, true)
	h.fromClient(t, "8.8.8.8:53", "first")
	if h.sess.allowedClient != clientAddr.Addr() {
		t.Fatalf("allowed client IP = %v, want %v", h.sess.allowedClient, clientAddr.Addr())
	}

	other := netip.MustParseAddrPort("10.8.8.8:6000")
	if err := h.sess.handlePacket(other, socks5UDPRequest(t, "8.8.8.8:53", "intruder")); err != nil {
		t.Fatalf("handlePacket: %v", err)
	}
	h.fromClient(t, "8.8.8.8:53", "third")
	got := h.waitForFrames(t, 2)
	if got[0].payload != "first" || got[1].payload != "third" {
		t.Fatalf("frames = %+v, want the intruder's request skipped", got)
	}
}

func TestUDPSessionDirectFallbacks(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(addr string) (string, error)
	}{
		{"resolve failure", func(string) (string, error) { return "", errors.New("nxdomain") }},
		{"ipv6 answer on an ipv4 socket", func(string) (string, error) { return "[2001:db8::1]:443", nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := resolveWithCache
			t.Cleanup(func() { resolveWithCache = old })
			resolveWithCache = func(_ context.Context, _ *dnsutil.Resolver, addr string) (string, error) { return tc.resolve(addr) }

			h := newUDPSessionHarness(t, &config.Config{ProxyMode: "direct"}, nil, true)
			h.fromClient(t, "host.example:443", "fallback")
			if got := h.waitForFrames(t, 1); got[0] != (udpFrame{"host.example:443", "fallback"}) {
				t.Fatalf("frames = %+v", got)
			}
		})
	}
}

// An IPv6 loopback relay cannot send to an IPv4 direct destination; the datagram must go through the
// proxy instead of failing the send and tearing down the whole association.
func TestUDPSessionIPv4DirectOnIPv6RelayIsProxied(t *testing.T) {
	probe, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	_ = probe.Close()

	h := newUDPSessionHarnessOn(t, &config.Config{ProxyMode: "direct"}, nil, true, "udp6", net.IPv6loopback)
	h.fromClient(t, "8.8.8.8:53", "v4-direct-on-v6-relay")
	if got := h.waitForFrames(t, 1); got[0] != (udpFrame{"8.8.8.8:53", "v4-direct-on-v6-relay"}) {
		t.Fatalf("frames = %+v", got)
	}
}

func BenchmarkUDPClientDatagram(b *testing.B) {
	mgrs := newBenchRouteManagers(b)
	cfg := &config.Config{ProxyMode: "pac"}
	h := newUDPSessionHarness(b, cfg, mgrs, false)
	old := resolveWithCache
	b.Cleanup(func() { resolveWithCache = old })
	sinkAddr := h.sink.udpAddr().String()
	resolveWithCache = func(_ context.Context, _ *dnsutil.Resolver, addr string) (string, error) {
		if addr == "www.example.cn:443" {
			return sinkAddr, nil
		}
		return addr, nil
	}

	payload := string(bytes.Repeat([]byte{'x'}, 200))
	cases := []struct{ name, dest string }{
		{"proxy-ip", "8.8.8.8:53"},
		{"proxy-domain", "foo.example.org:443"},
		{"direct-ip", sinkAddr},
		{"direct-domain", "www.example.cn:443"},
		{"reject", "track.ads.example:443"},
	}
	for _, tc := range cases {
		pkt := socks5UDPRequest(b, tc.dest, payload)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for i := 0; i < b.N; i++ {
				if err := h.sess.handlePacket(clientAddr, pkt); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkUDPPeerReply(b *testing.B) {
	h := newUDPSessionHarness(b, &config.Config{ProxyMode: "direct"}, nil, false)
	h.pointRepliesAtClientSocket()
	peer := h.sink.udpAddr()
	h.sess.allowedClient = clientAddr.Addr()
	h.sess.peers.Add(peer, time.Hour)

	pkt := bytes.Repeat([]byte{'y'}, 200)
	b.ReportAllocs()
	b.SetBytes(int64(len(pkt)))
	for i := 0; i < b.N; i++ {
		if err := h.sess.handlePacket(peer, pkt); err != nil {
			b.Fatal(err)
		}
	}
}
