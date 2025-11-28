// internal/config/config.go
package config

type Config struct {
	Mode             string       `json:"mode"`      // "client" or "server"
	Transport        string       `json:"transport"` // "tcp" or "udp"
	LocalPort        int          `json:"local_port"`
	ServerAddress    string       `json:"server_address"`
	FallbackAddr     string       `json:"fallback_address"`
	Key              string       `json:"key"`
	AEAD             string       `json:"aead"`              // "aes-128-gcm", "chacha20-poly1305", "none"
	SuspiciousAction string       `json:"suspicious_action"` // "fallback" or "silent"
	PaddingMin       int          `json:"padding_min"`
	PaddingMax       int          `json:"padding_max"`
	RuleURLs         []string     `json:"rule_urls"`    // 留空则使用默认，支持 "global", "direct" 关键字
	ProxyMode        string       `json:"proxy_mode"`   // 运行时状态，非JSON字段，由Load解析逻辑填充
	ASCII            string       `json:"ascii"`        // "prefer_entropy" (默认): 低熵, "prefer_ascii": 纯ASCII字符，高熵
	EnableMieru      bool         `json:"enable_mieru"` // 开启上下行分离
	MieruConfig      *MieruConfig `json:"mieru_config"` // Mieru 特定配置
}

type MieruConfig struct {
	Port          int    `json:"port"`      // 服务端 Mieru 监听端口 (区别于 Sudoku 端口)
	Transport     string `json:"transport"` // "TCP" or "UDP" (Mieru 底层)
	MTU           int    `json:"mtu"`
	Multiplexing  string `json:"multiplexing"` // "LOW", "MIDDLE", "HIGH"
	Username      string `json:"username"`     // 默认使用 "default"
	Password      string `json:"password"`     // 留空则复用 Sudoku Key
	ApplySettings bool   `json:"-"`            // 内部标记
}
