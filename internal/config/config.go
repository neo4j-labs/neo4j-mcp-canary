// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"crypto/tls"
	"fmt"
	"slices"
)

type TransportMode string

const (
	// DefaultSchemaSampleSize is the default value forwarded to apoc.meta.schema's
	// `sample` parameter, capping how many nodes per label APOC examines when
	// inferring the schema.
	DefaultSchemaSampleSize int32 = 1000
	// DefaultCypherMaxRows is the default per-call row cap applied by the streaming
	// read-cypher and write-cypher execution paths. It exists to protect the MCP client
	// from unbounded result sets — an agent that omits a LIMIT on a multi-million-row
	// table would otherwise hang for minutes while the driver buffers and serialises
	// the full payload. When the cap fires, the response includes a truncated=true flag
	// and a hint telling the caller to add a LIMIT. A value of 0 disables the cap.
	DefaultCypherMaxRows int32 = 1000
	// DefaultCypherMaxBytes is the default per-call byte cap applied alongside
	// DefaultCypherMaxRows. It complements the row cap: an agent asking for 1000
	// wide nodes (for example full Company records with 19 properties each) can
	// easily produce a response well over 1 MB, which then fails at the MCP
	// transport layer with an opaque "tool result too large" error — wasting the
	// work and giving the agent no structured signal. The byte cap causes the
	// streaming loop to stop at a size the transport can carry and surfaces a
	// truncation envelope with a hint that steers the agent toward a narrower
	// projection (for example RETURN c.name, c.companyNumber) rather than a
	// smaller LIMIT, because for wide nodes it's the per-row width that's the
	// real problem, not the row count.
	//
	// 900_000 bytes (~900 KB) leaves headroom under the observed 1 MB transport
	// ceiling. A value of 0 disables the cap.
	DefaultCypherMaxBytes int32 = 900_000
	// DefaultCypherTimeoutSeconds is the default context timeout (in seconds) for
	// read-cypher and write-cypher execution. Chosen to match DefaultSchemaTimeoutSeconds
	// so that a caller waiting on any single Cypher tool call sees consistent behaviour.
	// A value of 0 disables the timeout.
	DefaultCypherTimeoutSeconds int32 = 30
	// DefaultCypherMaxEstimatedRows is the default threshold for the EXPLAIN-time
	// estimate guard applied by read-cypher. Before executing a query, the handler
	// reads the planner's EstimatedRows at the root of the EXPLAIN plan; if it
	// exceeds this threshold, the query is refused with a hint telling the caller
	// to add a LIMIT.
	//
	// This sits above the row cap and context timeout as a third layer of defence:
	// the row cap reacts after rows start flowing, the timeout reacts after time
	// passes, and this guard reacts before the query even starts running — based
	// on what the planner already knows about the shape of the work.
	//
	// 1,000,000 is chosen as a clear "truly unbounded territory" line rather than
	// a tight match to DefaultCypherMaxRows: the planner already folds LIMIT
	// clauses into the root EstimatedRows, so a legitimate MATCH ... LIMIT 100
	// query has a root estimate around 100 and passes cleanly. A bare MATCH on a
	// multi-million-row label on the other hand estimates into the millions and is
	// exactly the shape this guard is trying to catch. A value of 0 disables the guard.
	DefaultCypherMaxEstimatedRows int32         = 1000000
	TransportModeStdio            TransportMode = "stdio"
	TransportModeHTTP             TransportMode = "http"
	DeprecatedVariableMessage     string        = "Warning: deprecated environment variable \"%s\". Please use: \"%s\" instead\n"
)

// ValidTransportModes defines the allowed transport mode values
var ValidTransportModes = []TransportMode{TransportModeStdio, TransportModeHTTP}

// OutputFormat identifies the wire format tool responses are rendered in
// before being sent back to the LLM client.
type OutputFormat string

const (
	// OutputFormatJSON renders tool responses as JSON (the default,
	// unchanged behaviour).
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatTOON renders tool responses as TOON (Token-Oriented Object
	// Notation), a compact format that cuts token usage versus JSON —
	// particularly for the tabular row shapes read-cypher/write-cypher and
	// list-gds-procedures return — at the cost of being less familiar to
	// generic JSON tooling.
	OutputFormatTOON OutputFormat = "toon"
)

// ValidOutputFormats defines the allowed output format values.
var ValidOutputFormats = []OutputFormat{OutputFormatJSON, OutputFormatTOON}

// Config holds the application configuration
type Config struct {
	URI                                         string
	Username                                    string
	Password                                    string // #nosec G117
	Database                                    string
	ReadOnly                                    bool // If true, disables write tools
	Telemetry                                   bool // If false, disables telemetry
	LogLevel                                    string
	LogFormat                                   string
	OutputFormat                                OutputFormat // Tool response format sent to the LLM client: "json" (default) or "toon"
	SchemaSampleSize                            int32
	CypherMaxRows                               int32         // Per-call row cap applied by read-cypher and write-cypher; 0 disables the cap
	CypherMaxBytes                              int32         // Per-call byte cap applied alongside CypherMaxRows; 0 disables the cap
	CypherTimeoutSeconds                        int32         // Context timeout in seconds for read-cypher and write-cypher execution; 0 disables the timeout
	CypherMaxEstimatedRows                      int32         // EXPLAIN-time estimate threshold above which read-cypher refuses the query; 0 disables the guard
	TransportMode                               TransportMode // MCP Transport mode (e.g., "stdio", "http")
	HTTPPort                                    string        // HTTP server port (default: "443" with TLS, "80" without TLS)
	HTTPHost                                    string        // HTTP server host (default: "127.0.0.1")
	HTTPAllowedOrigins                          string        // Comma-separated list of allowed CORS origins (optional, "*" for all)
	HTTPTLSEnabled                              bool          // If true, enables TLS/HTTPS for HTTP server (default: false)
	HTTPTLSCertFile                             string        // Path to TLS certificate file (required if HTTPTLSEnabled is true)
	HTTPTLSKeyFile                              string        // Path to TLS private key file (required if HTTPTLSEnabled is true)
	AuthHeaderName                              string        // HTTP header name to read auth credentials from (default: "Authorization")
	AllowUnauthenticatedPing                    bool          // If true, allows unauthenticated ping health checks in HTTP mode
	AllowUnauthenticatedToolsList               bool          // If true, allows unauthenticated tools list in HTTP mode
	AllowUnauthenticatedInitialize              bool          // If true, allows unauthenticated initialize in HTTP mode
	AllowUnauthenticatedNotificationsInitialize bool          // If true, allows unauthenticated initialize notifications in HTTP mode
}

// Validate validates the configuration and returns an error if invalid
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("configuration is required but was nil")
	}

	// URI is always required
	if c.URI == "" {
		return fmt.Errorf("Neo4j URI is required but was empty")
	}

	// Default to stdio if not provided (maintains backward compatibility with tests constructing Config directly)
	if c.TransportMode == "" {
		c.TransportMode = TransportModeStdio
	}

	// Validate transport mode
	if !slices.Contains(ValidTransportModes, c.TransportMode) {
		return fmt.Errorf("invalid transport mode '%s', must be one of %v", c.TransportMode, ValidTransportModes)
	}

	// For STDIO mode, require username and password from environment
	// For HTTP mode, credentials come from per-request Basic Auth headers
	if c.TransportMode == TransportModeStdio {
		if c.Username == "" {
			return fmt.Errorf("Neo4j username is required for STDIO mode")
		}
		if c.Password == "" {
			return fmt.Errorf("Neo4j password is required for STDIO mode")
		}
	} else if c.Username != "" || c.Password != "" {
		return fmt.Errorf("Neo4j username and password should not be set for HTTP transport mode; credentials are provided per-request via Basic Auth headers")
	}

	// For HTTP mode with TLS enabled, require certificate and key files
	if c.TransportMode == TransportModeHTTP && c.HTTPTLSEnabled {
		if c.HTTPTLSCertFile == "" {
			return fmt.Errorf("TLS certificate file is required when TLS is enabled (set NEO4J_MCP_HTTP_TLS_CERT_FILE)")
		}
		if c.HTTPTLSKeyFile == "" {
			return fmt.Errorf("TLS key file is required when TLS is enabled (set NEO4J_MCP_HTTP_TLS_KEY_FILE)")
		}

		// Validate that certificate and key files exist and are valid
		// This provides early, clear error messages before attempting to start the server
		if _, err := tls.LoadX509KeyPair(c.HTTPTLSCertFile, c.HTTPTLSKeyFile); err != nil {
			return fmt.Errorf("failed to load TLS certificate and key: %w", err)
		}
	}

	return nil
}
