package config

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestShortLinkRoundTrip_Client(t *testing.T) {
	cfg := &Config{
		Mode:          "client",
		LocalPort:     1081,
		ServerAddress: "8.8.8.8:443",
		Key:           "deadbeef",
		AEAD:          "aes-128-gcm",
		ASCII:         "prefer_ascii",
		EnableMieru:   true,
		MieruConfig: &MieruConfig{
			Port: 9000,
		},
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}
	if link == "" {
		t.Fatalf("empty link")
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}

	if decoded.ServerAddress != cfg.ServerAddress {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
	if decoded.LocalPort != cfg.LocalPort {
		t.Fatalf("local port mismatch, got %d", decoded.LocalPort)
	}
	if decoded.Key != cfg.Key {
		t.Fatalf("key mismatch, got %s", decoded.Key)
	}
	if decoded.AEAD != cfg.AEAD {
		t.Fatalf("aead mismatch, got %s", decoded.AEAD)
	}
	if !decoded.EnableMieru || decoded.MieruConfig == nil || decoded.MieruConfig.Port != cfg.MieruConfig.Port {
		t.Fatalf("mieru config mismatch")
	}
	if decoded.ASCII != "prefer_ascii" {
		t.Fatalf("ascii mismatch, got %s", decoded.ASCII)
	}
}

func TestShortLinkAdvertiseServer(t *testing.T) {
	cfg := &Config{
		Mode:         "server",
		LocalPort:    9443,
		Key:          "deadbeef",
		ASCII:        "",
		AEAD:         "",
		EnableMieru:  false,
		MieruConfig:  nil,
		FallbackAddr: "127.0.0.1:80",
	}

	link, err := BuildShortLinkFromConfig(cfg, "example.com")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}
	if link == "" {
		t.Fatalf("empty link")
	}
}

func TestShortLinkInvalidScheme(t *testing.T) {
	if _, err := BuildConfigFromShortLink("http://bad"); err == nil {
		t.Fatalf("expected error for bad scheme")
	}
}

func TestShortLinkMissingFields(t *testing.T) {
	payload := map[string]string{}
	raw, _ := json.Marshal(payload)
	link := "sudoku://" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := BuildConfigFromShortLink(link); err == nil {
		t.Fatalf("expected error for missing fields")
	}
}
