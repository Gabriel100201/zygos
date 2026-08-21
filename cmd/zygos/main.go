package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strings"

	"github.com/Gabriel100201/zygos/internal/config"
	mcppkg "github.com/Gabriel100201/zygos/internal/mcp"
	"github.com/Gabriel100201/zygos/internal/provider"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// version is stamped by GoReleaser via -X main.version at release time. It must
// keep a constant initializer: -X cannot write to a variable whose initializer
// is a function call.
var version = "dev"

// resolvedVersion is the version the binary reports. Builds produced by
// `go install <module>@<version>` get no ldflags, so it falls back to the module
// version Go embeds in the binary — otherwise the documented install path yields
// a binary that calls itself "dev" and reports that over the MCP handshake.
func resolvedVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	// "(devel)" is what a build from a working tree reports; it says less than "dev".
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	return version
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "mcp":
		cmdMCP()
	case "config":
		cmdConfig(os.Args[2:])
	case "version":
		fmt.Printf("zygos %s\n", resolvedVersion())
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdMCP() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var providers []provider.Provider
	for _, pc := range cfg.Providers {
		switch pc.Type {
		case "linear":
			providers = append(providers, provider.NewLinear(pc.Name, pc.APIKey))
		case "taiga":
			providers = append(providers, provider.NewTaiga(pc.Name, pc.URL, pc.Username, pc.Password))
		case "openproject":
			providers = append(providers, provider.NewOpenProject(pc.Name, pc.URL, pc.APIKey))
		}
	}

	reg := provider.NewRegistry(providers)
	srv := mcppkg.NewServer(reg, resolvedVersion())

	if err := mcpserver.ServeStdio(srv); err != nil {
		log.Fatalf("MCP server error: %v", err)
	}
}

func printUsage() {
	fmt.Println(`zygos — Unified task aggregator for Linear, Taiga & OpenProject (MCP server)

USAGE
  zygos mcp                 Start the MCP server (stdio transport)
  zygos config <subcommand> Manage providers (add / list / remove / test / path / init)
  zygos version             Print version
  zygos help                Show this help

QUICK START
  zygos config add linear      Interactive prompt to add a Linear workspace
  zygos config add taiga       Interactive prompt to add a Taiga instance
  zygos config add openproject Interactive prompt to add an OpenProject instance
  zygos config test         Verify every configured provider responds
  zygos mcp                 Launch the MCP server for your AI agent

CONFIG
  Path: $ZYGOS_CONFIG or ~/.zygos/config.yaml
  The file stores API keys / passwords, so it is written with mode 0600.`)
}
