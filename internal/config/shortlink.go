package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// shortLinkPayload holds the minimal fields we expose in sudoku:// links.
type shortLinkPayload struct {
	Host      string `json:"h"`            // server host / IP
	Port      int    `json:"p"`            // server port
	Key       string `json:"k"`            // shared key
	ASCII     string `json:"a,omitempty"`  // "ascii" or "entropy"
	AEAD      string `json:"e,omitempty"`  // AEAD method
	MixPort   int    `json:"m,omitempty"`  // local mixed proxy port
	MieruPort int    `json:"mp,omitempty"` // optional Mieru port (TCP)
}

// BuildShortLinkFromConfig builds a sudoku:// short link from the provided config.
// If cfg.ServerAddress is empty (server-side config), advertiseHost must be provided.
func BuildShortLinkFromConfig(cfg *Config, advertiseHost string) (string, error) {
	if cfg == nil {
		return "", errors.New("nil config")
	}

	host, port, err := deriveAdvertiseAddress(cfg, advertiseHost)
	if err != nil {
		return "", err
	}

	payload := shortLinkPayload{
		Host: host,
		Port: port,
		Key:  cfg.Key,
		AEAD: cfg.AEAD,
	}

	if cfg.Mode == "client" && cfg.LocalPort > 0 {
		payload.MixPort = cfg.LocalPort
	}
	if payload.MixPort == 0 {
		payload.MixPort = 1080 // reasonable default for mixed proxy
	}

	if cfg.EnableMieru && cfg.MieruConfig != nil {
		payload.MieruPort = cfg.MieruConfig.Port
	}

	payload.ASCII = encodeASCII(cfg.ASCII)
	if payload.AEAD == "" {
		payload.AEAD = "chacha20-poly1305"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return "sudoku://" + base64.RawURLEncoding.EncodeToString(data), nil
}

// BuildConfigFromShortLink parses a sudoku:// short link and returns a client config.
// The generated config is ready to run a PAC proxy.
func BuildConfigFromShortLink(link string) (*Config, error) {
	if !strings.HasPrefix(link, "sudoku://") {
		return nil, errors.New("invalid scheme")
	}

	encoded := strings.TrimPrefix(link, "sudoku://")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode short link failed: %w", err)
	}

	var payload shortLinkPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid short link payload: %w", err)
	}

	if payload.Host == "" || payload.Port == 0 || payload.Key == "" {
		return nil, errors.New("short link missing required fields")
	}

	cfg := &Config{
		Mode:          "client",
		Transport:     "tcp",
		LocalPort:     payload.MixPort,
		ServerAddress: fmt.Sprintf("%s:%d", payload.Host, payload.Port),
		Key:           payload.Key,
		AEAD:          payload.AEAD,
		PaddingMin:    5,
		PaddingMax:    15,
		ProxyMode:     "pac",
		RuleURLs: []string{
			"https://gh-proxy.org/https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/China/China.list",
			"https://gh-proxy.org/https://raw.githubusercontent.com/fernvenue/chn-cidr-list/master/ipv4.yaml",
		},
	}

	if cfg.LocalPort == 0 {
		cfg.LocalPort = 1080
	}

	if payload.MieruPort > 0 {
		cfg.EnableMieru = true
		cfg.MieruConfig = &MieruConfig{
			Port:      payload.MieruPort,
			Transport: "TCP",
		}
		if payload.MieruPort == cfg.LocalPort {
			cfg.MieruConfig.Transport = "UDP"
		}
	}

	cfg.ASCII = decodeASCII(payload.ASCII)
	if cfg.AEAD == "" {
		cfg.AEAD = "none"
	}

	InitMieruconfig(cfg)
	return cfg, nil
}

func encodeASCII(mode string) string {
	if strings.ToLower(mode) == "prefer_ascii" || mode == "ascii" {
		return "ascii"
	}
	return "entropy"
}

func decodeASCII(val string) string {
	switch strings.ToLower(val) {
	case "ascii", "prefer_ascii":
		return "prefer_ascii"
	default:
		return "prefer_entropy"
	}
}

func deriveAdvertiseAddress(cfg *Config, advertiseHost string) (string, int, error) {
	if cfg.ServerAddress != "" {
		host, portStr, err := net.SplitHostPort(cfg.ServerAddress)
		if err != nil {
			return "", 0, fmt.Errorf("invalid server_address: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port in server_address: %w", err)
		}
		return host, port, nil
	}

	if advertiseHost != "" && cfg.LocalPort > 0 {
		return advertiseHost, cfg.LocalPort, nil
	}

	return "", 0, errors.New("cannot derive server address; set server_address or provide advertise host")
}
