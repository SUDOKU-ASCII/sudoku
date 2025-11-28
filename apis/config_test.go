package apis

import (
	"testing"

	"github.com/saba-futai/sudoku/pkg/obfs/sudoku"
)

func TestValidateClient(t *testing.T) {
	cfg := &ProtocolConfig{
		Table:         sudoku.NewTable("seed", "prefer_ascii"),
		Key:           "k",
		AEADMethod:    "chacha20-poly1305",
		PaddingMin:    5,
		PaddingMax:    10,
		ServerAddress: "1.1.1.1:443",
		TargetAddress: "example.com:80",
	}
	if err := cfg.ValidateClient(); err != nil {
		t.Fatalf("ValidateClient unexpected error: %v", err)
	}

	cfg.PaddingMax = 1
	cfg.PaddingMin = 2
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected padding validation error")
	}

	cfg.PaddingMin = 0
	cfg.PaddingMax = 0
	cfg.AEADMethod = "bad"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected invalid AEAD error")
	}

	cfg.AEADMethod = "none"
	cfg.Table = nil
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected table nil error")
	}

	cfg.Table = sudoku.NewTable("seed", "prefer_ascii")
	cfg.ServerAddress = ""
	if err := cfg.ValidateClient(); err == nil {
		t.Fatalf("expected server address error")
	}
	cfg.ServerAddress = "1.1.1.1:443"
	cfg.TargetAddress = ""
	if err := cfg.ValidateClient(); err == nil {
		t.Fatalf("expected target address error")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AEADMethod == "" || cfg.PaddingMin == 0 || cfg.PaddingMax == 0 {
		t.Fatalf("defaults not set")
	}
}
