package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Save writes the config to disk with indentation.
func Save(path string, cfg *Config) error {
	if cfg == nil {
		return nil
	}

	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}
