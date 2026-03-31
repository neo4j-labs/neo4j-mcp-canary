// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const (
	protocolHTTP                = "http"
	protocolHTTPS               = "https"
	serverHTTPShutdownTimeout   = 65 * time.Second  // Timeout for graceful shutdown (must exceed WriteTimeout to allow active requests to complete)
	serverHTTPReadHeaderTimeout = 5 * time.Second   // SECURITY: Maximum time to read request headers (prevents Slowloris attacks)
	serverHTTPReadTimeout       = 15 * time.Second  // SECURITY: Maximum time to read entire request including body (prevents slow-read attacks)
	serverHTTPWriteTimeout      = 60 * time.Second  // FUNCTIONALITY: Maximum time to write response (allows complex Neo4j queries and large result sets)
	serverHTTPIdleTimeout       = 120 * time.Second // PERFORMANCE: Maximum time to keep idle keep-alive connections open (improves connection reuse)
	mcpServerInstruction        = "This is the Neo4j official MCP server providing tool calling to interact " +
		"with your Neo4j database. Always start by calling get-schema to understand " +
		"the graph data model, available relationships, and indexes " +
		"(including full-text indexes which can be queried with " +
		"db.index.fulltext.queryNodes() and db.index.fulltext.queryRelationships()). " +
		"Check list-gds-procedures for available graph analytics " +
		"such as centrality, community detection, and pathfinding before " +
		"writing manual traversals. Use read-cypher for queries and " +
		"write-cypher for mutations."
)

// Neo4jMCPServer represents the MCP server instance
type Neo4jMCPServer struct {
	MCPServer          *server.MCPServer
	httpServer         *http.Server
	HTTPServerReady    chan struct{}
	shutdownChan       chan struct{}
	config             *config.Config
	dbService          database.Service
	version            string
	anService          analytics.Service
	gdsInstalled       bool
	vectorIndexesFound bool
	initMu             sync.Mutex
	connectionVerified atomic.Bool
}

// NewNeo4jMCPServer creates a new MCP server instance
// The config parameter is expected to be already validated
func NewNeo4jMCPServer(version string, cfg *config.Config, dbService database.Service, anService analytics.Service) *Neo4jMCPServer {

	neo4jServer := &Neo4jMCPServer{
		HTTPServerReady:    make(chan struct{}),
		shutdownChan:       make(chan struct{}),
		config:             cfg,
		dbService:          dbService,
		version:            version,
		anService:          anService,
		gdsInstalled:       false,
		vectorIndexesFound: false,
	}

	hooks := neo4jServer.configureHooks()

	mcpServer := server.NewMCPServer(
		"neo4j-mcp",
		version,
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
		server.WithInstructions(mcpServerInstruction),
	)

	neo4jServer.MCPServer = mcpServer

	return neo4jServer
}

// Start initializes and starts the MCP server
func (s *Neo4jMCPServer) Start() error {

	switch s.config.TransportMode {
	case config.TransportModeHTTP:
		slog.Info("Registering server tools")
		if err := s.registerTools(); err != nil {
			return err
		}
		// in case of http mode, the initialization process is delayed until the credentials are available.
		// when the first client is performing the initialize request then the server perform

		s.emitServerStartupEvent()

		return s.StartHTTPServer()
	case config.TransportModeStdio:
		{
			err := s.verifyRequirements(context.Background())
			if err != nil {
				return err
			}

			// Register tools
			if err := s.registerTools(); err != nil {
				return fmt.Errorf("failed to register tools: %w", err)
			}

			s.emitServerStartupEvent()
			s.emitConnectionInitializedEvent(context.Background())

			return server.ServeStdio(s.MCPServer)
		}
	default:
		return fmt.Errorf("unsupported transport mode: %s", s.config.TransportMode)
	}
}

// parseAllowedOrigins parses the allowed origins string into a slice of strings
func parseAllowedOrigins(allowedOriginsStr string) []string {
	if allowedOriginsStr == "" {
		return []string{}
	}

	if allowedOriginsStr == "*" {
		return []string{"*"}
	}
	origins := strings.Split(allowedOriginsStr, ",")
	allowedOrigins := make([]string, 0, len(origins))

	for _, origin := range origins {
		allowedOrigins = append(allowedOrigins, strings.TrimSpace(origin))
	}

	return allowedOrigins
}

// verifyRequirements check the Neo4j requirements:
// - A valid connection with a Neo4j instance.
// - The ability to perform a read query (database name is correctly defined).
// - Required plugin installed: APOC (specifically apoc.meta.schema as it's used for get-schema)
// - In case GDS is not installed a flag is set in the server and tools will be registered accordingly
func (s *Neo4jMCPServer) verifyRequirements(ctx context.Context) error {
	err := s.dbService.VerifyConnectivity(ctx)
	if err != nil {
		return err
	}

	// Check for apoc.meta.schema procedure
	checkApocMetaSchemaQuery := "SHOW PROCEDURES YIELD name WHERE name = 'apoc.meta.schema' RETURN count(name) > 0 AS apocMetaSchemaAvailable"

	// Check for apoc.meta.schema availability
	records, err := s.dbService.ExecuteReadQuery(ctx, checkApocMetaSchemaQuery, nil)
	if err != nil {
		return fmt.Errorf("failed to check for APOC availability: %w", err)
	}
	if len(records) != 1 || len(records[0].Values) != 1 {
		return fmt.Errorf("failed to verify APOC availability: unexpected response from test query")
	}
	apocMetaSchemaAvailable, ok := records[0].Values[0].(bool)
	if !ok || !apocMetaSchemaAvailable {
		return fmt.Errorf("please ensure the APOC plugin is installed and includes the 'meta' component")
	}
	// Call gds.version procedure to determine if GDS is installed
	records, err = s.dbService.ExecuteReadQuery(ctx, "RETURN gds.version() as gdsVersion", nil)
	if err != nil {
		// GDS is optional, so we log a warning and continue, assuming it's not installed.
		log.Print("Impossible to verify GDS installation.")
		s.gdsInstalled = false
	} else if len(records) == 1 && len(records[0].Values) == 1 {
		_, ok := records[0].Values[0].(string)
		if ok {
			s.gdsInstalled = true
		}
	}

	// Check for vector indexes to enable the vector-search tool
	vectorRecords, err := s.dbService.ExecuteReadQuery(ctx,
		"SHOW INDEXES YIELD type WHERE type = 'VECTOR' RETURN count(*) AS count", nil)
	if err != nil {
		slog.Warn("failed to check for vector indexes, vector-search tool will be disabled", "error", err)
		s.vectorIndexesFound = false
	} else if len(vectorRecords) == 1 && len(vectorRecords[0].Values) >= 1 {
		if count, ok := toInt64Value(vectorRecords[0].Values[0]); ok && count > 0 {
			s.vectorIndexesFound = true
			slog.Info("vector indexes detected, enabling vector-search tool", "count", count)
		}
	}

	return nil
}

// toInt64Value converts a numeric value to int64, handling the common Neo4j driver types.
func toInt64Value(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

// emitServerStartupEvent emits the server startup event immediately with available info (no DB query)
func (s *Neo4jMCPServer) emitServerStartupEvent() {
	s.anService.EmitEvent(s.anService.NewStartupEvent(s.config.TransportMode, s.config.HTTPTLSEnabled, s.version))
}

// emitConnectionInitializedEvent emits the connection initialized event with DB information (STDIO mode only)
func (s *Neo4jMCPServer) emitConnectionInitializedEvent(ctx context.Context) {
	if !s.anService.IsEnabled() {
		return
	}

	records, err := s.dbService.ExecuteReadQuery(ctx, "CALL dbms.components()", map[string]any{})
	if err != nil {
		slog.Debug("Failed to collect connection metadata", "error", err.Error())
		return
	}

	connInfo := recordsToConnectionEventInfo(records)
	s.anService.EmitEvent(s.anService.NewConnectionInitializedEvent(connInfo))
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

// buildTLSConfig creates a TLS configuration with security best practices
// - Sets minimum TLS version to TLS 1.2 (allows TLS 1.3 negotiation)
// - Uses Go's default cipher suites (well-maintained and secure)
// - Compatible with self-signed and enterprise certificates
func (s *Neo4jMCPServer) buildTLSConfig() (*tls.Config, error) {
	// Load the certificate and key
	cert, err := tls.LoadX509KeyPair(s.config.HTTPTLSCertFile, s.config.HTTPTLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate and key: %w", err)
	}

	// Create TLS config with security best practices
	// MinVersion is set to TLS 1.2, which allows TLS 1.3 clients to negotiate higher versions
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		// CipherSuites: nil (uses Go's default secure cipher suites)
		// PreferServerCipherSuites: deprecated in Go 1.17+ (server preference is always used for TLS 1.3)
	}

	return tlsConfig, nil
}

// Stop gracefully stops the HTTP server
func (s *Neo4jMCPServer) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		slog.Info("Stopping HTTP server...")
		if err := s.httpServer.Shutdown(ctx); err != nil {
			slog.Error("Error shutting down HTTP server", "error", err)
			return err
		}
		// Signal the StartHTTPServer goroutine to exit
		close(s.shutdownChan)
		slog.Info("HTTP server stopped")
	}
	return nil
}

func (s *Neo4jMCPServer) StartHTTPServer() error {
	addr := fmt.Sprintf("%s:%s", s.config.HTTPHost, s.config.HTTPPort)
	protocol := protocolHTTP
	if s.config.HTTPTLSEnabled {
		protocol = protocolHTTPS
	}
	slog.Info("Starting HTTP server", "address", addr, "url", fmt.Sprintf("%s://%s", protocol, addr), "tls", s.config.HTTPTLSEnabled)

	// Create the StreamableHTTPServer - it serves on /mcp path by default
	mcpServerHTTP := server.NewStreamableHTTPServer(
		s.MCPServer,
		server.WithStateLess(true),
	)

	allowedOrigins := parseAllowedOrigins(s.config.HTTPAllowedOrigins)
	// Wrap handler with middleware and create HTTP server
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.chainMiddleware(allowedOrigins, mcpServerHTTP),
		// Timeouts optimized for stateless HTTP MCP requests
		ReadTimeout:       serverHTTPReadTimeout,
		WriteTimeout:      serverHTTPWriteTimeout,
		IdleTimeout:       serverHTTPIdleTimeout,
		ReadHeaderTimeout: serverHTTPReadHeaderTimeout,
	}

	// Configure TLS if enabled
	if s.config.HTTPTLSEnabled {
		tlsConfig, err := s.buildTLSConfig()
		if err != nil {
			return fmt.Errorf("failed to configure TLS: %w", err)
		}
		s.httpServer.TLSConfig = tlsConfig
		slog.Info("TLS configuration applied", "minVersion", "TLS 1.2 (allows TLS 1.3 negotiation)")
	}

	// Signal that httpServer is ready for reading
	close(s.HTTPServerReady)

	// Channel to receive server errors
	errChan := make(chan error, 1)
	go func() {
		var err error

		if s.config.HTTPTLSEnabled {
			// Use empty strings for cert/key files since they're already loaded in TLSConfig
			err = s.httpServer.ListenAndServeTLS("", "")
		} else {
			err = s.httpServer.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server failed: %w", err)
		}
	}()

	// Channel to receive shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Block until we receive a signal, an error, or a shutdown request
	select {
	case sig := <-sigChan:
		slog.Info("Shutdown signal received", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverHTTPShutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("Error during server shutdown", "error", err)
			return err
		}
		close(s.shutdownChan)
		slog.Info("HTTP server stopped gracefully")
		return nil
	case err := <-errChan:
		return err
	case <-s.shutdownChan:
		// Server was stopped via Stop() method
		return nil
	}
}

// configureHooks sets up MCP SDK hooks for tool call tracking
func (s *Neo4jMCPServer) configureHooks() *server.Hooks {
	hooks := &server.Hooks{}

	hooks.AddAfterCallTool(s.handleToolCallComplete)
	if s.config.TransportMode == config.TransportModeHTTP {
		hooks.AddBeforeInitialize(func(ctx context.Context, _ any, _ *mcp.InitializeRequest) {
			// if requirements and events are already verified/sent return
			if s.connectionVerified.Load() {
				return
			}
			// lock
			s.initMu.Lock()
			defer s.initMu.Unlock()

			// cover edge case "connectionVerified" stored in between check and lock
			if s.connectionVerified.Load() {
				return
			}

			slog.Info("Verify server requirements...")
			if err := s.verifyRequirements(ctx); err != nil {
				slog.Error("Error during verification", "error", err)
				return
			}

			if s.gdsInstalled {
				s.addGDSTools()
			}

			if s.vectorIndexesFound {
				s.addVectorTools()
			}

			s.emitConnectionInitializedEvent(ctx)

			s.connectionVerified.Store(true)
		})
	}

	return hooks
}

// handleToolCallComplete is called after every tool call completes.
// The result parameter is typed as `any` to match the OnAfterCallToolFunc signature
// defined by the mcp-go SDK (v0.46.0+).
func (s *Neo4jMCPServer) handleToolCallComplete(_ context.Context, _ any, request *mcp.CallToolRequest, result any) {
	if s.anService == nil || !s.anService.IsEnabled() {
		return
	}

	toolName := request.Params.Name

	// Determine success from the result. The SDK passes the raw result as `any`;
	// type-assert to *mcp.CallToolResult to inspect the IsError field.
	var toolResult *mcp.CallToolResult
	success := true
	if tr, ok := result.(*mcp.CallToolResult); ok {
		toolResult = tr
		success = !tr.IsError
	}

	// Build vector info based on tool type
	var vectorInfo *analytics.ToolVectorInfo
	switch toolName {
	case "get-schema":
		vectorInfo = extractSchemaVectorInfo(toolResult)
	case "read-cypher", "write-cypher":
		vectorInfo = extractCypherVectorInfo(request)
	case "vector-search":
		vectorSearchTrue := true
		vectorInfo = &analytics.ToolVectorInfo{
			VectorSearch: &vectorSearchTrue,
		}
	}

	// Emit tool event (connection info sent separately in CONNECTION_INITIALIZED event)
	s.anService.EmitEvent(s.anService.NewToolEvent(toolName, success, vectorInfo))

	// Handle GDS events for cypher tools
	if toolName == "read-cypher" || toolName == "write-cypher" {
		s.emitGDSEventsIfNeeded(request)
	}
}

// extractSchemaVectorInfo parses the get-schema result to count VECTOR indexes.
// Returns nil if the result cannot be parsed (graceful degradation — analytics
// should never break tool execution).
func extractSchemaVectorInfo(result *mcp.CallToolResult) *analytics.ToolVectorInfo {
	if result == nil || result.IsError || len(result.Content) == 0 {
		return nil
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return nil
	}

	// Minimal struct to extract just the indexes type field
	var schema struct {
		Indexes []struct {
			Type string `json:"type"`
		} `json:"indexes"`
	}
	if err := json.Unmarshal([]byte(textContent.Text), &schema); err != nil {
		slog.Debug("failed to parse get-schema result for vector analytics", "error", err)
		return nil
	}

	vectorCount := 0
	fulltextCount := 0
	for _, idx := range schema.Indexes {
		if idx.Type == "VECTOR" {
			vectorCount++
		}
		if idx.Type == "FULLTEXT" {
			fulltextCount++
		}
	}

	return &analytics.ToolVectorInfo{
		VectorIndexCount:   &vectorCount,
		FullTextIndexCount: &fulltextCount,
	}
}

// extractCypherVectorInfo inspects a Cypher query to detect vector search, vector property set,
// and full-text search operations. Detection is based on well-known procedure names and Cypher patterns.
func extractCypherVectorInfo(request *mcp.CallToolRequest) *analytics.ToolVectorInfo {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return nil
	}
	queryRaw, ok := args["query"]
	if !ok {
		return nil
	}
	queryStr, ok := queryRaw.(string)
	if !ok {
		return nil
	}

	lowerQuery := strings.ToLower(queryStr)

	// Detect vector search: db.index.vector.queryNodes / db.index.vector.queryRelationships
	vectorSearch := strings.Contains(lowerQuery, "db.index.vector.query")

	// Detect vector property set: db.create.setNodeVectorProperty / db.create.setRelationshipVectorProperty
	vectorPropertySet := strings.Contains(lowerQuery, "db.create.setnodevectorproperty") ||
		strings.Contains(lowerQuery, "db.create.setrelationshipvectorproperty")

	// Detect full-text search: db.index.fulltext.queryNodes / db.index.fulltext.queryRelationships
	fullTextSearch := strings.Contains(lowerQuery, "db.index.fulltext.querynodes") ||
		strings.Contains(lowerQuery, "db.index.fulltext.queryrelationships")

	// Only return info if at least one operation was detected
	if !vectorSearch && !vectorPropertySet && !fullTextSearch {
		return nil
	}

	return &analytics.ToolVectorInfo{
		VectorSearch:      &vectorSearch,
		VectorPropertySet: &vectorPropertySet,
		FullTextSearch:    &fullTextSearch,
	}
}

// emitGDSEventsIfNeeded checks if the cypher query contains GDS calls and emits appropriate events
func (s *Neo4jMCPServer) emitGDSEventsIfNeeded(request *mcp.CallToolRequest) {
	// Type assert Arguments to map[string]any
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return
	}

	// Extract query from arguments
	queryRaw, ok := args["query"]
	if !ok {
		return
	}

	queryStr, ok := queryRaw.(string)
	if !ok {
		return
	}

	lowerQuery := strings.ToLower(queryStr)
	if strings.Contains(lowerQuery, "call gds.graph.project") {
		s.anService.EmitEvent(s.anService.NewGDSProjCreatedEvent())
	}
	if strings.Contains(lowerQuery, "call gds.graph.drop") {
		s.anService.EmitEvent(s.anService.NewGDSProjDropEvent())
	}
}
