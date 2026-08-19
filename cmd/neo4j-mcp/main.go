// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/cli"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/logger"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"

	query "github.com/neo4j-contrib/query-go-sdk"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// go build -C cmd/neo4j-mcp -o ../../bin/ -ldflags "-X 'main.Version=9999'"
var Version = "development"

const MixPanelEndpoint = "https://api.mixpanel.com"
const MixPanelToken = "4bfb2414ab973c741b6f067bf06d5575" // #nosec G101 -- MixPanel tokens are safe to be public

func main() {
	// Handle CLI arguments (version, help, etc.)
	cli.HandleArgs(Version)

	// Parse CLI flags for configuration
	cliArgs := cli.ParseConfigFlags()

	// Load and validate configuration (env vars + CLI overrides)
	cfg, err := config.LoadConfig(&config.CLIOverrides{
		URI:                            cliArgs.URI,
		Username:                       cliArgs.Username,
		Password:                       cliArgs.Password,
		Database:                       cliArgs.Database,
		ReadOnly:                       cliArgs.ReadOnly,
		Telemetry:                      cliArgs.Telemetry,
		SchemaSampleSize:               cliArgs.SchemaSampleSize,
		CypherMaxRows:                  cliArgs.CypherMaxRows,
		CypherMaxBytes:                 cliArgs.CypherMaxBytes,
		CypherTimeout:                  cliArgs.CypherTimeout,
		CypherMaxEstimatedRows:         cliArgs.CypherMaxEstimatedRows,
		TransportMode:                  cliArgs.TransportMode,
		Port:                           cliArgs.HTTPPort,
		Host:                           cliArgs.HTTPHost,
		AllowedOrigins:                 cliArgs.HTTPAllowedOrigins,
		TLSEnabled:                     cliArgs.HTTPTLSEnabled,
		TLSCertFile:                    cliArgs.HTTPTLSCertFile,
		TLSKeyFile:                     cliArgs.HTTPTLSKeyFile,
		AuthHeaderName:                 cliArgs.AuthHeaderName,
		AllowUnauthenticatedPing:       cliArgs.HTTPAllowUnauthenticatedPing,
		AllowUnauthenticatedToolsList:  cliArgs.HTTPAllowUnauthenticatedToolsList,
		AllowUnauthenticatedInitialize: cliArgs.HTTPAllowUnauthenticatedInitialize,
		AllowUnauthenticatedNotificationsInitialize: cliArgs.HTTPAllowUnauthenticatedNotificationsInitialize,
	})
	if err != nil {
		// Can't use logger here yet, so just print to stderr
		fmt.Fprintln(os.Stderr, "Failed to load configuration: "+err.Error())
		os.Exit(1)
	}

	// Initialize global logger
	logger.Init(cfg.LogLevel, cfg.LogFormat, os.Stderr)

	ctx := context.Background()

	// initDatabaseService returns errors rather than calling os.Exit itself so
	// that main() is the only place that decides to exit — see its doc
	// comment for why that matters once a defer is in the picture.
	dbService, cleanup, err := initDatabaseService(ctx, cfg)
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

// initDatabaseService builds the database.Service to use for this run,
// auto-detecting which wire protocol to speak from the scheme of cfg.URI:
// http/https means the Query API (see newService), anything else
// (bolt, neo4j, and their +s/+ssc variants) means the existing Bolt driver.
//
// This deliberately returns errors instead of calling os.Exit itself, even
// though the only caller (main) always exits on a non-nil error: os.Exit
// skips deferred calls, so if this function registered its own defer for
// driver/client cleanup and then exited directly on a later failure, that
// cleanup would be silently skipped. Returning a cleanup func and letting
// main defer it — only after this function has already returned
// successfully — keeps main() the single place that decides to exit, with
// no defer registered before that decision is made.
func initDatabaseService(ctx context.Context, cfg *config.Config) (database.Service, func(), error) {
	connMode, err := queryapi.DetectMode(cfg.URI)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse Neo4j URI: %w", err)
	}

	if connMode == queryapi.ModeQueryAPI {
		return newService(ctx, cfg)
	}

	// Bolt path.
	// For STDIO mode: use environment credentials
	// For HTTP mode: create driver without auth, per-request credentials will be used via impersonation
	// Credentials come from per-request Basic Auth headers
	var authToken neo4j.AuthToken
	if cfg.TransportMode == config.TransportModeStdio {
		authToken = neo4j.BasicAuth(cfg.Username, cfg.Password, "")
	}

	driver, err := neo4j.NewDriver(cfg.URI, authToken)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create neo4j driver: %w", err)
	}

	dbService, err := database.NewNeo4jService(driver, cfg.Database, cfg.TransportMode, Version)
	if err != nil {
		if closeErr := driver.Close(ctx); closeErr != nil {
			slog.Error("Error closing driver after service creation failure", "error", closeErr)
		}
		return nil, nil, fmt.Errorf("failed to create database service: %w", err)
	}

	cleanup := func() {
		if err := driver.Close(ctx); err != nil {
			slog.Error("Error closing driver", "error", err)
		}
	}
	return dbService, cleanup, nil
}

// newService builds a database.Service backed by the Neo4j Query
// API instead of the Bolt driver. It verifies the connected server meets
// the minimum version this package supports before constructing anything
// else — see queryapi.EnsureMinimumVersion.
//
// The two MCP transport modes need different credential lifecycles, exactly
// as the Bolt path above does:
//   - STDIO: one long-lived *query.QueryAPIClient built from
//     NEO4J_USERNAME/NEO4J_PASSWORD, matching the STDIO Bolt driver's single
//     fixed authToken.
//   - HTTP: no fixed credentials — each MCP request supplies its own via
//     Basic or Bearer auth, so Service builds a fresh, lightweight
//     client per call (see queryapi.NewPerRequestClientFactory). All of them
//     share the *http.Client returned here so the underlying connections are
//     still pooled.
//
// The returned cleanup func must be deferred by the caller; it is not safe
// to call from within this function since that would run before the server
// ever starts serving.
func newService(ctx context.Context, cfg *config.Config) (database.Service, func(), error) {
	httpClient := &http.Client{}

	if err := queryapi.EnsureMinimumVersion(ctx, httpClient, cfg.URI); err != nil {
		return nil, nil, err
	}

	if cfg.TransportMode == config.TransportModeStdio {
		client, err := query.NewClient(
			query.WithBasicAuth(cfg.Username, cfg.Password),
			query.WithBaseURL(cfg.URI),
			query.WithDatabase(cfg.Database),
			query.WithStreamingSupport(true),
			query.WithHTTPClient(httpClient),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create query api client: %w", err)
		}

		dbService, err := queryapi.NewService(queryapi.NewStaticClientFactory(client))
		if err != nil {
			return nil, nil, err
		}
		return dbService, client.Close, nil
	}

	dbService, err := queryapi.NewService(queryapi.NewPerRequestClientFactory(cfg.URI, cfg.Database, httpClient))
	if err != nil {
		return nil, nil, err
	}
	return dbService, httpClient.CloseIdleConnections, nil
}
