// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/cli"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/logger"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"
)

// go build -C cmd/neo4j-mcp -o ../../bin/ -ldflags "-X 'main.Version=9999'"
var Version = "development"

const MixPanelEndpoint = "https://api.mixpanel.com"
const MixPanelToken = "4bfb2414ab973c741b6f067bf06d5575" // #nosec G101 -- MixPanel tokens are safe to be public

func main() {
	// Handle CLI arguments (version, help, etc.)
	cli.HandleArgs(Version)

	// Parse CLI flags for configuration
	overrides := cli.ParseConfigFlags()

	// Load and validate configuration (CLI flags + env vars + optional config file)
	cfg, err := config.LoadConfig(overrides)
	if err != nil {
		// Can't use logger here yet, so just print to stderr
		fmt.Fprintln(os.Stderr, "Failed to load configuration: "+err.Error())
		os.Exit(1)
	}

	// Initialize global logger
	logger.Init(cfg.LogLevel, cfg.LogFormat, os.Stderr)

	ctx := context.Background()

	// server.BuildDatabaseService returns errors rather than calling os.Exit
	// itself so that main() is the only place that decides to exit — see its
	// doc comment for why that matters once a defer is in the picture. It's
	// also reused by Neo4jMCPServer.Apply to rebuild the connection when an
	// admin change targets a new URI/database at runtime.
	dbService, cleanup, err := server.BuildDatabaseService(ctx, cfg, Version)
	if err != nil {
		slog.Error("Failed to initialize connection to Neo4j", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	anService := analytics.NewAnalytics(MixPanelToken, MixPanelEndpoint, cfg.URI)

	// Enable telemetry only when user has opted in AND Version is different from "development", which is changed via ldflags at build time.
	if cfg.Telemetry && Version != "development" {
		anService.Enable()
		log.Println("Telemetry is enabled to help us improve the product by collecting anonymous usage data such as: tools being used, the operating system, and CPU architecture.")
		log.Println("To disable telemetry, set the NEO4J_TELEMETRY environment variable to \"false\".")
	} else {
		log.Println("Telemetry disabled.")
		anService.Disable()
	}

	// Create and configure the MCP server
	mcpServer := server.NewNeo4jMCPServer(Version, cfg, dbService, anService)

	// Start the server - this blocks until shutdown for both stdio and HTTP modes
	if err := mcpServer.Start(); err != nil {
		slog.Error("Server error", "error", err)
		return
	}
}
