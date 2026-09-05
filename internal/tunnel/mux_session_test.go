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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMuxSession_KeepaliveUsesPingAndAcceptsPong(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	session := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		session.closeWithError(net.ErrClosed)
		_ = peerConn.Close()
	})

	session.startPingKeepalive(10 * time.Millisecond)
	_ = peerConn.SetReadDeadline(time.Now().Add(time.Second))

	var header [muxHeaderSize]byte
	if _, err := io.ReadFull(peerConn, header[:]); err != nil {
		t.Fatalf("read keepalive: %v", err)
	}
	if header[0] != muxFramePing {
		t.Fatalf("keepalive frame type = %d, want PING", header[0])
	}
	if streamID := binary.BigEndian.Uint32(header[1:5]); streamID != 0 {
		t.Fatalf("keepalive stream id = %d, want 0", streamID)
	}
	if payloadLen := binary.BigEndian.Uint32(header[5:9]); payloadLen != 0 {
		t.Fatalf("keepalive payload length = %d, want 0", payloadLen)
	}
	_, _ = peerConn.Write(header[:])
}

func TestMuxSession_Echo(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)

		msg, err := ReadKIPMessage(serverConn)
		if err != nil {
			return
		}
		if msg.Type != KIPTypeStartMux {
			return
		}
		sess := newMuxSession(serverConn, func(stream *muxStream, _ []byte) {
			_, _ = io.Copy(stream, stream)
		})
		<-sess.closed
	}()

	if err := WriteKIPMessage(clientConn, KIPTypeStartMux, nil); err != nil {
		t.Fatalf("start mux: %v", err)
	}
	mux, err := NewMuxClient(clientConn)
	if err != nil {
		t.Fatalf("NewMuxClient: %v", err)
	}
	defer mux.Close()

	stream, err := mux.Dial("example.com:80")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stream.Close()

	msg := []byte("hello mux")
	if _, err := stream.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo mismatch: got %q want %q", buf, msg)
	}

	_ = mux.Close()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("server did not exit: %v", ctx.Err())
	}
}

func TestMuxStream_EnqueueRejectsOverflow(t *testing.T) {
	st := newMuxStream(nil, 1)
	payload := make([]byte, muxMaxDataPayload)

	for queued := 0; queued < muxMaxQueuedBytesPerStream; queued += len(payload) {
		if err := st.enqueue(payload); err != nil {
			t.Fatalf("enqueue within limit: %v", err)
		}
	}

	if err := st.enqueue(payload); !errors.Is(err, errMuxReceiveQueueFull) {
		t.Fatalf("enqueue overflow error = %v, want %v", err, errMuxReceiveQueueFull)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(st, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := st.enqueue(payload); err != nil {
		t.Fatalf("enqueue after consuming capacity: %v", err)
	}
}

func TestMuxStream_EnqueueAfterCloseDoesNotBlock(t *testing.T) {
	st := newMuxStream(nil, 1)
	payload := make([]byte, muxMaxDataPayload)

	for queued := 0; queued < muxMaxQueuedBytesPerStream; queued += len(payload) {
		if err := st.enqueue(payload); err != nil {
			t.Fatalf("enqueue within limit: %v", err)
		}
	}

	st.closeNoSend(io.ErrClosedPipe)
	if err := st.enqueue(payload); err != nil {
		t.Fatalf("enqueue after close: %v", err)
	}
}

func TestMuxSession_SlowStreamDoesNotBlockOtherStreams(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	opened := make(chan *muxStream, 2)
	serverSession := newMuxSession(serverConn, func(stream *muxStream, _ []byte) {
		opened <- stream
	})
	clientSession := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		clientSession.closeWithError(net.ErrClosed)
		serverSession.closeWithError(net.ErrClosed)
	})

	slowClient := newMuxStream(clientSession, 1)
	clientSession.registerStream(slowClient)
	if err := clientSession.sendFrame(muxFrameOpen, slowClient.id, nil); err != nil {
		t.Fatalf("open slow stream: %v", err)
	}

	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("server did not open slow stream")
	}

	payload := make([]byte, muxMaxDataPayload)
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		for queued := 0; queued <= muxMaxQueuedBytesPerStream; queued += len(payload) {
			if _, err := slowClient.Write(payload); err != nil {
				return
			}
		}
	}()

	select {
	case <-floodDone:
	case <-time.After(time.Second):
		t.Fatal("failed to fill slow stream queue")
	}

	fastClient := newMuxStream(clientSession, 2)
	clientSession.registerStream(fastClient)
	fastDone := make(chan error, 1)
	go func() {
		if err := clientSession.sendFrame(muxFrameOpen, fastClient.id, nil); err != nil {
			fastDone <- err
			return
		}
		_, err := fastClient.Write([]byte("still responsive"))
		fastDone <- err
	}()

	var fastServer *muxStream
	select {
	case fastServer = <-opened:
	case <-time.After(time.Second):
		t.Fatal("slow stream blocked the entire mux session")
	}

	buf := make([]byte, len("still responsive"))
	if _, err := io.ReadFull(fastServer, buf); err != nil {
		t.Fatalf("read fast stream: %v", err)
	}
	if string(buf) != "still responsive" {
		t.Fatalf("fast stream payload mismatch: %q", buf)
	}
	if err := <-fastDone; err != nil {
		t.Fatalf("write fast stream: %v", err)
	}
}

func TestMuxStream_CloseWritePreservesResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverDone := make(chan error, 1)
	serverSession := newMuxSession(serverConn, func(stream *muxStream, _ []byte) {
		request, err := io.ReadAll(stream)
		if err != nil {
			serverDone <- err
			return
		}
		if string(request) != "request" {
			serverDone <- errors.New("request payload mismatch")
			return
		}
		if _, err := stream.Write([]byte("response")); err != nil {
			serverDone <- err
			return
		}
		serverDone <- stream.CloseWrite()
	})
	clientSession := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		clientSession.closeWithError(net.ErrClosed)
		serverSession.closeWithError(net.ErrClosed)
	})

	clientStream := newMuxStream(clientSession, 1)
	clientSession.registerStream(clientStream)
	if err := clientSession.sendFrame(muxFrameOpen, clientStream.id, nil); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := clientStream.Write([]byte("request")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := clientStream.CloseWrite(); err != nil {
		t.Fatalf("close request side: %v", err)
	}

	response, err := io.ReadAll(clientStream)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "response" {
		t.Fatalf("response payload mismatch: %q", response)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish half-closed exchange")
	}
}

func TestMuxStream_RemoteCloseKeepsLocalWriteAfterCloseRead(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverDone := make(chan error, 1)
	serverSession := newMuxSession(serverConn, func(stream *muxStream, _ []byte) {
		serverDone <- stream.CloseWrite()
	})
	clientSession := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		clientSession.closeWithError(net.ErrClosed)
		serverSession.closeWithError(net.ErrClosed)
	})

	clientStream := newMuxStream(clientSession, 1)
	clientSession.registerStream(clientStream)
	if err := clientSession.sendFrame(muxFrameOpen, clientStream.id, nil); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server CloseWrite: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not close its write side")
	}
	if _, err := clientStream.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("wait for remote close: %v", err)
	}
	// Closing reads must not make the stream disappear while its write side
	// is still available for a request/response style exchange.
	if err := clientStream.CloseRead(); err != nil {
		t.Fatalf("CloseRead: %v", err)
	}
	if _, err := clientStream.Write([]byte("request")); err != nil {
		t.Fatalf("write after remote close: %v", err)
	}
	if err := clientStream.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
}

func TestMuxSession_ConcurrentStreamsRemainOrdered(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverSession := newMuxSession(serverConn, func(stream *muxStream, _ []byte) {
		_, _ = io.Copy(stream, stream)
		_ = stream.CloseWrite()
	})
	clientSession := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		clientSession.closeWithError(net.ErrClosed)
		serverSession.closeWithError(net.ErrClosed)
	})

	const streamCount = 32
	var wg sync.WaitGroup
	errCh := make(chan error, streamCount)
	for i := 0; i < streamCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := openMuxStream(clientSession, []byte(fmt.Sprintf("target-%d", i)))
			if err != nil {
				errCh <- err
				return
			}
			defer st.Close()
			payload := bytes.Repeat([]byte{byte(i)}, 96*1024+17)
			if _, err := st.Write(payload); err != nil {
				errCh <- err
				return
			}
			closeWriter, ok := st.(interface{ CloseWrite() error })
			if !ok {
				errCh <- errors.New("mux stream does not support CloseWrite")
				return
			}
			if err := closeWriter.CloseWrite(); err != nil {
				errCh <- err
				return
			}
			got, err := io.ReadAll(st)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got, payload) {
				errCh <- fmt.Errorf("stream %d payload mismatch: got %d bytes, want %d", i, len(got), len(payload))
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}
