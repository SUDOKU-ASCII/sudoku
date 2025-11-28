package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saba-futai/sudoku/pkg/crypto"
)

func TestInitMieruconfigDefaults(t *testing.T) {
	pair, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey error: %v", err)
	}

	cfg := &Config{
		EnableMieru: true,
		LocalPort:   7000,
		Key:         crypto.EncodeScalar(pair.Private),
	}

	InitMieruconfig(cfg)

	if cfg.MieruConfig == nil {
		t.Fatalf("MieruConfig not initialized")
	}
	if cfg.MieruConfig.Port == 0 {
		t.Fatalf("Port not set")
	}
	if cfg.MieruConfig.Transport == "" {
		t.Fatalf("Transport not set")
	}
	if cfg.MieruConfig.Username == "" || cfg.MieruConfig.Password == "" {
		t.Fatalf("Identity not derived")
	}
}

func TestLoadDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.json")

	data := `{
		"mode": "client",
		"local_port": 8080,
		"server_address": "1.1.1.1:443",
		"key": "k",
		"aead": "none",
		"rule_urls": ["global"]
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Transport != "tcp" {
		t.Fatalf("Transport default not applied")
	}
	if cfg.ASCII != "prefer_entropy" {
		t.Fatalf("ASCII default not applied, got %s", cfg.ASCII)
	}
	if cfg.ProxyMode != "global" || cfg.RuleURLs != nil {
		t.Fatalf("ProxyMode parsing failed, mode=%s urls=%v", cfg.ProxyMode, cfg.RuleURLs)
	}
}
