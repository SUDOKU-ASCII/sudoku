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
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/SUDOKU-ASCII/sudoku/internal/protocol"
	"github.com/SUDOKU-ASCII/sudoku/pkg/connutil"
	"github.com/SUDOKU-ASCII/sudoku/pkg/logx"
)

// socks5UDPAssociateTimeout bounds the request read so a truncated request cannot pin the goroutine.
const socks5UDPAssociateTimeout = 10 * time.Second

func writeSocks5Reply(w io.Writer, rep byte) {
	// VER, REP, RSV, ATYP, BND.ADDR(IPv4=0.0.0.0), BND.PORT(0)
	_, _ = w.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func (c *client) handleSocks5(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nMethods]); err != nil {
		return
	}
	_, _ = conn.Write([]byte{0x05, 0x00}) // no auth

	header := make([]byte, 3)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}

	switch header[1] {
	case 0x01: // CONNECT
	case 0x03: // UDP ASSOCIATE
		c.handleSocks5UDPAssociate(conn)
		return
	default:
		writeSocks5Reply(conn, 0x07) // Command not supported
		return
	}

	destAddrStr, _, destIP, err := protocol.ReadAddress(conn)
	if err != nil {
		return
	}

	targetConn, decision, success := c.dialTarget("Socks5", conn, destAddrStr, destIP)
	if !success {
		if decision.action == routeActionReject {
			writeSocks5Reply(conn, 0x02) // Connection not allowed by ruleset
		} else {
			writeSocks5Reply(conn, 0x04) // Host unreachable
		}
		return
	}

	writeSocks5Reply(conn, 0x00)
	connutil.PipeConn(conn, targetConn)
}

func (c *client) handleSocks5UDPAssociate(ctrl net.Conn) {
	_ = ctrl.SetReadDeadline(time.Now().Add(socks5UDPAssociateTimeout))
	_, _, requestedIP, err := protocol.ReadAddress(ctrl)
	_ = ctrl.SetReadDeadline(time.Time{})
	if err != nil {
		writeSocks5Reply(ctrl, 0x01)
		return
	}
	localIP := ipFromNetAddr(ctrl.LocalAddr())
	remoteIP := ipFromNetAddr(ctrl.RemoteAddr())
	advertiseIP := udpAssociateAdvertiseIP(localIP, remoteIP, requestedIP)

	udpConn, udpNet, err := listenUDPAssociate(localIP, remoteIP, advertiseIP)
	if err != nil && advertiseIP != nil && advertiseIP.IsLoopback() {
		// The requested loopback family may not exist on this host (e.g. IPv6
		// disabled). Fall back to the control connection's family; opposite-
		// family destinations are still proxied by the session.
		advertiseIP = selectUDPAssociateAdvertiseIP(localIP, remoteIP)
		udpConn, udpNet, err = listenUDPAssociate(localIP, remoteIP, advertiseIP)
	}
	if err != nil {
		writeSocks5Reply(ctrl, 0x01)
		return
	}

	uotConn, chain, err := c.out.dialUoT(c.routing.Load().mode == "global")
	if err != nil {
		logx.Warnf("SOCKS5/UDP", "Dial Failed: %v", err)
		_ = udpConn.Close()
		writeSocks5Reply(ctrl, 0x01)
		return
	}

	reply := buildUDPAssociateReply(advertiseIP, udpConn.LocalAddr().(*net.UDPAddr).Port, localIP, remoteIP)
	if err := connutil.WriteFull(ctrl, reply); err != nil {
		_ = udpConn.Close()
		_ = uotConn.Close()
		return
	}

	logx.Infof("SOCKS5/UDP", "Associate ready on %s -> %s", udpConn.LocalAddr().String(), chain[0])
	newUoTClientSession(ctrl, udpConn, uotConn, udpNet, c, chain).run()
}

func buildUDPAssociateReply(host net.IP, port int, localIP net.IP, remoteIP net.IP) []byte {
	if host == nil {
		// Reply 0.0.0.0/:: to indicate "use the TCP endpoint host" (common SOCKS5 client behavior).
		host = net.IPv4zero
		if isIPv6Only(localIP, remoteIP) {
			host = net.IPv6unspecified
		}
	}

	out := make([]byte, 0, 32)
	out = append(out, 0x05, 0x00, 0x00)

	if ip4 := host.To4(); ip4 != nil {
		out = append(out, 0x01)
		out = append(out, ip4...)
	} else {
		out = append(out, 0x04)
		out = append(out, host.To16()...)
	}

	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	out = append(out, portBytes[:]...)
	return out
}
