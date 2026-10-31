/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
// Go 1.24+ enables MPTCP for TCP listeners by default on Linux, and an MPTCP
// listener is the precondition of the published CVE-2026-98432 exploit chain.
// This program does not use MPTCP; keep listeners on plain TCP. The complete
// fix is a patched host kernel -- this only removes the MPTCP attack path.
//go:debug multipathtcp=0
package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/SUDOKU-ASCII/sudoku/internal/app"
	"github.com/SUDOKU-ASCII/sudoku/internal/cli"
	"github.com/SUDOKU-ASCII/sudoku/internal/cliutil"
	"github.com/SUDOKU-ASCII/sudoku/internal/config"
	"github.com/SUDOKU-ASCII/sudoku/internal/reverse"
	"github.com/SUDOKU-ASCII/sudoku/pkg/crypto"
	"github.com/SUDOKU-ASCII/sudoku/pkg/logx"
)

var (
	configPaths cliutil.MultiValue
	testConfig  = flag.Bool("test", false, "Test configuration file and exit")
	keygen      = flag.Bool("keygen", false, "Generate a new Ed25519 key pair")
	more        = flag.String("more", "", "Generate more Private key (hex) for split key generations")
	linkInputs  cliutil.MultiValue
	exportLink  = flag.Bool("export-link", false, "Print sudoku:// short link generated from the config")
	publicHost  = flag.String("public-host", "", "Advertised server host for short link generation (server mode); supports host or host:port")
	setupWizard = flag.Bool("tui", false, "Launch interactive TUI to create config before starting")

	revDial     = flag.String("rev-dial", "", "Dial a reverse TCP-over-WebSocket endpoint (ws:// or wss://) and forward from rev-listen")
	revListen   = flag.String("rev-listen", "", "Local TCP listen address for reverse forwarder (e.g., 127.0.0.1:2222)")
	revInsecure = flag.Bool("rev-insecure", false, "Skip TLS verification for wss reverse dial (testing only)")
)

func init() {
	flag.Var(&configPaths, "c", "Path to configuration file (repeat or comma-separate for multiple client configs)")
	flag.Var(&linkInputs, "link", "Start client directly from sudoku:// short link(s) (repeat or comma-separate)")
}

func main() {
	flag.Parse()
	logx.InstallStd()

	if *revDial != "" || *revListen != "" {
		if *revDial == "" || *revListen == "" {
			logx.Fatalf("CLI", "reverse forwarder requires both -rev-dial and -rev-listen")
		}
		if err := reverse.ServeLocalWSForward(*revListen, *revDial, *revInsecure); err != nil {
			logx.Fatalf("CLI", "%v", err)
		}
		return
	}

	if *keygen {
		if *more != "" {
			x, err := crypto.ParsePrivateScalar(*more)
			if err != nil {
				logx.Fatalf("CLI", "Invalid private key: %v", err)
			}

			// 2. Generate new split key
			splitKey, err := crypto.SplitPrivateKey(x)
			if err != nil {
				logx.Fatalf("CLI", "Failed to split key: %v", err)
			}
			logx.Infof("CLI", "Split Private Key: %s", splitKey)
			return
		}

		// Generate new Master Key
		pair, err := crypto.GenerateMasterKey()
		if err != nil {
			logx.Fatalf("CLI", "Failed to generate key: %v", err)
		}
		splitKey, err := crypto.SplitPrivateKey(pair.Private)
		if err != nil {
			logx.Fatalf("CLI", "Failed to generate key: %v", err)
		}
		logx.Infof("CLI", "Available Private Key: %s", splitKey)
		logx.Infof("CLI", "Master Private Key: %s", crypto.EncodeScalar(pair.Private))
		logx.Infof("CLI", "Master Public Key:  %s", crypto.EncodePoint(pair.Public))
		return
	}

	if configPaths.IsSet() && linkInputs.IsSet() {
		logx.Fatalf("CLI", "Use either -c or -link, not both")
	}

	if links := linkInputs.Values(); len(links) > 0 {
		configs, tableSets, err := cli.BuildClientConfigsFromLinks(links)
		if err != nil {
			logx.Fatalf("CLI", "Failed to build client configs from links: %v", err)
		}
		app.RunClientPool(configs, tableSets)
		return
	}

	if *setupWizard {
		result, err := cli.RunSetupWizard(configPaths.First("server.config.json"), *publicHost)
		if errors.Is(err, cli.ErrSetupCancelled) {
			logx.Infof("CLI", "Setup cancelled, nothing was written")
			os.Exit(1)
		}
		if err != nil {
			logx.Fatalf("CLI", "Setup failed: %v", err)
		}

		tables, err := app.BuildTables(result.ServerConfig)
		if err != nil {
			logx.Fatalf("CLI", "Failed to build table: %v", err)
		}
		app.RunServer(result.ServerConfig, tables)
		return
	}

	paths := configPaths.Values("server.config.json")
	configs, err := cli.LoadConfigs(paths)
	if err != nil {
		logx.Fatalf("CLI", "Failed to load config(s): %v", err)
	}
	cfg := configs[0]

	if *testConfig {
		for i, loaded := range configs {
			logx.Infof("CLI", "Configuration %s is valid.", paths[i])
			logx.Infof("CLI", "Mode: %s", loaded.Mode)
			if loaded.Mode == "client" {
				logx.Infof("CLI", "Rules: %d URLs configured", len(loaded.RuleURLs))
			}
		}
		os.Exit(0)
	}

	if *exportLink {
		for i, loaded := range configs {
			link, err := config.BuildShortLinkFromConfig(loaded, *publicHost)
			if err != nil {
				logx.Fatalf("CLI", "Export short link failed for %s: %v", paths[i], err)
			}
			logx.Infof("CLI", "Short link (%s): %s", paths[i], link)
		}
		os.Exit(0)
	}

	if cfg.Mode == "client" {
		tableSets, err := cli.BuildClientTableSets(configs)
		if err != nil {
			logx.Fatalf("CLI", "Failed to build client table sets: %v", err)
		}
		app.RunClientPool(configs, tableSets)
		return
	}

	if len(configs) > 1 {
		logx.Fatalf("CLI", "Multiple -c values are supported only in client mode")
	}

	tables, err := app.BuildTables(cfg)
	if err != nil {
		logx.Fatalf("CLI", "Failed to build table: %v", err)
	}
	cfg.UsageFile = strings.TrimSuffix(paths[0], filepath.Ext(paths[0])) + ".usage.json"
	app.RunServer(cfg, tables)
}
