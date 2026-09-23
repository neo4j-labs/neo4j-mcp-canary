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
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/oidc"
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

	// initDatabaseService/initMultiInstanceDatabaseService return errors
	// rather than calling os.Exit themselves so that main() is the only
	// place that decides to exit — see initDatabaseService's doc comment
	// for why that matters once a defer is in the picture.
	var dbService database.Service
	var cleanup func()
	if len(cfg.Instances) > 0 {
		dbService, cleanup, err = initMultiInstanceDatabaseService(ctx, cfg)
	} else {
		dbService, cleanup, err = initDatabaseService(ctx, cfg)
	}
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

	// In multi-instance mode, wire up Bearer-token verification for any
	// bearer-type instances. NewVerifierRegistry is a no-op (an empty
	// registry) when there are none, so this is always safe to call rather
	// than needing its own conditional.
	if len(cfg.Instances) > 0 {
		verifiers, err := oidc.NewVerifierRegistry(ctx, cfg.Instances)
		if err != nil {
			slog.Error("Failed to initialize Bearer token verification", "error", err)
			os.Exit(1)
		}
		mcpServer.SetBearerVerifier(verifiers.VerifyBearer)
	}

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

// initMultiInstanceDatabaseService builds one Neo4j Bolt driver per
// configured instance (Config.Instances) and wraps them in a
// database.InstanceRegistry, so the single dbService handed to every tool
// handler transparently dispatches to whichever instance a request
// selected (internal/auth.WithInstanceSelection, set by that instance's
// own HTTP route — see internal/server.buildMultiInstanceHandler). Only
// Bolt-scheme instance URIs are supported here; a Query API instance URI
// (http/https) is rejected with a clear startup error rather than silently
// misbehaving — Query API-backed multi-instance isn't implemented yet.
//
// Each instance's auth.Type determines its driver's own credentials and
// whether per-call context auth is used, mirroring the single-instance
// branching in initDatabaseService:
//   - basic: a static service-account credential is baked into the driver;
//     perRequestAuth is false since there is nothing per-call to look at —
//     the client authenticates to this server with an API key, not Neo4j
//     credentials (see internal/server.instanceAuthMiddleware).
//   - basic_passthrough / bearer: the driver gets no fixed credentials
//     (the zero-value neo4j.AuthToken, exactly like today's single-instance
//     HTTP mode below); perRequestAuth is true, since instanceAuthMiddleware
//     puts the client's forwarded Basic credentials or verified Bearer
//     token on the request context for every call.
//
// Like initDatabaseService, this returns errors instead of calling os.Exit
// itself, and the returned cleanup func must be deferred by the caller only
// once this has already returned successfully.
func initMultiInstanceDatabaseService(ctx context.Context, cfg *config.Config) (database.Service, func(), error) {
	services := make(map[string]database.Service, len(cfg.Instances))
	var drivers []neo4j.Driver

	closeDrivers := func() {
		for _, d := range drivers {
			if err := d.Close(ctx); err != nil {
				slog.Error("Error closing driver", "error", err)
			}
		}
	}

	for _, inst := range cfg.Instances {
		connMode, err := queryapi.DetectMode(inst.URI)
		if err != nil {
			closeDrivers()
			return nil, nil, fmt.Errorf("instance %q: failed to parse Neo4j URI: %w", inst.Name, err)
		}
		if connMode == queryapi.ModeQueryAPI {
			closeDrivers()
			return nil, nil, fmt.Errorf("instance %q: Query API URIs are not supported in multi-instance mode yet", inst.Name)
		}

		var authToken neo4j.AuthToken
		perRequestAuth := true
		if inst.Auth.Type == config.InstanceAuthBasic {
			authToken = neo4j.BasicAuth(inst.Auth.Username, inst.Auth.Password, "")
			perRequestAuth = false
		}

		driver, err := neo4j.NewDriver(inst.URI, authToken)
		if err != nil {
			closeDrivers()
			return nil, nil, fmt.Errorf("instance %q: failed to create neo4j driver: %w", inst.Name, err)
		}
		drivers = append(drivers, driver)

		dbService, err := database.NewNeo4jServiceWithAuthMode(driver, inst.Database, perRequestAuth, Version)
		if err != nil {
			closeDrivers()
			return nil, nil, fmt.Errorf("instance %q: failed to create database service: %w", inst.Name, err)
		}
		services[inst.Name] = dbService
	}

	return database.NewInstanceRegistry(services), closeDrivers, nil
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
