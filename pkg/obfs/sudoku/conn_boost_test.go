package sudoku

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"testing"
)

func TestBoostRoundTripASCII(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	table := NewTable("boost-key", "prefer_ascii")

	srv := NewConn(serverRaw, table, 0, 0, false)
	cli := NewConn(clientRaw, table, 0, 0, false)

	key := sha256.Sum256([]byte("boost-key"))
	iv := bytes.Repeat([]byte{0xAB}, 16)

	if err := srv.EnableBoost(true, false, key[:], iv, true); err != nil {
		t.Fatalf("enable boost on server: %v", err)
	}
	if err := cli.EnableBoost(false, true, key[:], iv, true); err != nil {
		t.Fatalf("enable boost on client: %v", err)
	}

	payload := []byte{0x3a, 0x1f, 0x71, 0x42, 0x99, 0x10, 0x7c}

	go func() {
		defer srv.Close()
		if _, err := srv.Write(payload); err != nil {
			t.Errorf("server write: %v", err)
		}
	}()

	readBuf := make([]byte, len(payload))
	if _, err := io.ReadFull(cli, readBuf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("payload mismatch: got %x want %x", readBuf, payload)
	}
	cli.Close()
}

func TestBoostRoundTripLargeEntropy(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	table := NewTable("boost-key-entropy", "prefer_entropy")

	srv := NewConn(serverRaw, table, 0, 0, false)
	cli := NewConn(clientRaw, table, 0, 0, false)

	key := sha256.Sum256([]byte("boost-key-entropy"))
	iv := bytes.Repeat([]byte{0x11}, 16)

	if err := srv.EnableBoost(true, false, key[:], iv, false); err != nil {
		t.Fatalf("enable boost on server: %v", err)
	}
	if err := cli.EnableBoost(false, true, key[:], iv, false); err != nil {
		t.Fatalf("enable boost on client: %v", err)
	}

	payload := make([]byte, 1<<20+123) // ~1MB+ padding
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("fill payload: %v", err)
	}

	go func() {
		defer srv.Close()
		if _, err := srv.Write(payload); err != nil {
			t.Errorf("server write: %v", err)
		}
	}()

	readBuf := make([]byte, len(payload))
	if _, err := io.ReadFull(cli, readBuf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("payload mismatch")
	}
	cli.Close()
}
