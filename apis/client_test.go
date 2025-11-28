package apis

import (
	"crypto/sha256"
	"testing"
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
