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
	"testing"
	"time"
)

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

func TestSelectUDPAssociateAdvertiseIP(t *testing.T) {
	tests := []struct {
		name          string
		local, remote string
		want          string
	}{
		{name: "no local address", local: "", remote: "10.0.0.2", want: ""},
		{name: "wildcard local address", local: "0.0.0.0", remote: "10.0.0.2", want: ""},
		{name: "ipv4 loopback remains ipv4 loopback", local: "127.0.0.1", remote: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback remains ipv6 loopback", local: "::1", remote: "::1", want: "::1"},
		{name: "loopback with unknown peer uses endpoint fallback", local: "127.0.0.1", want: ""},
		{name: "loopback with different family uses endpoint fallback", local: "::1", remote: "127.0.0.1", want: ""},
		{name: "LAN gateway is retained", local: "172.16.0.1", remote: "172.16.1.10", want: "172.16.0.1"},
		{name: "LAN IPv6 gateway is retained", local: "fd47:2412:2232::1", remote: "fd47:2412:2232::10", want: "fd47:2412:2232::1"},
		{name: "loopback is not advertised to remote peer", local: "::1", remote: "172.19.0.2", want: ""},
		{name: "link-local is not advertised", local: "fe80::1", remote: "fe80::2", want: ""},
		{name: "private local with private peer", local: "192.168.1.5", remote: "192.168.1.9", want: "192.168.1.5"},
		{name: "private local with public peer", local: "192.168.1.5", remote: "8.8.8.8", want: ""},
		{name: "private local with unknown peer", local: "192.168.1.5", remote: "", want: ""},
		{name: "public local", local: "203.0.113.7", remote: "8.8.8.8", want: "203.0.113.7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectUDPAssociateAdvertiseIP(net.ParseIP(tc.local), net.ParseIP(tc.remote))
			if ipString(got) != tc.want {
				t.Fatalf("selectUDPAssociateAdvertiseIP(%q, %q) = %q, want %q", tc.local, tc.remote, ipString(got), tc.want)
			}
		})
	}
}

func TestUDPAssociateLoopbackAddressFamily(t *testing.T) {
	for _, tc := range []struct {
		host, network string
		atyp          byte
	}{
		{"127.0.0.1", "udp4", 1}, {"::1", "udp6", 4},
	} {
		t.Run(tc.network, func(t *testing.T) {
			local := net.ParseIP(tc.host)
			advertised := selectUDPAssociateAdvertiseIP(local, local)
			relay, network, err := listenUDPAssociate(local, local, advertised)
			if err != nil {
				if tc.network == "udp6" {
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				t.Fatal(err)
			}
			defer relay.Close()
			relay.SetDeadline(time.Now().Add(2 * time.Second))
			if network != tc.network {
				t.Fatalf("network = %s, want %s", network, tc.network)
			}
			addr := relay.LocalAddr().(*net.UDPAddr)
			if !addr.IP.Equal(local) {
				t.Fatalf("relay = %s, want %s", addr.IP, local)
			}
			reply := buildUDPAssociateReply(advertised, addr.Port, local, local)
			if reply[3] != tc.atyp {
				t.Fatalf("reply family = %d, want %d", reply[3], tc.atyp)
			}
			peer, err := net.DialUDP(tc.network, nil, addr)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			peer.SetDeadline(time.Now().Add(2 * time.Second))
			payload := []byte("SOCKS5 UDP loopback")
			if _, err := peer.Write(payload); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 128)
			n, src, err := relay.ReadFromUDP(buf)
			if err != nil {
				t.Fatal(err)
			}
			if string(buf[:n]) != string(payload) {
				t.Fatalf("unexpected request: %q", buf[:n])
			}
			if _, err := relay.WriteToUDP(buf[:n], src); err != nil {
				t.Fatal(err)
			}
			n, err = peer.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if string(buf[:n]) != string(payload) {
				t.Fatalf("unexpected reply: %q", buf[:n])
			}
		})
	}
}

func TestInitialUDPAssociateClientIP(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
		want string
	}{
		{name: "nil stays nil", ip: nil, want: ""},
		{name: "loopback becomes nil", ip: net.ParseIP("127.0.0.1"), want: ""},
		{name: "wildcard becomes nil", ip: net.IPv6unspecified, want: ""},
		{name: "private ipv4 is kept", ip: net.ParseIP("172.19.0.2"), want: "172.19.0.2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ipString(initialUDPAssociateClientIP(tc.ip)); got != tc.want {
				t.Fatalf("initialUDPAssociateClientIP(%v) = %q, want %q", tc.ip, got, tc.want)
			}
		})
	}
}

func TestBuildUDPAssociateReply(t *testing.T) {
	tests := []struct {
		name          string
		host          string
		local, remote string
		want          []byte
	}{
		{name: "explicit ipv4", host: "10.0.0.148", want: []byte{5, 0, 0, 1, 10, 0, 0, 148, 0x04, 0xd2}},
		{name: "explicit ipv6", host: "2001:db8::1", want: append([]byte{5, 0, 0, 4}, append(net.ParseIP("2001:db8::1").To16(), 0x04, 0xd2)...)},
		{name: "wildcard for ipv4 control peer", local: "192.168.1.5", remote: "192.168.1.9", want: []byte{5, 0, 0, 1, 0, 0, 0, 0, 0x04, 0xd2}},
		{name: "wildcard for ipv6 control peer", local: "::1", remote: "::1", want: append([]byte{5, 0, 0, 4}, append(net.IPv6unspecified.To16(), 0x04, 0xd2)...)},
		{name: "wildcard for unknown peers", want: []byte{5, 0, 0, 1, 0, 0, 0, 0, 0x04, 0xd2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUDPAssociateReply(net.ParseIP(tc.host), 1234, net.ParseIP(tc.local), net.ParseIP(tc.remote))
			if string(got) != string(tc.want) {
				t.Fatalf("reply = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUDPAssociateRequestedFamily(t *testing.T) {
	tests := []struct{ name, local, remote, requested, want string }{
		{"local IPv4 control IPv6 UDP", "127.0.0.1", "127.0.0.1", "2001:4860:4860::8888", "::1"},
		{"local IPv6 control IPv4 UDP", "::1", "::1", "8.8.8.8", "127.0.0.1"},
		{"local IPv4 concrete peer", "127.0.0.1", "127.0.0.1", "127.0.0.1", "127.0.0.1"},
		{"local IPv6 concrete peer", "::1", "::1", "::1", "::1"},
		{"IPv4 unspecified keeps TCP endpoint", "::1", "::1", "0.0.0.0", "::1"},
		{"IPv6 unspecified keeps TCP endpoint", "127.0.0.1", "127.0.0.1", "::", "127.0.0.1"},
		{"domain keeps TCP endpoint", "127.0.0.1", "127.0.0.1", "", "127.0.0.1"},
		{"LAN IPv4 gateway unchanged", "172.16.0.1", "172.16.215.83", "8.8.8.8", "172.16.0.1"},
		{"LAN IPv4 gateway with IPv6 target unchanged", "172.16.0.1", "172.16.215.83", "2001:4860:4860::8888", "172.16.0.1"},
		{"LAN IPv6 gateway with IPv4 target unchanged", "fd47:2412:2232::1", "fd47:2412:2232::10", "8.8.8.8", "fd47:2412:2232::1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := udpAssociateAdvertiseIP(net.ParseIP(tc.local), net.ParseIP(tc.remote), net.ParseIP(tc.requested))
			if ipString(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
