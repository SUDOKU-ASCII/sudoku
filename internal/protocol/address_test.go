package protocol

import (
	"bytes"
	"testing"
)

func TestWriteReadAddress_IPv4(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "1.2.3.4:8080"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeIPv4 {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip == nil {
		t.Fatalf("ip should not be nil for ipv4")
	}
}

func TestWriteReadAddress_Domain(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "example.com:53"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeDomain {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip != nil {
		t.Fatalf("expected nil ip for domain, got %v", ip)
	}
}

func TestWriteReadAddress_IPv6(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "[2001:db8::1]:443"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeIPv6 {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip == nil {
		t.Fatalf("ip should not be nil for ipv6")
	}
}

func TestWriteAddress_DomainTooLong(t *testing.T) {
	longDomain := make([]byte, 256)
	for i := range longDomain {
		longDomain[i] = 'a'
	}
	err := WriteAddress(new(bytes.Buffer), string(longDomain)+":80")
	if err == nil {
		t.Fatalf("expected error for long domain")
	}
}

func TestReadAddress_UnknownType(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0x02, 0x00, 0x50}) // invalid type, port after
	if _, _, _, err := ReadAddress(buf); err == nil {
		t.Fatalf("expected error for unknown type")
	}
}
