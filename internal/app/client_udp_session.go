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
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SUDOKU-ASCII/sudoku/internal/tunnel"
)

const (
	udpResolveTimeout = 2 * time.Second
	udpPeerTTL        = 2 * time.Minute
	udpLogQuietTTL    = 30 * time.Second
)

// routeLogKey identifies a destination whose route has been logged, so each is logged once per quiet window.
type routeLogKey struct {
	action routeAction
	dest   string
}

type uotClientSession struct {
	ctrlConn  net.Conn
	udpConn   *net.UDPConn
	uotConn   net.Conn
	uotWriter *tunnel.UoTWriter
	udpNet    string
	client    *client
	flows     *udpFlows // nil unless the external controller is on

	closeOnce sync.Once

	// clientAddr is where replies go: the source of the latest request.
	clientAddr atomic.Pointer[netip.AddrPort]

	// allowedClient is the only source IP whose datagrams count as requests; any other source must be a
	// direct peer. It is invalid until the first request when the control peer's address is unusable.
	allowedClient netip.Addr
	peers         ttlSet[netip.AddrPort] // direct destinations that may reply
	logOnce       ttlSet[routeLogKey]
	reply         []byte // scratch for replies built by pipeClientToServer's goroutine
}

// newUoTClientSession relays one UDP association; proxied datagrams go through uotConn, which
// was dialed through proxyChain.
func newUoTClientSession(ctrl net.Conn, udpConn *net.UDPConn, uotConn net.Conn, udpNet string, c *client, proxyChain []string) *uotClientSession {
	sess := &uotClientSession{
		ctrlConn:      ctrl,
		udpConn:       udpConn,
		uotConn:       uotConn,
		udpNet:        udpNet,
		client:        c,
		allowedClient: addrFromIP(initialUDPAssociateClientIP(ipFromNetAddr(ctrl.RemoteAddr()))),
		peers:         newTTLSet[netip.AddrPort](256),
		logOnce:       newTTLSet[routeLogKey](1024),
	}
	sess.uotWriter = tunnel.NewUoTWriter(uotConn, func(error) { sess.close() })
	if c.tracker != nil {
		sess.flows = newUDPFlows(c.tracker, ctrl, proxyChain, closerFunc(sess.close))
	}
	return sess
}

func (s *uotClientSession) run() {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); s.consumeControl() }()
	go func() { defer wg.Done(); s.pipeClientToServer() }()
	go func() { defer wg.Done(); s.pipeServerToClient() }()
	wg.Wait()
	s.close()
}

func (s *uotClientSession) close() {
	s.closeOnce.Do(func() {
		s.uotWriter.Close()
		s.flows.close()
		_ = s.udpConn.Close()
		_ = s.uotConn.Close()
		_ = s.ctrlConn.Close()
	})
}

func (s *uotClientSession) consumeControl() {
	_, _ = io.Copy(io.Discard, s.ctrlConn)
	s.close()
}

func (s *uotClientSession) pipeClientToServer() {
	buf := make([]byte, 65535)
	for {
		n, from, err := s.udpConn.ReadFromUDPAddrPort(buf)
		if err != nil {
			s.close()
			return
		}
		// A dual-stack socket reports IPv4 sources as IPv4-mapped IPv6.
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if err := s.handlePacket(from, buf[:n]); err != nil {
			s.close()
			return
		}
	}
}

// handlePacket dispatches one datagram from the relay socket: either a reply from a direct peer or a
// SOCKS5 UDP request from the client. A non-nil error means a write failed and the session must end.
func (s *uotClientSession) handlePacket(from netip.AddrPort, pkt []byte) error {
	if s.allowedClient.IsValid() && s.allowedClient != from.Addr().WithZone("") {
		return s.relayPeerResponse(from, pkt)
	}
	return s.relayClientDatagram(from, pkt)
}

func (s *uotClientSession) relayPeerResponse(from netip.AddrPort, pkt []byte) error {
	if !s.peers.Has(from) {
		return nil
	}
	clientAddr := s.clientAddr.Load()
	if clientAddr == nil {
		return nil
	}
	s.reply = append(appendSocks5UDPHeader(s.reply[:0], from), pkt...)
	_, err := s.udpConn.WriteToUDPAddrPort(s.reply, *clientAddr)
	s.flows.receivedDirect(from, len(pkt))
	return err
}

func (s *uotClientSession) relayClientDatagram(from netip.AddrPort, pkt []byte) error {
	req, err := parseSocks5UDPRequest(pkt)
	if err != nil {
		return nil
	}
	if !s.allowedClient.IsValid() {
		s.allowedClient = from.Addr().WithZone("")
	}
	s.setClientAddr(from)

	decision := s.client.routing.Load().decide(req.destAddr, req.destIP)
	if decision.action == routeActionReject {
		s.logRouteOnce(from, req.destAddr, decision)
		return nil
	}

	var direct netip.AddrPort
	if !decision.shouldProxy() {
		var err error
		direct, err = s.directAddr(req)
		switch {
		case err != nil:
			decision.action = routeActionProxy
			decision.match += "/RESOLVE_FAIL"
		case direct.Addr().Is6() && s.udpNet == "udp4":
			decision.action = routeActionProxy
			decision.match += "/IPV6->PROXY"
		case !direct.Addr().Is6() && s.udpNet == "udp6":
			// A relay bound to an IPv6 loopback only (selected from the UDP
			// ASSOCIATE request family) cannot send to IPv4 directly. Proxy it
			// instead of tearing down the whole association on sendto failure.
			decision.action = routeActionProxy
			decision.match += "/IPV4->PROXY"
		default:
			s.peers.Add(direct, udpPeerTTL)
			if _, err := s.udpConn.WriteToUDPAddrPort(req.payload, direct); err != nil {
				return err
			}
			s.flows.sent(req.destAddr, decision, direct, len(req.payload))
		}
	}

	if decision.shouldProxy() {
		var err error
		if req.literal.IsValid() {
			err = s.uotWriter.WriteAddrPort(req.literal, req.payload)
		} else {
			err = s.uotWriter.WriteDatagram(req.destAddr, req.payload)
		}
		if err != nil {
			return err
		}
		s.flows.sent(req.destAddr, decision, netip.AddrPort{}, len(req.payload))
	}

	s.logRouteOnce(from, req.destAddr, decision)
	return nil
}

// directAddr is where a directly routed request goes, resolving domain names. IP destinations are used as is.
func (s *uotClientSession) directAddr(req udpRequest) (netip.AddrPort, error) {
	if req.literal.IsValid() {
		return req.literal, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), udpResolveTimeout)
	defer cancel()
	addr, err := resolveUDPAddr(ctx, req.destAddr, s.client.resolver)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return addr.AddrPort(), nil
}

func (s *uotClientSession) logRouteOnce(from netip.AddrPort, destAddr string, decision routeDecision) {
	if s.logOnce.Allow(routeLogKey{decision.action, destAddr}, udpLogQuietTTL) {
		logRoute("UDP", net.UDPAddrFromAddrPort(from), destAddr, decision)
	}
}

func (s *uotClientSession) pipeServerToClient() {
	var reply []byte
	datagrams := tunnel.NewUoTReader(s.uotConn)
	for {
		addr, payload, err := datagrams.ReadDatagram()
		if err != nil {
			s.close()
			return
		}

		clientAddr := s.clientAddr.Load()
		if clientAddr == nil {
			continue
		}
		s.flows.receivedProxy(addr, len(payload))
		var ok bool
		if reply, ok = appendUDPResponse(reply[:0], addr, payload); ok {
			if _, err := s.udpConn.WriteToUDPAddrPort(reply, *clientAddr); err != nil {
				s.close()
				return
			}
		}
	}
}

func (s *uotClientSession) setClientAddr(addr netip.AddrPort) {
	if cur := s.clientAddr.Load(); cur == nil || *cur != addr {
		s.clientAddr.Store(&addr)
	}
}
