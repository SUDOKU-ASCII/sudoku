package crypto

import (
	"io"
	"net"
	"testing"
)

func TestAEADConnRoundTrip_Chacha(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	connA, err := NewAEADConn(left, "secret-key", "chacha20-poly1305")
	if err != nil {
		t.Fatalf("NewAEADConn A error: %v", err)
	}
	connB, err := NewAEADConn(right, "secret-key", "chacha20-poly1305")
	if err != nil {
		t.Fatalf("NewAEADConn B error: %v", err)
	}

	msg := []byte("hello aead")
	go func() {
		defer connA.Close()
		if _, err := connA.Write(msg); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}()

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(connB, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("payload mismatch, got %q", string(buf))
	}
}

func TestAEADConnNone_Passthrough(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	connA, err := NewAEADConn(left, "ignored", "none")
	if err != nil {
		t.Fatalf("NewAEADConn A error: %v", err)
	}
	connB, err := NewAEADConn(right, "ignored", "none")
	if err != nil {
		t.Fatalf("NewAEADConn B error: %v", err)
	}

	msg := []byte("plain text")
	go connA.Write(msg)

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(connB, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("payload mismatch, got %q", string(buf))
	}
}

func TestAEADConnUnsupported(t *testing.T) {
	if _, err := NewAEADConn(nil, "key", "invalid"); err == nil {
		t.Fatalf("expected error for unsupported cipher")
	}
}
