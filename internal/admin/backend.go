// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package admin implements the read/write config dashboard: a small JSON
// API plus an embedded HTML/CSS/vanilla-JS static page, mounted by
// internal/server under /admin. It depends only on internal/config (a leaf
// package) and stdlib — never on internal/server — so that internal/server
// can import this package to mount its routes without an import cycle.
// Neo4jMCPServer implements the Backend interface below and is handed to
// New as a plain interface value; this package neither knows nor cares that
// the concrete type behind it lives in internal/server.
package admin

import (
	"context"
	"net/http"
)

// ToolInfo is the subset of a registered tool's metadata the dashboard
// displays — deliberately primitive-typed rather than referencing
// mcpsdk.Tool, for the same import-boundary reason as ApplyOutcome below.
type ToolInfo struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Category string `json:"category"`
	ReadOnly bool   `json:"readOnly"`
}

// ApplyOutcome mirrors server.ApplyResult in primitive form, so this
// package never needs to import internal/server's types.
type ApplyOutcome struct {
	Tiers   []string `json:"tiers"`
	Bounced bool     `json:"bounced"`
}

// FieldDiff describes one changed Config field, for the dashboard's
// diff-before-apply preview.
type FieldDiff struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// PreviewResult is what a candidate config would do if applied, without
// actually applying it.
type PreviewResult struct {
	Changes []FieldDiff `json:"changes"`
	Tiers   []string    `json:"tiers"`
}

// EditableConfig is the explicit, narrow set of Config fields the dashboard
// can change — everything else (Username, Password, AdminToken itself,
// TransportMode, LogLevel/LogFormat, Telemetry, the header-name settings,
// ...) is intentionally not representable here at all. This is a whitelist
// by construction: the wire type simply has no field for anything not
// meant to be dashboard-editable, so there's no way for a request body to
// accidentally (or maliciously) clear a field like AdminToken that isn't
// exposed in the form in the first place — a full-Config round-trip
// (decode into *config.Config, re-Apply it) would zero out every field the
// client's JSON omitted, which is exactly the bug this type avoids.
type EditableConfig struct {
	ReadOnly               bool   `json:"readOnly"`
	EnabledTools           string `json:"enabledTools"`
	EnabledToolCategories  string `json:"enabledToolCategories"`
	OutputFormat           string `json:"outputFormat"`
	CypherMaxRows          int32  `json:"cypherMaxRows"`
	CypherMaxBytes         int32  `json:"cypherMaxBytes"`
	CypherTimeoutSeconds   int32  `json:"cypherTimeoutSeconds"`
	CypherMaxEstimatedRows int32  `json:"cypherMaxEstimatedRows"`
	SchemaSampleSize       int32  `json:"schemaSampleSize"`
	HTTPAllowedOrigins     string `json:"httpAllowedOrigins"`
	HTTPHost               string `json:"httpHost"`
	HTTPPort               string `json:"httpPort"`
	HTTPTLSEnabled         bool   `json:"httpTlsEnabled"`
	HTTPTLSCertFile        string `json:"httpTlsCertFile"`
	HTTPTLSKeyFile         string `json:"httpTlsKeyFile"`
	URI                    string `json:"uri"`
	Database               string `json:"database"`
}

// RedactedConfig is what GetConfig returns to the browser: every field
// EditableConfig exposes, plus a handful of read-only display fields.
// Password/AdminToken/Username/TransportMode and everything else live-only
// are deliberately absent — this package never sends a secret to the
// browser, and never accepts one back either (see EditableConfig).
type RedactedConfig struct {
	EditableConfig
	HasAdminToken bool `json:"hasAdminToken"`
}

// Backend is everything the admin dashboard needs from the running
// Neo4jMCPServer. Defined here (the consumer), implemented there — the
// standard Go way to avoid a two-package import cycle.
type Backend interface {
	// GetConfig returns the current effective config, redacted for display.
	GetConfig() RedactedConfig
	// Apply reconfigures the running server, changing only the fields
	// EditableConfig exposes. See server.Neo4jMCPServer.Apply for what
	// "without a restart" costs per field.
	Apply(ctx context.Context, edits EditableConfig) (ApplyOutcome, error)
	// Preview reports what Apply(ctx, edits) would do, without doing it —
	// the diff and tier classification the dashboard shows before enabling
	// its Apply button.
	Preview(edits EditableConfig) PreviewResult
	// ListTools returns metadata for every currently registered tool.
	ListTools() []ToolInfo
	// ServeMCP serves a single MCP JSON-RPC request through the exact same
	// handler/middleware chain a real client would hit — used by the
	// dashboard's built-in tool-call playground, so a call made from the
	// browser reflects precisely what an external client would experience
	// (including auth and tool-selection headers), with no separate
	// invocation path to get subtly out of sync with the real one.
	ServeMCP(w http.ResponseWriter, r *http.Request)
}
