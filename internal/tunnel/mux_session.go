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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	muxFrameOpen  byte = 0x01
	muxFrameData  byte = 0x02
	muxFrameClose byte = 0x03
	muxFrameReset byte = 0x04
)

const (
	muxHeaderSize = 1 + 4 + 4
	// muxMaxQueuedBytesPerStream bounds unread payload retained by a single logical stream.
	// A stream that exceeds the limit is reset so it cannot block the shared demux loop.
	muxMaxQueuedBytesPerStream = 4 * 1024 * 1024
	muxMaxFrameSize            = 256 * 1024
	// Larger data frames materially reduce per-frame lock/copy overhead for
	// single-tunnel large downloads while still staying well below the hard cap.
	muxMaxDataPayload    = 128 * 1024
	muxKeepaliveInterval = 15 * time.Second
)

var errMuxReceiveQueueFull = errors.New("mux receive queue full")

type muxSession struct {
	conn net.Conn

	writeMu sync.Mutex

	streamsMu sync.Mutex
	streams   map[uint32]*muxStream
	nextID    uint32

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error

	lastWrite     atomic.Int64
	keepaliveOnce sync.Once

	onOpen func(stream *muxStream, payload []byte)
}

func newMuxSession(conn net.Conn, onOpen func(stream *muxStream, payload []byte)) *muxSession {
	s := &muxSession{
		conn:    conn,
		streams: make(map[uint32]*muxStream),
		closed:  make(chan struct{}),
		onOpen:  onOpen,
	}
	s.lastWrite.Store(time.Now().UnixNano())
	go s.readLoop()
	return s
}

func (s *muxSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *muxSession) closedErr() error {
	s.streamsMu.Lock()
	err := s.closeErr
	s.streamsMu.Unlock()
	if err == nil {
		return io.ErrClosedPipe
	}
	return err
}

func (s *muxSession) closeWithError(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	s.closeOnce.Do(func() {
		s.streamsMu.Lock()
		if s.closeErr == nil {
			s.closeErr = err
		}
		streams := make([]*muxStream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		s.streams = make(map[uint32]*muxStream)
		s.streamsMu.Unlock()

		for _, st := range streams {
			st.closeNoSend(err)
		}

		close(s.closed)
		_ = s.conn.Close()
	})
}

func (s *muxSession) registerStream(st *muxStream) {
	s.streamsMu.Lock()
	s.streams[st.id] = st
	s.streamsMu.Unlock()
}

func (s *muxSession) getStream(id uint32) *muxStream {
	s.streamsMu.Lock()
	st := s.streams[id]
	s.streamsMu.Unlock()
	return st
}

func (s *muxSession) removeStream(id uint32) {
	s.streamsMu.Lock()
	delete(s.streams, id)
	s.streamsMu.Unlock()
}

func (s *muxSession) nextStreamID() uint32 {
	s.streamsMu.Lock()
	s.nextID++
	id := s.nextID
	if id == 0 {
		s.nextID++
		id = s.nextID
	}
	s.streamsMu.Unlock()
	return id
}

func (s *muxSession) sendFrame(frameType byte, streamID uint32, payload []byte) error {
	if s.isClosed() {
		return s.closedErr()
	}
	if len(payload) > muxMaxFrameSize {
		return fmt.Errorf("mux payload too large: %d", len(payload))
	}

	var header [muxHeaderSize]byte
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:5], streamID)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := writeFull(s.conn, header[:]); err != nil {
		s.closeWithError(err)
		return err
	}
	if len(payload) > 0 {
		if err := writeFull(s.conn, payload); err != nil {
			s.closeWithError(err)
			return err
		}
	}
	s.lastWrite.Store(time.Now().UnixNano())
	return nil
}

func (s *muxSession) startKeepalive(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	s.keepaliveOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					lastWrite := time.Unix(0, s.lastWrite.Load())
					if time.Since(lastWrite) < interval {
						continue
					}
					// Stream 0 is never allocated. Existing peers, including
					// v0.4.7, ignore DATA for unknown streams.
					if err := s.sendFrame(muxFrameData, 0, nil); err != nil {
						return
					}
				case <-s.closed:
					return
				}
			}
		}()
	})
}

func (s *muxSession) sendReset(streamID uint32, msg string) {
	// Best-effort: ignore errors (session is probably already failing).
	if msg == "" {
		msg = "reset"
	}
	_ = s.sendFrame(muxFrameReset, streamID, []byte(msg))
	_ = s.sendFrame(muxFrameClose, streamID, nil)
}

func (s *muxSession) readLoop() {
	var header [muxHeaderSize]byte
	for {
		if _, err := io.ReadFull(s.conn, header[:]); err != nil {
			s.closeWithError(err)
			return
		}
		frameType := header[0]
		streamID := binary.BigEndian.Uint32(header[1:5])
		payloadLen := binary.BigEndian.Uint32(header[5:9])
		if payloadLen > muxMaxFrameSize {
			s.closeWithError(fmt.Errorf("invalid mux frame length: %d", payloadLen))
			return
		}
		n := int(payloadLen)

		var payload []byte
		if n > 0 {
			payload = make([]byte, n)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				s.closeWithError(err)
				return
			}
		}

		switch frameType {
		case muxFrameOpen:
			if s.onOpen == nil {
				s.sendReset(streamID, "unexpected open")
				continue
			}
			if streamID == 0 {
				s.sendReset(streamID, "invalid stream id")
				continue
			}
			if existing := s.getStream(streamID); existing != nil {
				s.sendReset(streamID, "stream already exists")
				continue
			}
			st := newMuxStream(s, streamID)
			s.registerStream(st)
			// Avoid blocking the demux loop on dial/IO.
			go s.onOpen(st, payload)

		case muxFrameData:
			st := s.getStream(streamID)
			if st == nil {
				// Unknown stream; ignore to avoid killing the whole session.
				continue
			}
			if len(payload) == 0 {
				continue
			}
			if err := st.enqueue(payload); err != nil {
				st.closeNoSend(err)
				s.removeStream(streamID)
				// Sending a reset must not hold up unrelated streams.
				go s.sendReset(streamID, err.Error())
			}

		case muxFrameClose:
			st := s.getStream(streamID)
			if st == nil {
				continue
			}
			if st.closeRemoteWrite() {
				s.removeStream(streamID)
			}

		case muxFrameReset:
			st := s.getStream(streamID)
			if st == nil {
				continue
			}
			msg := strings.TrimSpace(string(payload))
			if msg == "" {
				msg = "reset"
			}
			st.closeNoSend(errors.New(msg))
			s.removeStream(streamID)

		default:
			s.closeWithError(fmt.Errorf("unknown mux frame type: %d", frameType))
			return
		}
	}
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

type muxStream struct {
	session *muxSession
	id      uint32

	writeMu sync.Mutex

	mu                sync.Mutex
	cond              *sync.Cond
	closed            bool
	localReadClosed   bool
	localWriteClosed  bool
	remoteWriteClosed bool
	closeErr          error
	readBuf           []byte
	queue             [][]byte
	// queuedBytes includes unread bytes in readBuf and queue.
	queuedBytes int

	localAddr  net.Addr
	remoteAddr net.Addr
}

func newMuxStream(session *muxSession, id uint32) *muxStream {
	st := &muxStream{
		session:    session,
		id:         id,
		localAddr:  &net.TCPAddr{},
		remoteAddr: &net.TCPAddr{},
	}
	st.cond = sync.NewCond(&st.mu)
	return st
}

func (c *muxStream) closeNoSend(err error) {
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *muxStream) closeRemoteWrite() bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	c.remoteWriteClosed = true
	// A remote CLOSE only ends the remote-to-local direction. Keep the
	// stream registered while the local write side is still usable; removing
	// it here would silently discard a valid response after CloseRead.
	remove := c.localWriteClosed && c.remoteWriteClosed
	c.cond.Broadcast()
	c.mu.Unlock()
	return remove
}

func (c *muxStream) closedErr() error {
	c.mu.Lock()
	err := c.closedErrLocked()
	c.mu.Unlock()
	return err
}

func (c *muxStream) closedErrLocked() error {
	if c.closeErr == nil {
		return io.ErrClosedPipe
	}
	return c.closeErr
}

func (c *muxStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for len(c.readBuf) == 0 && len(c.queue) == 0 && !c.closed && !c.localReadClosed && !c.remoteWriteClosed {
		c.cond.Wait()
	}
	if len(c.readBuf) == 0 && len(c.queue) > 0 {
		c.readBuf = c.queue[0]
		c.queue[0] = nil
		c.queue = c.queue[1:]
		if len(c.queue) == 0 {
			c.queue = nil
		}
	}
	if len(c.readBuf) == 0 {
		switch {
		case c.closed:
			return 0, c.closedErrLocked()
		case c.localReadClosed:
			return 0, io.ErrClosedPipe
		case c.remoteWriteClosed:
			return 0, io.EOF
		}
	}

	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	if len(c.readBuf) == 0 {
		c.readBuf = nil
	}
	c.queuedBytes -= n
	if c.queuedBytes < 0 {
		c.queuedBytes = 0
	}
	c.cond.Broadcast()
	return n, nil
}

func (c *muxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.session.isClosed() {
		return 0, c.session.closedErr()
	}
	c.mu.Lock()
	closed := c.closed
	writeClosed := c.localWriteClosed
	c.mu.Unlock()
	if closed {
		return 0, c.closedErr()
	}
	if writeClosed {
		return 0, io.ErrClosedPipe
	}

	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > muxMaxDataPayload {
			chunk = p[:muxMaxDataPayload]
		}
		if err := c.session.sendFrame(muxFrameData, c.id, chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (c *muxStream) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	sendClose := !c.localWriteClosed
	c.closed = true
	c.localReadClosed = true
	c.localWriteClosed = true
	if c.closeErr == nil {
		c.closeErr = io.ErrClosedPipe
	}
	c.readBuf = nil
	c.queue = nil
	c.queuedBytes = 0
	c.cond.Broadcast()
	c.mu.Unlock()

	if c.session != nil {
		if sendClose {
			_ = c.session.sendFrame(muxFrameClose, c.id, nil)
		}
		c.session.removeStream(c.id)
	}
	return nil
}

func (c *muxStream) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	if c.closed || c.localWriteClosed {
		c.mu.Unlock()
		return nil
	}
	c.localWriteClosed = true
	remove := c.remoteWriteClosed || c.localReadClosed
	c.mu.Unlock()

	if c.session == nil {
		return nil
	}
	err := c.session.sendFrame(muxFrameClose, c.id, nil)
	if remove {
		c.session.removeStream(c.id)
	}
	return err
}

func (c *muxStream) CloseRead() error {
	c.mu.Lock()
	if c.closed || c.localReadClosed {
		c.mu.Unlock()
		return nil
	}
	c.localReadClosed = true
	c.readBuf = nil
	c.queue = nil
	c.queuedBytes = 0
	remove := c.localWriteClosed
	c.cond.Broadcast()
	c.mu.Unlock()

	if remove && c.session != nil {
		c.session.removeStream(c.id)
	}
	return nil
}

func (c *muxStream) LocalAddr() net.Addr  { return c.localAddr }
func (c *muxStream) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *muxStream) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	_ = c.SetWriteDeadline(t)
	return nil
}
func (c *muxStream) SetReadDeadline(time.Time) error  { return nil }
func (c *muxStream) SetWriteDeadline(time.Time) error { return nil }

func (c *muxStream) enqueue(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.localReadClosed || c.remoteWriteClosed {
		return nil
	}
	if c.queuedBytes+len(payload) > muxMaxQueuedBytesPerStream {
		return errMuxReceiveQueueFull
	}
	c.queuedBytes += len(payload)
	if len(c.readBuf) == 0 && len(c.queue) == 0 {
		c.readBuf = payload
	} else {
		c.queue = append(c.queue, payload)
	}
	c.cond.Broadcast()
	return nil
}
