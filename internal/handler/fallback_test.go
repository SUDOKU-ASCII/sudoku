package handler

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/saba-futai/sudoku/internal/config"
)

type recordedConn struct {
	net.Conn
	data []byte
}

func (r *recordedConn) GetBufferedAndRecorded() []byte {
	return r.data
}

func TestHandleSuspiciousFallback(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	got := make(chan []byte, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		all, _ := io.ReadAll(conn)
		got <- all
	}()

	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	cfg := &config.Config{
		FallbackAddr: l.Addr().String(),
	}

	wrapper := &recordedConn{Conn: serverSide, data: []byte("bad")}

	go HandleSuspicious(wrapper, serverSide, cfg)

	// Write extra data that should also be forwarded
	if _, err := clientSide.Write([]byte("tail")); err != nil {
		t.Fatalf("write: %v", err)
	}
	clientSide.Close()

	select {
	case data := <-got:
		if string(data) != "badtail" {
			t.Fatalf("unexpected fallback data: %q", string(data))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("fallback did not receive data")
	}
}
