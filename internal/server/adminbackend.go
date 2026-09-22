// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/admin"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

// adminBackend adapts Neo4jMCPServer to admin.Backend. It exists as a
// separate type, rather than putting Apply/ListTools/etc. directly on
// Neo4jMCPServer, because Neo4jMCPServer's own Apply/ListTools already have
// different signatures for internal use (returning ApplyResult and
// []mcpsdk.Tool respectively) — admin.Backend needs primitive-typed
// equivalents, per the import-boundary rationale on admin.Backend itself.
type adminBackend struct {
	s *Neo4jMCPServer
}

// newAdminBackend wraps s for use as the admin dashboard's Backend.
func newAdminBackend(s *Neo4jMCPServer) admin.Backend {
	return &adminBackend{s: s}
}

var _ admin.Backend = (*adminBackend)(nil)

// applyEditsTo returns a copy of base with only the EditableConfig fields
// overlaid — everything else (Username, Password, AdminToken, TransportMode,
// Telemetry, LogLevel/LogFormat, the header-name settings, ...) comes from
// base untouched. This is what makes EditableConfig safe to accept from the
// browser: there is no way for a request body to affect a field this
// function doesn't explicitly copy.
func applyEditsTo(base *config.Config, edits admin.EditableConfig) *config.Config {
	next := *base
	next.ReadOnly = edits.ReadOnly
	next.EnabledTools = edits.EnabledTools
	next.EnabledToolCategories = edits.EnabledToolCategories
	next.OutputFormat = config.OutputFormat(edits.OutputFormat)
	next.CypherMaxRows = edits.CypherMaxRows
	next.CypherMaxBytes = edits.CypherMaxBytes
	next.CypherTimeoutSeconds = edits.CypherTimeoutSeconds
	next.CypherMaxEstimatedRows = edits.CypherMaxEstimatedRows
	next.SchemaSampleSize = edits.SchemaSampleSize
	next.HTTPAllowedOrigins = edits.HTTPAllowedOrigins
	next.HTTPHost = edits.HTTPHost
	next.HTTPPort = edits.HTTPPort
	next.HTTPTLSEnabled = edits.HTTPTLSEnabled
	next.HTTPTLSCertFile = edits.HTTPTLSCertFile
	next.HTTPTLSKeyFile = edits.HTTPTLSKeyFile
	next.URI = edits.URI
	next.Database = edits.Database
	return &next
}

// toEditableConfig is applyEditsTo's inverse — the projection of a full
// Config down to the fields the dashboard displays/edits.
func toEditableConfig(cfg *config.Config) admin.EditableConfig {
	return admin.EditableConfig{
		ReadOnly:               cfg.ReadOnly,
		EnabledTools:           cfg.EnabledTools,
		EnabledToolCategories:  cfg.EnabledToolCategories,
		OutputFormat:           string(cfg.OutputFormat),
		CypherMaxRows:          cfg.CypherMaxRows,
		CypherMaxBytes:         cfg.CypherMaxBytes,
		CypherTimeoutSeconds:   cfg.CypherTimeoutSeconds,
		CypherMaxEstimatedRows: cfg.CypherMaxEstimatedRows,
		SchemaSampleSize:       cfg.SchemaSampleSize,
		HTTPAllowedOrigins:     cfg.HTTPAllowedOrigins,
		HTTPHost:               cfg.HTTPHost,
		HTTPPort:               cfg.HTTPPort,
		HTTPTLSEnabled:         cfg.HTTPTLSEnabled,
		HTTPTLSCertFile:        cfg.HTTPTLSCertFile,
		HTTPTLSKeyFile:         cfg.HTTPTLSKeyFile,
		URI:                    cfg.URI,
		Database:               cfg.Database,
	}
}

func (a *adminBackend) GetConfig() admin.RedactedConfig {
	cfg := a.s.config.Get()
	return admin.RedactedConfig{
		EditableConfig: toEditableConfig(cfg),
		HasAdminToken:  cfg.AdminToken != "",
	}
}

func (a *adminBackend) Apply(ctx context.Context, edits admin.EditableConfig) (admin.ApplyOutcome, error) {
	newCfg := applyEditsTo(a.s.config.Get(), edits)
	result, err := a.s.Apply(ctx, newCfg)
	if err != nil {
		return admin.ApplyOutcome{}, err
	}
	tiers := make([]string, 0, len(result.Tiers))
	for _, t := range result.Tiers {
		tiers = append(tiers, string(t))
	}
	return admin.ApplyOutcome{Tiers: tiers, Bounced: result.Bounced}, nil
}

// Preview reports what Apply would do, without doing it — the same
// diffTiers classification Apply itself uses, plus a human-readable
// per-field diff for the dashboard's confirmation view.
func (a *adminBackend) Preview(edits admin.EditableConfig) admin.PreviewResult {
	oldCfg := a.s.config.Get()
	newCfg := applyEditsTo(oldCfg, edits)
	tiers := diffTiers(oldCfg, newCfg)
	tierStrings := make([]string, 0, len(tiers))
	for _, t := range tiers {
		tierStrings = append(tierStrings, string(t))
	}

	return admin.PreviewResult{
		Changes: diffEditableFields(toEditableConfig(oldCfg), edits),
		Tiers:   tierStrings,
	}
}

// diffEditableFields compares the editable-field projections directly
// (rather than the full Config structs) since every field EditableConfig
// carries is safe to display in full — none of them are secrets.
func diffEditableFields(oldEdits, newEdits admin.EditableConfig) []admin.FieldDiff {
	var changes []admin.FieldDiff
	add := func(field string, oldV, newV any) {
		if fmt.Sprint(oldV) != fmt.Sprint(newV) {
			changes = append(changes, admin.FieldDiff{Field: field, Old: fmt.Sprint(oldV), New: fmt.Sprint(newV)})
		}
	}
	add("ReadOnly", oldEdits.ReadOnly, newEdits.ReadOnly)
	add("EnabledTools", oldEdits.EnabledTools, newEdits.EnabledTools)
	add("EnabledToolCategories", oldEdits.EnabledToolCategories, newEdits.EnabledToolCategories)
	add("OutputFormat", oldEdits.OutputFormat, newEdits.OutputFormat)
	add("CypherMaxRows", oldEdits.CypherMaxRows, newEdits.CypherMaxRows)
	add("CypherMaxBytes", oldEdits.CypherMaxBytes, newEdits.CypherMaxBytes)
	add("CypherTimeoutSeconds", oldEdits.CypherTimeoutSeconds, newEdits.CypherTimeoutSeconds)
	add("CypherMaxEstimatedRows", oldEdits.CypherMaxEstimatedRows, newEdits.CypherMaxEstimatedRows)
	add("SchemaSampleSize", oldEdits.SchemaSampleSize, newEdits.SchemaSampleSize)
	add("HTTPAllowedOrigins", oldEdits.HTTPAllowedOrigins, newEdits.HTTPAllowedOrigins)
	add("HTTPHost", oldEdits.HTTPHost, newEdits.HTTPHost)
	add("HTTPPort", oldEdits.HTTPPort, newEdits.HTTPPort)
	add("HTTPTLSEnabled", oldEdits.HTTPTLSEnabled, newEdits.HTTPTLSEnabled)
	add("HTTPTLSCertFile", oldEdits.HTTPTLSCertFile, newEdits.HTTPTLSCertFile)
	add("HTTPTLSKeyFile", oldEdits.HTTPTLSKeyFile, newEdits.HTTPTLSKeyFile)
	add("URI", oldEdits.URI, newEdits.URI)
	add("Database", oldEdits.Database, newEdits.Database)
	return changes
}

// ListTools translates ToolDefinition metadata into admin's primitive-typed
// ToolInfo.
func (a *adminBackend) ListTools() []admin.ToolInfo {
	s := a.s
	deps := s.buildToolDependencies()
	defs := s.getAllToolsDefs(deps)
	registered := make(map[string]bool)
	for _, t := range s.mcpServer.ListTools() {
		registered[t.Name] = true
	}

	infos := make([]admin.ToolInfo, 0, len(defs))
	for _, d := range defs {
		if !registered[d.definition.Tool.Name] {
			continue
		}
		infos = append(infos, admin.ToolInfo{
			Name:     d.definition.Tool.Name,
			Label:    d.Label(),
			Category: string(d.Category),
			ReadOnly: d.readonly,
		})
	}
	return infos
}

// ServeMCP serves req through the exact same handler/middleware chain a
// real MCP client hits, so the dashboard's tool-call playground reflects
// precisely what an external client would experience.
func (a *adminBackend) ServeMCP(w http.ResponseWriter, r *http.Request) {
	a.s.buildHandler().ServeHTTP(w, r)
}
