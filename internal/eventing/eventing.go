// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package eventing

import (
	"context"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Emitter turns server lifecycle and tool-call activity into analytics
// events. It has no MCP-protocol or HTTP-transport knowledge of its own —
// callers feed it the request/result values from those layers.
type Emitter struct {
	an      analytics.Service
	db      database.Service
	cfg     *config.Config
	version string
}

// NewEmitter creates an Emitter backed by the given services and config.
func NewEmitter(an analytics.Service, db database.Service, cfg *config.Config, version string) *Emitter {
	return &Emitter{an: an, db: db, cfg: cfg, version: version}
}

// EmitServerStartup emits the server startup event immediately with available info (no DB query)
func (e *Emitter) EmitServerStartup() {
	// DetectMode is a pure scheme check (no network access), so it's safe to
	// recompute here rather than threading the mode through the constructor.
	// A malformed URI would already have failed at connection-setup time in
	// main.go before the server ever got this far, so the error here is
	// ignored — ModeBolt is DetectMode's safe zero-value fallback.
	connMode, _ := queryapi.DetectMode(e.cfg.URI)
	e.an.EmitEvent(e.an.NewStartupEvent(e.cfg.TransportMode, e.cfg.HTTPTLSEnabled, e.version, connMode.String()))
}

// EmitConnectionInitialized emits the connection initialized event with DB information (STDIO mode only)
func (e *Emitter) EmitConnectionInitialized(ctx context.Context) {
	if !e.an.IsEnabled() {
		return
	}

	records, err := e.db.ExecuteReadQuery(ctx, "CALL dbms.components()", map[string]any{})
	if err != nil {
		slog.Debug("Failed to collect connection metadata", "error", err.Error())
		return
	}

	connInfo := recordsToConnectionEventInfo(records)
	e.an.EmitEvent(e.an.NewConnectionInitializedEvent(connInfo))
}

// recordsToConnectionEventInfo converts dbms.components() records to ConnectionEventInfo
func recordsToConnectionEventInfo(records []*neo4j.Record) analytics.ConnectionEventInfo {
	// Default to "unknown" for all failure cases (empty records, malformed data, etc.)
	connInfo := analytics.ConnectionEventInfo{
		Neo4jVersion:  "unknown",
		Edition:       "unknown",
		CypherVersion: []string{"unknown"},
	}

	for _, record := range records {
		nameRaw, ok := record.Get("name")
		if !ok {
			slog.Debug("missing 'name' column in dbms.components record")
			continue
		}
		name, ok := nameRaw.(string)
		if !ok {
			slog.Debug("invalid 'name' type in dbms.components record")
			continue
		}

		editionRaw, ok := record.Get("edition")
		if !ok {
			slog.Debug("missing 'edition' column in dbms.components record")
			continue
		}
		edition, ok := editionRaw.(string)
		if !ok {
			slog.Debug("invalid 'edition' type in dbms.components record")
			continue
		}

		versionsRaw, ok := record.Get("versions")
		if !ok {
			slog.Debug("missing 'versions' column in dbms.components record")
			continue
		}
		versions, ok := versionsRaw.([]any)
		if !ok {
			slog.Debug("invalid 'versions' type in dbms.components record")
			continue
		}

		switch name {
		case "Neo4j Kernel":
			if len(versions) > 0 {
				if v, ok := versions[0].(string); ok {
					connInfo.Neo4jVersion = v
				}
			}
			connInfo.Edition = edition
		case "Cypher":
			var stringVersions []string
			for _, v := range versions {
				if s, ok := v.(string); ok {
					stringVersions = append(stringVersions, s)
				}
			}
			connInfo.CypherVersion = stringVersions
		}
	}
	return connInfo
}
