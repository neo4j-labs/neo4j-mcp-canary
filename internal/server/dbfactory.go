// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"

	query "github.com/neo4j-contrib/query-go-sdk"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// BuildDatabaseService builds the database.Service to use for cfg,
// auto-detecting which wire protocol to speak from the scheme of cfg.URI:
// http/https means the Query API (see buildQueryAPIService), anything else
// (bolt, neo4j, and their +s/+ssc variants) means the Bolt driver.
//
// This is the single place that knows how to turn a *config.Config into a
// running database.Service — used both at process startup (cmd/neo4j-mcp/main.go)
// and by Neo4jMCPServer.Apply when an admin change rebuilds the connection
// against a new URI/database at runtime (see apply.go). It deliberately
// returns errors instead of exiting/panicking, since a Tier 3 Apply failure
// must leave the server's existing connection intact, not bring the process
// down. The returned cleanup func must be called exactly once, after the
// caller is done with the returned Service (main.go defers it for the life
// of the process; Apply calls it once the old service has been swapped out
// and any in-flight requests against it have had a chance to finish).
func BuildDatabaseService(ctx context.Context, cfg *config.Config, version string) (database.Service, func(), error) {
	connMode, err := queryapi.DetectMode(cfg.URI)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse Neo4j URI: %w", err)
	}

	if connMode == queryapi.ModeQueryAPI {
		return buildQueryAPIService(ctx, cfg)
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

	dbService, err := database.NewNeo4jService(driver, cfg.Database, cfg.TransportMode, version)
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

// buildQueryAPIService builds a database.Service backed by the Neo4j Query
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
func buildQueryAPIService(ctx context.Context, cfg *config.Config) (database.Service, func(), error) {
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
