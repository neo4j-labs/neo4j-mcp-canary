// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"context"
	"crypto/tls"
	"fmt"
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
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/eventing"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/readiness"
)

const (
	protocolHTTP                = "http"
	protocolHTTPS               = "https"
	serverHTTPShutdownTimeout   = 65 * time.Second  // Timeout for graceful shutdown (must exceed WriteTimeout to allow active requests to complete)
	serverHTTPReadHeaderTimeout = 5 * time.Second   // SECURITY: Maximum time to read request headers (prevents Slowloris attacks)
	serverHTTPReadTimeout       = 15 * time.Second  // SECURITY: Maximum time to read entire request including body (prevents slow-read attacks)
	serverHTTPWriteTimeout      = 60 * time.Second  // FUNCTIONALITY: Maximum time to write response (allows complex Neo4j queries and large result sets)
	serverHTTPIdleTimeout       = 120 * time.Second // PERFORMANCE: Maximum time to keep idle keep-alive connections open (improves connection reuse)
	mcpServerInstruction        = "This experimental MCP server lets you interact with a Neo4j database. " +
		"Start by calling get-schema to see the node labels, relationship types, and " +
		"properties in the graph (requires APOC). Use read-cypher for queries and " +
		"write-cypher for mutations — full-text and vector indexes aren't listed by " +
		"get-schema, but can still be queried directly via db.index.fulltext.queryNodes()/" +
		"queryRelationships() and db.index.vector.queryNodes()/queryRelationships(). " +
		"Other tools may be available depending on server configuration and installed " +
		"plugins (e.g. graph analytics) — check the current tool list rather than " +
		"assuming a fixed set."
)

// Neo4jMCPServer represents the MCP server instance
type Neo4jMCPServer struct {
	mcpServer              *mcpsdk.Server
	httpServer             *http.Server
	HTTPServerReady        chan struct{}
	shutdownChan           chan struct{}
	config                 *config.Config
	dbService              database.Service
	version                string
	anService              analytics.Service
	events                 *eventing.Emitter
	gdsInstalled           bool
	searchVersionSupported bool
	initMu                 sync.Mutex
	connectionVerified     atomic.Bool
	// bearerVerifier verifies a client-presented Bearer token for a
	// bearer-type multi-instance route (issuer/audience/signature/expiry
	// against that instance's configured JWKS). Set via SetBearerVerifier
	// once the caller has one to offer; nil means bearer-type instances
	// reject every request with 501, which is the case until that wiring
	// lands.
	bearerVerifier BearerVerifyFunc
}

// SetBearerVerifier installs the verifier multi-instance bearer-type routes
// use to validate an incoming client token before forwarding it to Neo4j.
// Must be called before Start if any configured instance has auth.type
// "bearer" — see the bearerVerifier field.
func (s *Neo4jMCPServer) SetBearerVerifier(verifier BearerVerifyFunc) {
	s.bearerVerifier = verifier
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
		events:          eventing.NewEmitter(anService, dbService, cfg, version),
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

		s.events.EmitServerStartup()

		return s.StartHTTPServer()
	case config.TransportModeStdio:
		{
			result, err := readiness.NewChecker(s.dbService, s.config.URI).Verify(context.Background())
			if err != nil {
				return err
			}
			s.gdsInstalled = result.GDSInstalled
			s.searchVersionSupported = result.SearchVersionSupported

			// Register tools
			if err := s.registerTools(); err != nil {
				return fmt.Errorf("failed to register tools: %w", err)
			}

			s.events.EmitServerStartup()
			s.events.EmitConnectionInitialized(context.Background())

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

	// Create the streamable-HTTP handler. It's shared across every route below
	// (single-instance or multi-instance): the MCP tool set is identical either
	// way, only the Neo4j instance a call ends up hitting differs, and that's
	// resolved from context (auth.GetInstanceSelection) inside the handler chain,
	// not by handing the SDK a different tool registration per instance.
	mcpServerHTTP := s.mcpServer.HTTPHandler(mcpsdk.HTTPOptions{Stateless: true})
	allowedOrigins := parseAllowedOrigins(s.config.HTTPAllowedOrigins)

	var handler http.Handler
	if len(s.config.Instances) > 0 {
		handler = s.buildMultiInstanceHandler(allowedOrigins, mcpServerHTTP)
	} else {
		// Path routing (mounting it at /mcp) is handled by
		// pathValidationMiddleware in the middleware chain below.
		handler = s.chainMiddleware(allowedOrigins, mcpServerHTTP)
	}

	// Wrap handler with middleware and create HTTP server
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: handler,
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

// buildMultiInstanceHandler registers one route per configured Neo4j
// instance ("/<name>/mcp") on a shared mux, rather than the single fixed
// "/mcp" registration single-instance mode uses. Each route gets its own
// middleware chain (chainMiddlewareForInstance) that stamps that instance's
// name into context and enforces that instance's own auth.Type before
// reaching the shared SDK handler. An unconfigured path segment is simply
// unmatched by the mux and 404s for free — instance names are already
// validated as safe, unambiguous URL path segments in
// config.ValidateInstances.
func (s *Neo4jMCPServer) buildMultiInstanceHandler(allowedOrigins []string, mcpServerHTTP http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, inst := range s.config.Instances {
		routeHandler := s.chainMiddlewareForInstance(inst, allowedOrigins, mcpServerHTTP)
		mux.Handle("/"+inst.Name+"/mcp", routeHandler)
		mux.Handle("/"+inst.Name+"/mcp/", routeHandler)

		// Only bearer-type instances have an identity provider to advertise —
		// basic/basic_passthrough instances have no RFC 9728 story at all.
		if inst.Auth.Type == config.InstanceAuthBearer {
			mux.Handle(protectedResourceMetadataPath(inst.Name), protectedResourceMetadataHandler(s.scheme(), inst.Name, inst.Auth.Issuer))
		}
	}
	return mux
}

// configureHooks sets up MCP SDK hooks for tool call tracking
func (s *Neo4jMCPServer) configureHooks() {
	s.mcpServer.OnAfterCallTool(s.events.OnToolCallComplete)
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

// verifyOnFirstRequest runs the readiness check, conditionally registers GDS
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
	result, err := readiness.NewChecker(s.dbService, s.config.URI).Verify(ctx)
	if err != nil {
		slog.Error("Error during verification", "error", err)
		return
	}
	s.gdsInstalled = result.GDSInstalled
	s.searchVersionSupported = result.SearchVersionSupported

	if s.gdsInstalled || s.searchVersionSupported {
		s.reregisterOptionalTools()
	}

	s.events.EmitConnectionInitialized(ctx)

	s.connectionVerified.Store(true)
}
