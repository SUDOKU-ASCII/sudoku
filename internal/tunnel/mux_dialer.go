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
package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/SUDOKU-ASCII/sudoku/internal/protocol"
	"github.com/SUDOKU-ASCII/sudoku/pkg/obfs/httpmask"
)

// MuxDialer multiplexes multiple target connections over a single upgraded Sudoku tunnel.
//
// It keeps one long-lived Sudoku tunnel and opens lightweight sub-streams for
// each destination, regardless of whether the outer transport is raw TCP or HTTPMask.
type MuxDialer struct {
	BaseDialer

	mu         sync.Mutex
	creating   bool
	createDone chan struct{}
	createStop context.CancelFunc
	session    *muxSession
	closed     bool
}

func (d *MuxDialer) Dial(destAddrStr string) (net.Conn, error) {
	var addrBuf bytes.Buffer
	if err := protocol.WriteAddress(&addrBuf, destAddrStr); err != nil {
		return nil, fmt.Errorf("encode address failed: %w", err)
	}
	payload := addrBuf.Bytes()

	var lastErr error
	for range 2 {
		sess, err := d.getOrCreateSession(context.Background())
		if err != nil {
			return nil, err
		}
		st, err := openMuxStream(sess, payload)
		if err == nil {
			return st, nil
		}
		lastErr = err
		d.discardSession(sess)
	}
	return nil, fmt.Errorf("mux open failed: %w", lastErr)
}

func (d *MuxDialer) DialUDPOverTCP() (net.Conn, error) {
	// UoT uses a dedicated tunnel because it already multiplexes at the packet layer.
	return d.dialUoT()
}

func openMuxStream(sess *muxSession, payload []byte) (net.Conn, error) {
	streamID := sess.nextStreamID()
	st := newMuxStream(sess, streamID)
	sess.registerStream(st)
	if err := sess.sendFrame(muxFrameOpen, streamID, payload); err != nil {
		st.closeNoSend(err)
		sess.removeStream(streamID)
		return nil, err
	}
	return st, nil
}

// Warm establishes the native mux session. Split HTTPMask sessions wait for
// the downlink and the first upload to be accepted before mux is exposed.
func (d *MuxDialer) Warm(ctx context.Context) error {
	_, err := d.getOrCreateSession(ctx)
	return err
}

// Maintain keeps a warmed mux session available until ctx is canceled.
// notify receives nil whenever a session becomes ready and an error when a
// session fails or cannot be created.
func (d *MuxDialer) Maintain(ctx context.Context, notify func(error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	const (
		minBackoff = 250 * time.Millisecond
		maxBackoff = 5 * time.Second
	)
	backoff := minBackoff
	for {
		sess, err := d.getOrCreateSession(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if notify != nil {
				notify(err)
			}
			if !waitMuxRetry(ctx, backoff) {
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = minBackoff
		if notify != nil {
			notify(nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-sess.closed:
		}
		d.discardSession(sess)
		if ctx.Err() != nil {
			return
		}
		if notify != nil {
			notify(fmt.Errorf("mux session ended: %w", sess.closedErr()))
		}
		if !waitMuxRetry(ctx, backoff) {
			return
		}
	}
}

func waitMuxRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (d *MuxDialer) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	stop := d.createStop
	sess := d.session
	d.session = nil
	d.mu.Unlock()
	if stop != nil {
		stop()
	}
	if sess != nil {
		sess.closeWithError(io.ErrClosedPipe)
	}
	return nil
}

func (d *MuxDialer) getOrCreateSession(ctx context.Context) (*muxSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, net.ErrClosed
		}
		if sess := d.session; sess != nil && !sess.isClosed() {
			d.mu.Unlock()
			return sess, nil
		}
		if d.creating {
			done := d.createDone
			d.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		d.creating = true
		d.createDone = make(chan struct{})
		d.mu.Unlock()
		break
	}

	createCtx, stop := context.WithCancel(ctx)
	d.mu.Lock()
	if d.closed {
		d.creating = false
		close(d.createDone)
		d.createDone = nil
		d.mu.Unlock()
		stop()
		return nil, net.ErrClosed
	}
	d.createStop = stop
	d.mu.Unlock()

	sess, err := d.createSession(createCtx)
	stop()
	d.mu.Lock()
	d.createStop = nil
	if err == nil && !d.closed {
		d.session = sess
	} else if sess != nil {
		sess.closeWithError(net.ErrClosed)
	}
	d.creating = false
	close(d.createDone)
	d.createDone = nil
	closed := d.closed
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if closed {
		return nil, net.ErrClosed
	}
	return sess, nil
}

func (d *MuxDialer) createSession(ctx context.Context) (*muxSession, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("missing config")
	}
	if !d.Config.SessionMuxEnabled() {
		return nil, fmt.Errorf("mux requires multiplex=on (got %q)", d.Config.MultiplexMode())
	}

	baseConn, err := d.dialBaseContext(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() {
		_ = baseConn.Close()
	})
	defer stop()

	if err := WriteKIPMessage(baseConn, KIPTypeStartMux, nil); err != nil {
		_ = baseConn.Close()
		return nil, fmt.Errorf("mux start failed: %w", err)
	}
	if err := httpmask.WaitTunnelReady(ctx, baseConn); err != nil {
		_ = baseConn.Close()
		return nil, fmt.Errorf("mux warmup failed: %w", err)
	}
	if !stop() {
		_ = baseConn.Close()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	}
	sess := newMuxSession(baseConn, nil)
	sess.startKeepalive(muxKeepaliveInterval)
	return sess, nil
}

func (d *MuxDialer) discardSession(sess *muxSession) {
	if sess == nil {
		return
	}
	d.mu.Lock()
	if d.session == sess {
		d.session = nil
	}
	d.mu.Unlock()
}
