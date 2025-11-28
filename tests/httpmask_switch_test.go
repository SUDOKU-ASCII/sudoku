package tests

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/saba-futai/sudoku/apis"
	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

func TestHTTPMaskSwitch(t *testing.T) {
	// Setup server
	serverListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer serverListener.Close()

	serverAddr := serverListener.Addr().String()
	table := sudoku.NewTable("test-seed", "prefer_ascii")
	key := "test-key-123456"

	serverCfg := &apis.ProtocolConfig{
		Key:                     key,
		AEADMethod:              "chacha20-poly1305",
		Table:                   table,
		PaddingMin:              10,
		PaddingMax:              20,
		HandshakeTimeoutSeconds: 5,
		DisableHTTPMask:         false, // Server enables mask (but should auto-detect)
	}

	go func() {
		for {
			conn, err := serverListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tunnelConn, _, err := apis.ServerHandshake(c, serverCfg)
				if err != nil {
					// Handshake failed
					return
				}
				defer tunnelConn.Close()
				// Echo server
				io.Copy(tunnelConn, tunnelConn)
			}(conn)
		}
	}()

	// Test Case 1: Client with Mask (Default)
	t.Run("ClientWithMask", func(t *testing.T) {
		clientCfg := &apis.ProtocolConfig{
			ServerAddress:   serverAddr,
			TargetAddress:   "example.com:80",
			Key:             key,
			AEADMethod:      "chacha20-poly1305",
			Table:           table,
			PaddingMin:      10,
			PaddingMax:      20,
			DisableHTTPMask: false,
		}

		conn, err := apis.Dial(context.Background(), clientCfg)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()

		msg := []byte("hello masked")
		if _, err := conn.Write(msg); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if string(buf) != string(msg) {
			t.Fatalf("expected %s, got %s", msg, buf)
		}
	})

	// Test Case 2: Client without Mask
	t.Run("ClientWithoutMask", func(t *testing.T) {
		clientCfg := &apis.ProtocolConfig{
			ServerAddress:   serverAddr,
			TargetAddress:   "example.com:80",
			Key:             key,
			AEADMethod:      "chacha20-poly1305",
			Table:           table,
			PaddingMin:      10,
			PaddingMax:      20,
			DisableHTTPMask: true,
		}

		conn, err := apis.Dial(context.Background(), clientCfg)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()

		msg := []byte("hello unmasked")
		if _, err := conn.Write(msg); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if string(buf) != string(msg) {
			t.Fatalf("expected %s, got %s", msg, buf)
		}
	})
}
