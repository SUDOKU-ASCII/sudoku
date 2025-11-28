package app

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/saba-futai/sudoku/internal/config"
	"github.com/saba-futai/sudoku/pkg/crypto"
)

// WizardResult aggregates outputs from the interactive setup.
type WizardResult struct {
	ServerConfig     *config.Config
	ClientConfig     *config.Config
	ServerConfigPath string
	ClientConfigPath string
	ShortLink        string
}

// RunSetupWizard builds server/client configs interactively and exports a short link.
func RunSetupWizard(defaultServerPath, publicHost string) (*WizardResult, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("== Sudoku Server Setup ==")
	host := promptString(reader, "Server public host/IP", publicHost, "127.0.0.1")
	serverPort := promptInt(reader, "Server port", 8080)
	mixPort := promptInt(reader, "Client mixed proxy port", 1080)
	fallback := promptString(reader, "Fallback address for suspicious traffic", "", "127.0.0.1:80")
	aead := promptString(reader, "AEAD (chacha20-poly1305 / aes-128-gcm / none)", "", "chacha20-poly1305")
	asciiMode := resolveASCII(promptString(reader, "Encoding (ascii / entropy)", "", "entropy"))
	suspiciousAction := promptString(reader, "Suspicious action (fallback / silent)", "", "fallback")
	paddingMin := promptInt(reader, "Padding min (%)", 5)
	paddingMax := promptInt(reader, "Padding max (%)", 15)
	if paddingMax < paddingMin {
		fmt.Printf("Padding max is smaller than min, using %d for both\n", paddingMin)
		paddingMax = paddingMin
	}
	mieruPort := promptInt(reader, "Mieru port (0 to disable)", 0)

	keyInput := promptString(reader, "Shared key (leave empty to auto-generate)", "", "")
	key := strings.TrimSpace(keyInput)
	if key == "" {
		// Use public key as the shared secret to avoid accidental private key exposure.
		pair, err := crypto.GenerateMasterKey()
		if err != nil {
			return nil, fmt.Errorf("generate key failed: %w", err)
		}
		key = crypto.EncodePoint(pair.Public)
		fmt.Printf("Generated shared key: %s\n", key)
	}

	serverCfg := &config.Config{
		Mode:             "server",
		Transport:        "tcp",
		LocalPort:        serverPort,
		FallbackAddr:     fallback,
		Key:              key,
		AEAD:             aead,
		SuspiciousAction: suspiciousAction,
		PaddingMin:       paddingMin,
		PaddingMax:       paddingMax,
		ASCII:            asciiMode,
		EnableMieru:      mieruPort > 0,
	}

	clientCfg := &config.Config{
		Mode:          "client",
		Transport:     "tcp",
		LocalPort:     mixPort,
		ServerAddress: fmt.Sprintf("%s:%d", host, serverPort),
		Key:           key,
		AEAD:          aead,
		PaddingMin:    paddingMin,
		PaddingMax:    paddingMax,
		ASCII:         asciiMode,
		ProxyMode:     "pac",
		RuleURLs:      nil,
		EnableMieru:   mieruPort > 0,
	}

	if mieruPort > 0 {
		serverCfg.MieruConfig = &config.MieruConfig{
			Port:      mieruPort,
			Transport: "TCP",
		}
		clientCfg.MieruConfig = &config.MieruConfig{
			Port:      mieruPort,
			Transport: "TCP",
		}
	}

	config.InitMieruconfig(serverCfg)
	config.InitMieruconfig(clientCfg)

	serverPath := promptString(reader, "Server config output path", defaultServerPath, defaultServerPath)
	if serverPath == "" {
		serverPath = "config.server.json"
	}
	clientPath := promptString(reader, "Client config output path", "client.config.json", "client.config.json")
	if clientPath == "" {
		clientPath = "client.config.json"
	}

	if err := config.Save(serverPath, serverCfg); err != nil {
		return nil, fmt.Errorf("save server config: %w", err)
	}
	if err := config.Save(clientPath, clientCfg); err != nil {
		return nil, fmt.Errorf("save client config: %w", err)
	}

	shortLink, err := config.BuildShortLinkFromConfig(clientCfg, "")
	if err != nil {
		return nil, fmt.Errorf("build short link: %w", err)
	}

	return &WizardResult{
		ServerConfig:     serverCfg,
		ClientConfig:     clientCfg,
		ServerConfigPath: serverPath,
		ClientConfigPath: clientPath,
		ShortLink:        shortLink,
	}, nil
}

func promptString(r *bufio.Reader, label, current, fallback string) string {
	displayDefault := current
	if displayDefault == "" {
		displayDefault = fallback
	}
	fmt.Printf("%s [%s]: ", label, displayDefault)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return displayDefault
	}
	return line
}

func promptInt(r *bufio.Reader, label string, def int) int {
	fmt.Printf("%s [%d]: ", label, def)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	val, err := strconv.Atoi(line)
	if err != nil {
		fmt.Printf("Invalid number, using %d\n", def)
		return def
	}
	return val
}

func resolveASCII(val string) string {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "ascii", "prefer_ascii":
		return "prefer_ascii"
	default:
		return "prefer_entropy"
	}
}
