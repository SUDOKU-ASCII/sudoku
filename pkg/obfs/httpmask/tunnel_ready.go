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
	"context"
	"net"
	"sync"

	"github.com/SUDOKU-ASCII/sudoku/pkg/connutil"
)

type tunnelReadiness struct {
	pullReady chan struct{}
	pushReady chan struct{}
	pullOnce  sync.Once
	pushOnce  sync.Once
}

func newTunnelReadiness() *tunnelReadiness {
	return &tunnelReadiness{
		pullReady: make(chan struct{}),
		pushReady: make(chan struct{}),
	}
}

func (r *tunnelReadiness) markPullReady() {
	if r != nil {
		r.pullOnce.Do(func() { close(r.pullReady) })
	}
}

func (r *tunnelReadiness) markPushReady() {
	if r != nil {
		r.pushOnce.Do(func() { close(r.pushReady) })
	}
}

func (r *tunnelReadiness) wait(ctx context.Context, closed <-chan struct{}, closedErr func() error) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, ready := range []<-chan struct{}{r.pullReady, r.pushReady} {
		select {
		case <-ready:
		case <-closed:
			return closedErr()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type readyTunnelConn struct {
	net.Conn
	waitReady func(context.Context) error
}

func (c *readyTunnelConn) CloseWrite() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseWrite(c.Conn)
}

func (c *readyTunnelConn) CloseRead() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseRead(c.Conn)
}

func (c *readyTunnelConn) waitHTTPMaskReady(ctx context.Context) error {
	if c == nil || c.waitReady == nil {
		return nil
	}
	return c.waitReady(ctx)
}

type tunnelReadyConn interface {
	waitHTTPMaskReady(context.Context) error
}

func wrapReadyTunnelConn(conn net.Conn, waitReady func(context.Context) error) net.Conn {
	if conn == nil || waitReady == nil {
		return conn
	}
	return &readyTunnelConn{Conn: conn, waitReady: waitReady}
}

// WaitTunnelReady waits for a split HTTP tunnel's downlink and first upload.
// Other transports are ready when DialTunnel returns and complete immediately.
func WaitTunnelReady(ctx context.Context, conn net.Conn) error {
	if ready, ok := conn.(tunnelReadyConn); ok {
		return ready.waitHTTPMaskReady(ctx)
	}
	return nil
}
