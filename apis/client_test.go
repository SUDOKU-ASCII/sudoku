package apis

import (
	"context"
	"crypto/sha256"
	"net"
	"testing"
	"time"

	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

func TestBuildHandshakePayload(t *testing.T) {
	key := "handshake-key"
	p := buildHandshakePayload(key)

	if len(p) != 16 {
		t.Fatalf("unexpected length %d", len(p))
	}
	allZero := true
	for i := 0; i < 8; i++ {
		if p[i] != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatalf("timestamp appears zero")
	}

	hash := sha256.Sum256([]byte(key))
	for i := 0; i < 8; i++ {
		if p[8+i] != hash[i] {
			t.Fatalf("hash segment mismatch at %d", i)
		}
	}
}

func TestDialUoTHandshake(t *testing.T) {
	table := sudoku.NewTable("uot-key", "prefer_ascii")
	cfg := &ProtocolConfig{
		Table:                   table,
		Key:                     "uot-key",
		AEADMethod:              "chacha20-poly1305",
		PaddingMin:              0,
		PaddingMax:              0,
		HandshakeTimeoutSeconds: 2,
		DisableHTTPMask:         true,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srvCfg := *cfg

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		c, _, isUoT, err := ServerHandshakeWithUoT(conn, &srvCfg)
		if err != nil {
			t.Errorf("server handshake: %v", err)
			return
		}
		if !isUoT {
			t.Errorf("expected UoT session")
		}
		c.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg.ServerAddress = ln.Addr().String()

	clientConn, err := DialUoT(ctx, cfg)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	clientConn.Close()
}
