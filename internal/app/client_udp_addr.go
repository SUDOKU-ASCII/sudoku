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
	"net"
	"net/netip"
)

func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip.To16()
}

// ipFromNetAddr returns the canonical IP of addr, or nil when it has none.
// The UDP-associate helpers below expect their IP arguments to come from here.
func ipFromNetAddr(addr net.Addr) net.IP {
	if addr == nil {
		return nil
	}
	switch a := addr.(type) {
	case *net.TCPAddr:
		return normalizeIP(a.IP)
	case *net.UDPAddr:
		return normalizeIP(a.IP)
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return nil
		}
		return normalizeIP(net.ParseIP(host))
	}
}

// isIPv6Only reports whether the control connection runs over IPv6, preferring the local end.
func isIPv6Only(localIP net.IP, remoteIP net.IP) bool {
	if localIP != nil {
		return localIP.To4() == nil
	}
	return remoteIP != nil && remoteIP.To4() == nil
}

func udpNetworkAndBindIP(localIP net.IP, remoteIP net.IP) (string, net.IP) {
	// Best-effort family match with the TCP control peer.
	if remoteIP != nil {
		if remoteIP.To4() != nil {
			return "udp4", net.IPv4zero
		}
		return "udp6", net.IPv6unspecified
	}

	// Unknown peer family: try dual-stack first when possible.
	if isIPv6Only(localIP, remoteIP) {
		return "udp6", net.IPv6unspecified
	}
	return "udp", nil
}

func sameIPFamily(a, b net.IP) bool {
	return (a.To4() != nil) == (b.To4() != nil)
}

// listenUDPAssociate binds the relay socket on bindIP when one is advertised, otherwise on a dual-stack
// socket if its family suits the control peer, otherwise on a family-specific one.
func listenUDPAssociate(localIP net.IP, remoteIP net.IP, bindIP net.IP) (*net.UDPConn, string, error) {
	if bindIP != nil && !bindIP.IsUnspecified() {
		udpNet := "udp6"
		if bindIP.To4() != nil {
			udpNet = "udp4"
		}
		c, err := net.ListenUDP(udpNet, &net.UDPAddr{IP: bindIP})
		return c, udpNet, err
	}

	if udpConn, err := net.ListenUDP("udp", &net.UDPAddr{}); err == nil {
		// Some platforms bind "udp" IPv6-only, which a peer of the other family cannot reach.
		if remoteIP == nil || sameIPFamily(remoteIP, udpConn.LocalAddr().(*net.UDPAddr).IP) {
			return udpConn, "udp", nil
		}
		_ = udpConn.Close()
	}

	udpNet, udpBindIP := udpNetworkAndBindIP(localIP, remoteIP)
	c, err := net.ListenUDP(udpNet, &net.UDPAddr{IP: udpBindIP})
	return c, udpNet, err
}

// selectUDPAssociateAdvertiseIP picks the BND.ADDR to advertise, or nil to tell the client to reuse
// the TCP endpoint host. Local clients keep the control connection's loopback address
// and IP family; LAN clients keep the gateway address they actually connected to.
func selectUDPAssociateAdvertiseIP(localIP net.IP, remoteIP net.IP) net.IP {
	switch {
	case localIP == nil || localIP.IsUnspecified():
		return nil
	case localIP.IsLoopback():
		if remoteIP != nil && remoteIP.IsLoopback() && sameIPFamily(localIP, remoteIP) {
			return localIP
		}
		return nil
	case !localIP.IsGlobalUnicast():
		return nil
	case localIP.IsPrivate() && (remoteIP == nil || !remoteIP.IsPrivate()):
		return nil
	}
	return localIP
}

// initialUDPAssociateClientIP is the datagram source allowed before the first packet arrives.
// Loopback and wildcard control peers (e.g. behind a port forward) are not pinned.
func initialUDPAssociateClientIP(ip net.IP) net.IP {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return nil
	}
	return ip
}

// addrFromIP converts ip to an unmapped netip.Addr; nil gives the invalid zero Addr.
func addrFromIP(ip net.IP) netip.Addr {
	a, _ := netip.AddrFromSlice(ip)
	return a.Unmap()
}

// A local SOCKS client may use a UDP family different from its TCP control
// connection (for example dae's IPv6 UDP flows through an IPv4 proxy URL).
// Honor concrete request addresses only for loopback peers. LAN peers keep
// the actual gateway endpoint; never advertise loopback to a remote client.
func udpAssociateAdvertiseIP(localIP, remoteIP, requestedIP net.IP) net.IP {
	if localIP != nil && remoteIP != nil && localIP.IsLoopback() && remoteIP.IsLoopback() && requestedIP != nil && !requestedIP.IsUnspecified() {
		if requestedIP.To4() != nil {
			return net.IPv4(127, 0, 0, 1)
		}
		return net.IPv6loopback
	}
	return selectUDPAssociateAdvertiseIP(localIP, remoteIP)
}
