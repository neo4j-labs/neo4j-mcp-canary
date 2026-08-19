// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"fmt"
	"os"
	"slices"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/logger"
)

// CLIOverrides holds resolved CLI flag values keyed by the Config struct
// field name each value belongs to (e.g. "URI", "SchemaSampleSize"). It also
// carries the one non-Config key "ConfigFile" for the --config-file flag.
// Only non-empty values should be set; see cli.ParseConfigFlags.
type CLIOverrides map[string]string

// configFileOverrideKey is the CLIOverrides key used for the --config-file
// flag. It is not a Config struct field — nothing needs it after startup —
// so it deliberately lives outside the Fields() schema.
const configFileOverrideKey = "ConfigFile"

// configFileEnvVar is the environment variable equivalent of --config-file.
const configFileEnvVar = "NEO4J_CONFIG_FILE"

// Field describes one configuration parameter: where it comes from (env var,
// optional deprecated aliases, CLI flag) and how a resolved raw string is
// applied to a Config. This is the single place to add, rename, or remove a
// configuration parameter — adding one here (plus the corresponding Config
// struct field in config.go) is the entire change; internal/cli and
// cmd/neo4j-mcp/main.go derive everything else from this list.
type Field struct {
	// Name is the Config struct field this parameter sets; also the key used
	// in the CLIOverrides map and checked against Config by TestFields_MatchConfigStruct.
	Name string
	// EnvVar is the canonical environment variable name.
	EnvVar string
	// DeprecatedEnvVars are legacy env var names that still work but print
	// DeprecatedVariableMessage when set, independent of whether they end up
	// winning the resolution.
	DeprecatedEnvVars []string
	// FlagName is the stdlib flag name (no leading dashes). Empty means this
	// parameter has no CLI flag.
	FlagName string
	// Placeholder is a short cosmetic value hint shown in generated --help
	// output, e.g. "URI", "BOOLEAN", "INT".
	Placeholder string
	// Description is a one-line, human-readable description used in
	// generated --help output and docs.
	Description string
	// DefaultDisplay is a human-readable default shown in generated docs,
	// e.g. "1000", "info". Empty means "no default worth documenting".
	DefaultDisplay string
	// Required marks a parameter as one that must be supplied by some
	// source; it only affects which help-text section the parameter is
	// listed under (validation itself lives in Config.Validate).
	Required bool
	// Setter parses raw (the resolved value from CLI/env/file, or "" if
	// unresolved by any source) and applies it — including any
	// parameter-specific default — to cfg.
	Setter func(cfg *Config, raw string)
}

// Fields returns the configuration schema: one Field per Config struct
// field, in the order they should appear in generated CLI help output.
func Fields() []Field {
	return fields
}

func defaultString(raw, def string) string {
	if raw == "" {
		return def
	}
	return raw
}

var fields = []Field{
	{
		Name: "URI", EnvVar: "NEO4J_URI", FlagName: "neo4j-uri",
		Placeholder: "URI", Description: "Neo4j connection URI", Required: true,
		Setter: func(cfg *Config, raw string) { cfg.URI = raw },
	},
	{
		Name: "Username", EnvVar: "NEO4J_USERNAME", FlagName: "neo4j-username",
		Placeholder: "USERNAME", Description: "Database username", Required: true,
		Setter: func(cfg *Config, raw string) { cfg.Username = raw },
	},
	{
		Name: "Password", EnvVar: "NEO4J_PASSWORD", FlagName: "neo4j-password",
		Placeholder: "PASSWORD", Description: "Database password", Required: true,
		Setter: func(cfg *Config, raw string) { cfg.Password = raw },
	},
	{
		Name: "Database", EnvVar: "NEO4J_DATABASE", FlagName: "neo4j-database",
		Placeholder: "DATABASE", Description: "Database name", DefaultDisplay: "neo4j",
		Setter: func(cfg *Config, raw string) { cfg.Database = defaultString(raw, "neo4j") },
	},
	{
		Name: "ReadOnly", EnvVar: "NEO4J_READ_ONLY", FlagName: "neo4j-read-only",
		Placeholder: "BOOLEAN", Description: "Enable read-only mode: true or false", DefaultDisplay: "false",
		Setter: func(cfg *Config, raw string) { cfg.ReadOnly = ParseBool(raw, false) },
	},
	{
		Name: "Telemetry", EnvVar: "NEO4J_TELEMETRY", FlagName: "neo4j-telemetry",
		Placeholder: "BOOLEAN", Description: "Enable telemetry: true or false", DefaultDisplay: "true",
		Setter: func(cfg *Config, raw string) { cfg.Telemetry = ParseBool(raw, true) },
	},
	{
		// No CLI flag today (matches pre-refactor behaviour); config-file and
		// env sources still apply.
		Name: "LogLevel", EnvVar: "NEO4J_LOG_LEVEL",
		Description: "Log level", DefaultDisplay: "info",
		Setter: func(cfg *Config, raw string) {
			level := defaultString(raw, "info")
			if !slices.Contains(logger.ValidLogLevels, level) {
				fmt.Fprintf(os.Stderr, "Warning: invalid NEO4J_LOG_LEVEL '%s', using default 'info'. Valid values: %v\n", level, logger.ValidLogLevels)
				level = "info"
			}
			cfg.LogLevel = level
		},
	},
	{
		Name: "LogFormat", EnvVar: "NEO4J_LOG_FORMAT",
		Description: "Log format", DefaultDisplay: "text",
		Setter: func(cfg *Config, raw string) {
			format := defaultString(raw, "text")
			if !slices.Contains(logger.ValidLogFormats, format) {
				fmt.Fprintf(os.Stderr, "Warning: invalid NEO4J_LOG_FORMAT '%s', using default 'text'. Valid values: %v\n", format, logger.ValidLogFormats)
				format = "text"
			}
			cfg.LogFormat = format
		},
	},
	{
		Name: "SchemaSampleSize", EnvVar: "NEO4J_SCHEMA_SAMPLE_SIZE", FlagName: "neo4j-schema-sample-size",
		Placeholder: "INT", Description: "Number of nodes per label APOC samples when inferring schema", DefaultDisplay: "1000",
		Setter: func(cfg *Config, raw string) { cfg.SchemaSampleSize = ParseInt32(raw, DefaultSchemaSampleSize) },
	},
	{
		Name: "CypherMaxRows", EnvVar: "NEO4J_CYPHER_MAX_ROWS", FlagName: "neo4j-cypher-max-rows",
		Placeholder: "INT", Description: "Per-call row cap for read-cypher and write-cypher; 0 disables", DefaultDisplay: "1000",
		Setter: func(cfg *Config, raw string) { cfg.CypherMaxRows = ParseInt32(raw, DefaultCypherMaxRows) },
	},
	{
		Name: "CypherMaxBytes", EnvVar: "NEO4J_CYPHER_MAX_BYTES", FlagName: "neo4j-cypher-max-bytes",
		Placeholder: "INT", Description: "Per-call byte cap for read-cypher and write-cypher; 0 disables", DefaultDisplay: "900000",
		Setter: func(cfg *Config, raw string) { cfg.CypherMaxBytes = ParseInt32(raw, DefaultCypherMaxBytes) },
	},
	{
		Name: "CypherTimeoutSeconds", EnvVar: "NEO4J_CYPHER_TIMEOUT", FlagName: "neo4j-cypher-timeout",
		Placeholder: "INT", Description: "Context timeout in seconds for read-cypher and write-cypher execution; 0 disables", DefaultDisplay: "30",
		Setter: func(cfg *Config, raw string) { cfg.CypherTimeoutSeconds = ParseInt32(raw, DefaultCypherTimeoutSeconds) },
	},
	{
		Name: "CypherMaxEstimatedRows", EnvVar: "NEO4J_CYPHER_MAX_ESTIMATED_ROWS", FlagName: "neo4j-cypher-max-estimated-rows",
		Placeholder: "INT", Description: "EXPLAIN-time estimate threshold above which read-cypher refuses the query; 0 disables", DefaultDisplay: "1000000",
		Setter: func(cfg *Config, raw string) {
			cfg.CypherMaxEstimatedRows = ParseInt32(raw, DefaultCypherMaxEstimatedRows)
		},
	},
	{
		Name: "TransportMode", EnvVar: "NEO4J_TRANSPORT_MODE", DeprecatedEnvVars: []string{"NEO4J_MCP_TRANSPORT"},
		FlagName: "neo4j-transport-mode", Placeholder: "MODE",
		Description: "MCP Transport mode (e.g., 'stdio', 'http')", DefaultDisplay: "stdio",
		Setter: func(cfg *Config, raw string) {
			cfg.TransportMode = TransportMode(defaultString(raw, string(TransportModeStdio)))
		},
	},
	{
		// HTTPPort's default depends on HTTPTLSEnabled, resolved after this
		// per-field loop; see LoadConfig's post-processing step.
		Name: "HTTPPort", EnvVar: "NEO4J_MCP_HTTP_PORT", FlagName: "neo4j-http-port",
		Placeholder: "PORT", Description: "HTTP server port", DefaultDisplay: "443 with TLS, 80 without TLS",
		Setter: func(cfg *Config, raw string) { cfg.HTTPPort = raw },
	},
	{
		Name: "HTTPHost", EnvVar: "NEO4J_MCP_HTTP_HOST", FlagName: "neo4j-http-host",
		Placeholder: "HOST", Description: "HTTP server host", DefaultDisplay: "127.0.0.1",
		Setter: func(cfg *Config, raw string) { cfg.HTTPHost = defaultString(raw, "127.0.0.1") },
	},
	{
		Name: "HTTPAllowedOrigins", EnvVar: "NEO4J_MCP_HTTP_ALLOWED_ORIGINS", FlagName: "neo4j-http-allowed-origins",
		Placeholder: "ORIGINS", Description: "Comma-separated list of allowed CORS origins",
		Setter: func(cfg *Config, raw string) { cfg.HTTPAllowedOrigins = raw },
	},
	{
		Name: "HTTPTLSEnabled", EnvVar: "NEO4J_MCP_HTTP_TLS_ENABLED", FlagName: "neo4j-http-tls-enabled",
		Placeholder: "BOOLEAN", Description: "Enable TLS/HTTPS for HTTP server: true or false", DefaultDisplay: "false",
		Setter: func(cfg *Config, raw string) { cfg.HTTPTLSEnabled = ParseBool(raw, false) },
	},
	{
		Name: "HTTPTLSCertFile", EnvVar: "NEO4J_MCP_HTTP_TLS_CERT_FILE", FlagName: "neo4j-http-tls-cert-file",
		Placeholder: "PATH", Description: "Path to TLS certificate file (required if TLS is enabled)",
		Setter: func(cfg *Config, raw string) { cfg.HTTPTLSCertFile = raw },
	},
	{
		Name: "HTTPTLSKeyFile", EnvVar: "NEO4J_MCP_HTTP_TLS_KEY_FILE", FlagName: "neo4j-http-tls-key-file",
		Placeholder: "PATH", Description: "Path to TLS private key file (required if TLS is enabled)",
		Setter: func(cfg *Config, raw string) { cfg.HTTPTLSKeyFile = raw },
	},
	{
		// AuthHeaderName's default is applied here, but its trim +
		// empty-after-trim validation happens as an explicit post-processing
		// step in LoadConfig, since it produces a specific validation error.
		Name: "AuthHeaderName", EnvVar: "NEO4J_HTTP_AUTH_HEADER_NAME", FlagName: "neo4j-http-auth-header-name",
		Placeholder: "HEADER", Description: "Name of the HTTP header to read auth credentials from", DefaultDisplay: "Authorization",
		Setter: func(cfg *Config, raw string) { cfg.AuthHeaderName = defaultString(raw, "Authorization") },
	},
	{
		Name: "AllowUnauthenticatedPing", EnvVar: "NEO4J_HTTP_ALLOW_UNAUTHENTICATED_PING", FlagName: "neo4j-http-allow-unauthenticated-ping",
		Placeholder: "BOOLEAN", Description: "Allow unauthenticated ping health checks: true or false", DefaultDisplay: "true",
		Setter: func(cfg *Config, raw string) { cfg.AllowUnauthenticatedPing = ParseBool(raw, true) },
	},
	{
		Name: "AllowUnauthenticatedToolsList", EnvVar: "NEO4J_HTTP_ALLOW_UNAUTHENTICATED_TOOLS_LIST", FlagName: "neo4j-http-allow-unauthenticated-tools-list",
		Placeholder: "BOOLEAN", Description: "Allow unauthenticated tools list: true or false", DefaultDisplay: "true",
		Setter: func(cfg *Config, raw string) { cfg.AllowUnauthenticatedToolsList = ParseBool(raw, true) },
	},
	{
		Name: "AllowUnauthenticatedInitialize", EnvVar: "NEO4J_HTTP_ALLOW_UNAUTHENTICATED_INITIALIZE", FlagName: "neo4j-http-allow-unauthenticated-initialize",
		Placeholder: "BOOLEAN", Description: "Allow unauthenticated initialize: true or false", DefaultDisplay: "true",
		Setter: func(cfg *Config, raw string) { cfg.AllowUnauthenticatedInitialize = ParseBool(raw, true) },
	},
	{
		Name: "AllowUnauthenticatedNotificationsInitialize", EnvVar: "NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE", FlagName: "neo4j-http-allow-unauthenticated-notifications-initialize",
		Placeholder: "BOOLEAN", Description: "Allow unauthenticated initialize notifications: true or false", DefaultDisplay: "true",
		Setter: func(cfg *Config, raw string) {
			cfg.AllowUnauthenticatedNotificationsInitialize = ParseBool(raw, true)
		},
	},
}
