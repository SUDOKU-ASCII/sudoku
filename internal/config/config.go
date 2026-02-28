package config

type Config struct {
	Mode               string       `json:"mode"`      // "client" or "server"
	Transport          string       `json:"transport"` // "tcp" or "udp"
	LocalPort          int          `json:"local_port"`
	ServerAddress      string       `json:"server_address"`
	FallbackAddr       string       `json:"fallback_address"`
	Key                string       `json:"key"`
	AEAD               string       `json:"aead"`              // "aes-128-gcm", "chacha20-poly1305", "none"
	SuspiciousAction   string       `json:"suspicious_action"` // "fallback" or "silent"
	PaddingMin         int          `json:"padding_min"`
	PaddingMax         int          `json:"padding_max"`
	RuleURLs           []string     `json:"rule_urls"`            // Empty = use defaults; supports "global", "direct" keywords
	ProxyMode          string       `json:"proxy_mode"`           // Runtime state, populated by Load logic
	ASCII              string       `json:"ascii"`                // "prefer_entropy" (default): low entropy; "prefer_ascii": pure ASCII, high entropy
	CustomTable        string       `json:"custom_table"`         // Optional: defines X/P/V layout, e.g. "xpxvvpvv"
	CustomTables       []string     `json:"custom_tables"`        // Optional: rotate among multiple X/P/V layouts
	EnablePureDownlink bool         `json:"enable_pure_downlink"` // Enable pure Sudoku downlink; false uses bandwidth-optimized packed encoding

	HTTPMask HTTPMaskConfig `json:"httpmask"`

	Reverse *ReverseConfig `json:"reverse,omitempty"`
}
