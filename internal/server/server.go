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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"

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
	mcpServerInstruction        = "This experimental MCP server allows interaction " +
		"with your Neo4j database. Start by calling get-schema to understand " +
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
	mcpServer          *mcpsdk.Server
	httpServer         *http.Server
	HTTPServerReady    chan struct{}
	shutdownChan       chan struct{}
	config             *config.Config
	dbService          database.Service
	version            string
	anService          analytics.Service
	gdsInstalled       bool
	initMu             sync.Mutex
	connectionVerified atomic.Bool
}

// NewNeo4jMCPServer creates a new MCP server instance
// The config parameter is expected to be already validated
func NewNeo4jMCPServer(version string, cfg *config.Config, dbService database.Service, anService analytics.Service) *Neo4jMCPServer {

	neo4jServer := &Neo4jMCPServer{
		HTTPServerReady: make(chan struct{}),
		shutdownChan:    make(chan struct{}),
		config:          cfg,
		dbService:       dbService,
		version:         version,
		anService:       anService,
		gdsInstalled:    false,
	}

	neo4jServer.mcpServer = mcpsdk.NewServer(
		"neo4j-mcp",
		version,
		mcpsdk.WithInstructions(mcpServerInstruction),
	)

	neo4jServer.configureHooks()

	return neo4jServer
}

// ListTools returns every tool currently registered on the underlying MCP
// server. Exists so external test code doesn't need direct access to the
// wrapped mcpsdk.Server.
func (s *Neo4jMCPServer) ListTools() []mcpsdk.Tool {
	return s.mcpServer.ListTools()
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

			return s.mcpServer.ServeStdio(context.Background())
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

// parseCommaList splits a comma-separated string into trimmed, non-empty
// entries. An empty input yields an empty (non-nil) slice. Used for both the
// static tool-selection config fields and their HTTP header counterparts.
func parseCommaList(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// verifyRequirements check the Neo4j requirements:
// - A valid connection with a Neo4j instance.
// - The ability to perform a read query (database name is correctly defined).
// - In case GDS is not installed a flag is set in the server and tools will be registered accordingly
func (s *Neo4jMCPServer) verifyRequirements(ctx context.Context) error {
	err := s.dbService.VerifyConnectivity(ctx)
	if err != nil {
		return err
	}

	// Call gds.version procedure to determine if GDS is installed
	records, err := s.dbService.ExecuteReadQuery(ctx, "RETURN gds.version() as gdsVersion", nil)
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

	return nil
}

// emitServerStartupEvent emits the server startup event immediately with available info (no DB query)
func (s *Neo4jMCPServer) emitServerStartupEvent() {
	// DetectMode is a pure scheme check (no network access), so it's safe to
	// recompute here rather than threading the mode through the constructor.
	// A malformed URI would already have failed at connection-setup time in
	// main.go before the server ever got this far, so the error here is
	// ignored — ModeBolt is DetectMode's safe zero-value fallback.
	connMode, _ := queryapi.DetectMode(s.config.URI)
	s.anService.EmitEvent(s.anService.NewStartupEvent(s.config.TransportMode, s.config.HTTPTLSEnabled, s.version, connMode.String()))
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

	// Create the streamable-HTTP handler. Path routing (mounting it at /mcp)
	// is handled by pathValidationMiddleware in the middleware chain below.
	mcpServerHTTP := s.mcpServer.HTTPHandler(mcpsdk.HTTPOptions{Stateless: true})

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
func (s *Neo4jMCPServer) configureHooks() {
	s.mcpServer.OnAfterCallTool(s.handleToolCallComplete)
	if s.config.TransportMode == config.TransportModeHTTP {
		// The MCP client negotiates either the classic "initialize" handshake or,
		// when both client and server support it, the newer stateless "discover"
		// handshake (protocol version 2026-07-28+) — the client picks whichever
		// succeeds, so the server must run first-request verification on both.
		s.mcpServer.OnBeforeInitialize(func(ctx context.Context) {
			s.verifyOnFirstRequest(ctx)
		})
		s.mcpServer.OnBeforeDiscover(func(ctx context.Context) {
			s.verifyOnFirstRequest(ctx)
		})
		s.mcpServer.SetToolAccessFilter(s.toolAccessFilter)
	}
}

// toolAccessFilter implements mcpsdk.ToolAccessFunc: it reads the per-request
// tool selection (populated by toolSelectionMiddleware from the
// HTTPToolsHeaderName/HTTPToolCategoriesHeaderName headers) and, if either
// was set, resolves it to the concrete set of tool names allowed for this
// request. Because it only ever selects from tools already registered on
// the server (narrowed by ReadOnly/GDS-availability/EnabledTools/
// EnabledToolCategories), a request can only narrow the visible tool set
// further, never widen it.
func (s *Neo4jMCPServer) toolAccessFilter(ctx context.Context) (map[string]bool, bool) {
	names, categories, ok := auth.GetToolSelection(ctx)
	if !ok || (len(names) == 0 && len(categories) == 0) {
		return nil, false
	}

	deps := s.buildToolDependencies()
	allowed := make(map[string]bool)
	for _, d := range s.getAllToolsDefs(deps) {
		if slices.Contains(names, d.definition.Tool.Name) || slices.Contains(categories, string(d.Category)) {
			allowed[d.definition.Tool.Name] = true
		}
	}
	return allowed, true
}

// verifyOnFirstRequest runs verifyRequirements, conditionally registers GDS
// tools, and emits the connection-initialized event exactly once, on the
// first request handled in HTTP mode (initialize or discover).
func (s *Neo4jMCPServer) verifyOnFirstRequest(ctx context.Context) {
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

	s.emitConnectionInitializedEvent(ctx)

	s.connectionVerified.Store(true)
}

// handleToolCallComplete is called after every tool call completes.
func (s *Neo4jMCPServer) handleToolCallComplete(_ context.Context, request *mcpsdk.CallToolRequest, result *mcpsdk.CallToolResult) {
	if s.anService == nil || !s.anService.IsEnabled() {
		return
	}

	toolName := request.Params.Name

	success := true
	if result != nil {
		success = !result.IsError
	}

	// Build vector info based on tool type
	var vectorInfo *analytics.ToolVectorInfo
	switch toolName {
	case "get-schema":
		vectorInfo = extractSchemaVectorInfo(result)
	case "read-cypher", "write-cypher":
		vectorInfo = extractCypherVectorInfo(request)
	case "vector-search":
		vectorSearchTrue := true
		vectorInfo = &analytics.ToolVectorInfo{
			VectorSearch: &vectorSearchTrue,
		}
	}

	// Emit tool event (connection info sent separately in CONNECTION_INITIALIZED event)
	s.anService.EmitEvent(s.anService.NewToolEvent(toolName, success, vectorInfo, s.config.OutputFormat))

	// Handle GDS events for cypher tools
	if toolName == "read-cypher" || toolName == "write-cypher" {
		s.emitGDSEventsIfNeeded(request)
	}
}

// extractSchemaVectorInfo parses the get-schema result to count VECTOR indexes.
// Returns nil if the result cannot be parsed (graceful degradation — analytics
// should never break tool execution).
func extractSchemaVectorInfo(result *mcpsdk.CallToolResult) *analytics.ToolVectorInfo {
	if result == nil || result.IsError || len(result.Content) == 0 {
		return nil
	}
	textContent, ok := mcpsdk.AsTextContent(result.Content[0])
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
func extractCypherVectorInfo(request *mcpsdk.CallToolRequest) *analytics.ToolVectorInfo {
	queryRaw, ok := request.Params.Arguments["query"]
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
func (s *Neo4jMCPServer) emitGDSEventsIfNeeded(request *mcpsdk.CallToolRequest) {
	queryRaw, ok := request.Params.Arguments["query"]
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
