package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Gabriel100201/tablero/internal/config"
	mcppkg "github.com/Gabriel100201/tablero/internal/mcp"
	"github.com/Gabriel100201/tablero/internal/provider"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var version = "dev"

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
		fmt.Printf("tablero %s\n", version)
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
	srv := mcppkg.NewServer(reg, version)

	if err := mcpserver.ServeStdio(srv); err != nil {
		log.Fatalf("MCP server error: %v", err)
	}
}

func printUsage() {
	fmt.Println(`tablero — Unified task aggregator for Linear, Taiga & OpenProject (MCP server)

USAGE
  tablero mcp                 Start the MCP server (stdio transport)
  tablero config <subcommand> Manage providers (add / list / remove / test / path / init)
  tablero version             Print version
  tablero help                Show this help

QUICK START
  tablero config add linear      Interactive prompt to add a Linear workspace
  tablero config add taiga       Interactive prompt to add a Taiga instance
  tablero config add openproject Interactive prompt to add an OpenProject instance
  tablero config test         Verify every configured provider responds
  tablero mcp                 Launch the MCP server for your AI agent

CONFIG
  Path: $TABLERO_CONFIG or ~/.tablero/config.yaml
  The file stores API keys / passwords, so it is written with mode 0600.`)
}
