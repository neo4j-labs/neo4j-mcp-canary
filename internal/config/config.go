// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/logger"
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
	SchemaSampleSize                            int32
	CypherMaxRows                               int32 // Per-call row cap applied by read-cypher and write-cypher; 0 disables the cap
	CypherMaxBytes                              int32 // Per-call byte cap applied alongside CypherMaxRows; 0 disables the cap
	CypherTimeoutSeconds                        int32 // Context timeout in seconds for read-cypher and write-cypher execution; 0 disables the timeout
	CypherMaxEstimatedRows                      int32 // EXPLAIN-time estimate threshold above which read-cypher refuses the query; 0 disables the guard
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

// CLIOverrides holds optional configuration values from CLI flags
type CLIOverrides struct {
	URI                                         string
	Username                                    string
	Password                                    string // #nosec G117
	Database                                    string
	ReadOnly                                    string
	Telemetry                                   string
	CypherMaxRows                               string
	CypherMaxBytes                              string
	CypherTimeout                               string
	CypherMaxEstimatedRows                      string
	TransportMode                               string
	Port                                        string
	Host                                        string
	AllowedOrigins                              string
	TLSEnabled                                  string
	TLSCertFile                                 string
	TLSKeyFile                                  string
	AuthHeaderName                              string
	AllowUnauthenticatedPing                    string
	AllowUnauthenticatedToolsList               string
	AllowUnauthenticatedInitialize              string
	AllowUnauthenticatedNotificationsInitialize string
}

// LoadConfig loads configuration from environment variables, applies CLI overrides, and validates.
// CLI flag values take precedence over environment variables.
// Returns an error if required configuration is missing or invalid.
func LoadConfig(cliOverrides *CLIOverrides) (*Config, error) {
	logLevel := GetEnvWithDefault("NEO4J_LOG_LEVEL", "info")
	logFormat := GetEnvWithDefault("NEO4J_LOG_FORMAT", "text")

	// Validate log level and use default if invalid
	if !slices.Contains(logger.ValidLogLevels, logLevel) {
		fmt.Fprintf(os.Stderr, "Warning: invalid NEO4J_LOG_LEVEL '%s', using default 'info'. Valid values: %v\n", logLevel, logger.ValidLogLevels)
		logLevel = "info"
	}

	// Validate log format and use default if invalid
	if !slices.Contains(logger.ValidLogFormats, logFormat) {
		fmt.Fprintf(os.Stderr, "Warning: invalid NEO4J_LOG_FORMAT '%s', using default 'text'. Valid values: %v\n", logFormat, logger.ValidLogFormats)
		logFormat = "text"
	}

	if GetEnv("NEO4J_MCP_TRANSPORT") != "" {
		fmt.Fprintf(os.Stderr, DeprecatedVariableMessage, "NEO4J_MCP_TRANSPORT", "NEO4J_TRANSPORT_MODE")
	}

	cfg := &Config{
		URI:                            GetEnv("NEO4J_URI"),
		Username:                       GetEnv("NEO4J_USERNAME"),
		Password:                       GetEnv("NEO4J_PASSWORD"),
		Database:                       GetEnvWithDefault("NEO4J_DATABASE", "neo4j"),
		ReadOnly:                       ParseBool(GetEnv("NEO4J_READ_ONLY"), false),
		Telemetry:                      ParseBool(GetEnv("NEO4J_TELEMETRY"), true),
		LogLevel:                       logLevel,
		LogFormat:                      logFormat,
		SchemaSampleSize:               ParseInt32(GetEnv("NEO4J_SCHEMA_SAMPLE_SIZE"), DefaultSchemaSampleSize),
		CypherMaxRows:                  ParseInt32(GetEnv("NEO4J_CYPHER_MAX_ROWS"), DefaultCypherMaxRows),
		CypherMaxBytes:                 ParseInt32(GetEnv("NEO4J_CYPHER_MAX_BYTES"), DefaultCypherMaxBytes),
		CypherTimeoutSeconds:           ParseInt32(GetEnv("NEO4J_CYPHER_TIMEOUT"), DefaultCypherTimeoutSeconds),
		CypherMaxEstimatedRows:         ParseInt32(GetEnv("NEO4J_CYPHER_MAX_ESTIMATED_ROWS"), DefaultCypherMaxEstimatedRows),
		TransportMode:                  GetTransportModeWithDefault("NEO4J_TRANSPORT_MODE", GetTransportModeWithDefault("NEO4J_MCP_TRANSPORT", TransportModeStdio)),
		HTTPPort:                       GetEnv("NEO4J_MCP_HTTP_PORT"), // Default set after TLS determination
		HTTPHost:                       GetEnvWithDefault("NEO4J_MCP_HTTP_HOST", "127.0.0.1"),
		HTTPAllowedOrigins:             GetEnv("NEO4J_MCP_HTTP_ALLOWED_ORIGINS"),
		HTTPTLSEnabled:                 ParseBool(GetEnv("NEO4J_MCP_HTTP_TLS_ENABLED"), false),
		HTTPTLSCertFile:                GetEnv("NEO4J_MCP_HTTP_TLS_CERT_FILE"),
		HTTPTLSKeyFile:                 GetEnv("NEO4J_MCP_HTTP_TLS_KEY_FILE"),
		AuthHeaderName:                 GetEnvWithDefault("NEO4J_HTTP_AUTH_HEADER_NAME", "Authorization"),
		AllowUnauthenticatedPing:       ParseBool(GetEnv("NEO4J_HTTP_ALLOW_UNAUTHENTICATED_PING"), true),
		AllowUnauthenticatedToolsList:  ParseBool(GetEnv("NEO4J_HTTP_ALLOW_UNAUTHENTICATED_TOOLS_LIST"), true),
		AllowUnauthenticatedInitialize: ParseBool(GetEnv("NEO4J_HTTP_ALLOW_UNAUTHENTICATED_INITIALIZE"), true),
		AllowUnauthenticatedNotificationsInitialize: ParseBool(GetEnv("NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE"), true),
	}

	// Apply CLI overrides if provided
	if cliOverrides != nil {
		if cliOverrides.URI != "" {
			cfg.URI = cliOverrides.URI
		}
		if cliOverrides.Username != "" {
			cfg.Username = cliOverrides.Username
		}
		if cliOverrides.Password != "" {
			cfg.Password = cliOverrides.Password
		}
		if cliOverrides.Database != "" {
			cfg.Database = cliOverrides.Database
		}
		if cliOverrides.ReadOnly != "" {
			cfg.ReadOnly = ParseBool(cliOverrides.ReadOnly, false)
		}
		if cliOverrides.Telemetry != "" {
			cfg.Telemetry = ParseBool(cliOverrides.Telemetry, true)
		}
		if cliOverrides.CypherMaxRows != "" {
			cfg.CypherMaxRows = ParseInt32(cliOverrides.CypherMaxRows, DefaultCypherMaxRows)
		}
		if cliOverrides.CypherMaxBytes != "" {
			cfg.CypherMaxBytes = ParseInt32(cliOverrides.CypherMaxBytes, DefaultCypherMaxBytes)
		}
		if cliOverrides.CypherTimeout != "" {
			cfg.CypherTimeoutSeconds = ParseInt32(cliOverrides.CypherTimeout, DefaultCypherTimeoutSeconds)
		}
		if cliOverrides.CypherMaxEstimatedRows != "" {
			cfg.CypherMaxEstimatedRows = ParseInt32(cliOverrides.CypherMaxEstimatedRows, DefaultCypherMaxEstimatedRows)
		}
		if cliOverrides.TransportMode != "" {
			cfg.TransportMode = TransportMode(cliOverrides.TransportMode)
		}
		if cliOverrides.Port != "" {
			cfg.HTTPPort = cliOverrides.Port
		}
		if cliOverrides.Host != "" {
			cfg.HTTPHost = cliOverrides.Host
		}
		if cliOverrides.AllowedOrigins != "" {
			cfg.HTTPAllowedOrigins = cliOverrides.AllowedOrigins
		}
		if cliOverrides.TLSEnabled != "" {
			cfg.HTTPTLSEnabled = ParseBool(cliOverrides.TLSEnabled, false)
		}
		if cliOverrides.TLSCertFile != "" {
			cfg.HTTPTLSCertFile = cliOverrides.TLSCertFile
		}
		if cliOverrides.TLSKeyFile != "" {
			cfg.HTTPTLSKeyFile = cliOverrides.TLSKeyFile
		}
		if cliOverrides.AuthHeaderName != "" {
			cfg.AuthHeaderName = cliOverrides.AuthHeaderName
		}
		if cliOverrides.AllowUnauthenticatedPing != "" {
			cfg.AllowUnauthenticatedPing = ParseBool(cliOverrides.AllowUnauthenticatedPing, false)
		}
		if cliOverrides.AllowUnauthenticatedToolsList != "" {
			cfg.AllowUnauthenticatedToolsList = ParseBool(cliOverrides.AllowUnauthenticatedToolsList, false)
		}
		if cliOverrides.AllowUnauthenticatedInitialize != "" {
			cfg.AllowUnauthenticatedInitialize = ParseBool(cliOverrides.AllowUnauthenticatedInitialize, false)
		}
		if cliOverrides.AllowUnauthenticatedNotificationsInitialize != "" {
			cfg.AllowUnauthenticatedNotificationsInitialize = ParseBool(cliOverrides.AllowUnauthenticatedNotificationsInitialize, false)
		}
	}

	// Set default HTTP port based on TLS configuration if not explicitly provided
	// Default to 443 for HTTPS, 80 for HTTP
	if cfg.HTTPPort == "" {
		if cfg.HTTPTLSEnabled {
			cfg.HTTPPort = "443"
		} else {
			cfg.HTTPPort = "80"
		}
	}

	// Normalize and validate
	headName := strings.TrimSpace(cfg.AuthHeaderName)
	if headName == "" {
		return nil, fmt.Errorf("invalid auth header name: explicitly configured header name cannot be empty; unset NEO4J_HTTP_AUTH_HEADER_NAME or provide a valid header name")
	}
	// store normalized value
	cfg.AuthHeaderName = headName

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// GetEnv returns the value of an environment variable or empty string if not set
func GetEnv(key string) string {
	return os.Getenv(key)
}

// GetEnvWithDefault returns the value of an environment variable or a default value
func GetEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetTransportModeWithDefault returns the value of an environment variable or a default value
func GetTransportModeWithDefault(key, defaultValue TransportMode) TransportMode {
	if value := os.Getenv(string(key)); value != "" {
		return TransportMode(value)
	}
	return defaultValue
}

// ParseBool parses a string to bool using strconv.ParseBool.
// Returns the default value if the string is empty or invalid.
// Logs a warning if the value is non-empty but invalid.
// Accepts: "1", "t", "T", "true", "True", "TRUE" for true
//
//	"0", "f", "F", "false", "False", "FALSE" for false
func ParseBool(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("Warning: Invalid boolean value %q, using default: %v", value, defaultValue)
		return defaultValue
	}
	return parsed
}

// ParseInt32 parses a string to int32.
// Returns the default value if the string is empty or invalid.
func ParseInt32(value string, defaultValue int32) int32 {
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		log.Printf("Warning: Invalid integer value %q, using default: %v", value, defaultValue)
		return defaultValue
	}
	return int32(parsed)
}
