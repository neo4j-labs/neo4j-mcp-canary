// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

const (
	corsMaxAgeSeconds           = "86400" // 24 hours
	maxUnauthenticatedBodyBytes = 4 * 1024
)

var errRequestBodyTooLarge = errors.New("request body too large")

// chainMiddleware chains together all HTTP middleware for this server instance
func (s *Neo4jMCPServer) chainMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	if s == nil || s.config == nil {
		panic("chainMiddleware: server or config is nil")
	}

	// Chain middleware in reverse order (last added = first to execute)
	// Execution order: PathValidator -> CORS -> Auth (Bearer/Basic) -> Logging -> Handler

	// Start with the actual handler
	handler := next

	// Add logging middleware
	handler = loggingMiddleware()(handler)

	var unauthMethods []string
	if s.config.AllowUnauthenticatedPing {
		unauthMethods = append(unauthMethods, "ping")
	}
	if s.config.AllowUnauthenticatedToolsList {
		unauthMethods = append(unauthMethods, "tools/list")
	}
	if s.config.AllowUnauthenticatedInitialize {
		unauthMethods = append(unauthMethods, "initialize")
	}
	if s.config.AllowUnauthenticatedNotificationsInitialize {
		unauthMethods = append(unauthMethods, "notifications/initialized")
	}

	handler = authMiddleware(s.config.AuthHeaderName, unauthMethods, s.anService)(handler)

	// Add per-request tool-selection middleware (reads the tools/categories
	// headers, if present, into the request context for the server's
	// ToolAccessFilter to consume).
	handler = toolSelectionMiddleware(s.config.HTTPToolsHeaderName, s.config.HTTPToolCategoriesHeaderName)(handler)

	// Attach this server's top-level embedding provider config (if any) so
	// set-vector-property's text field / check-embedding-dimensions work in
	// single-instance HTTP mode too, not just multi-instance.
	handler = embeddingConfigMiddleware(s.embeddingConfig)(handler)

	// Add CORS middleware (if configured) - includes Mcp-Session-Id in allowed headers
	handler = corsMiddleware(allowedOrigins, s.config.AuthHeaderName, s.config.HTTPToolsHeaderName, s.config.HTTPToolCategoriesHeaderName)(handler)

	// Add path validation middleware last (executes first - reject non-/mcp paths quickly)
	handler = pathValidationMiddleware()(handler)

	return handler
}

// chainMiddlewareForInstance builds the middleware chain for one
// multi-instance route ("/<name>/mcp" — see buildMultiInstanceHandler in
// server.go). It mirrors chainMiddleware's CORS/tool-selection/logging
// wrapping, but swaps the generic authMiddleware for instanceAuthMiddleware
// (which branches on this specific instance's auth.Type) and needs no
// pathValidationMiddleware — the mux already scoped this handler to exactly
// this instance's path, so an unconfigured path segment 404s before ever
// reaching a middleware chain at all.
func (s *Neo4jMCPServer) chainMiddlewareForInstance(inst config.NeoInstance, allowedOrigins []string, next http.Handler) http.Handler {
	handler := next
	handler = loggingMiddleware()(handler)
	handler = instanceAuthMiddleware(inst.Name, inst.Auth, inst.Embedding, s.config.HTTPAPIKeyHeaderName, s.scheme(), s.bearerVerifier)(handler)
	handler = toolSelectionMiddleware(s.config.HTTPToolsHeaderName, s.config.HTTPToolCategoriesHeaderName)(handler)
	handler = corsMiddleware(allowedOrigins, s.config.AuthHeaderName, s.config.HTTPToolsHeaderName, s.config.HTTPToolCategoriesHeaderName)(handler)
	return handler
}

// scheme reports the URL scheme this server is externally reachable on,
// for building absolute URLs (RFC 9728 resource/metadata URLs) from a
// request's Host header.
func (s *Neo4jMCPServer) scheme() string {
	if s.config.HTTPTLSEnabled {
		return protocolHTTPS
	}
	return protocolHTTP
}

// BearerVerifyFunc verifies a client-presented Bearer token for a
// bearer-type multi-instance route against that instance's configured
// identity provider (issuer/jwks_uri/audience — see config.InstanceAuth). A
// non-nil error means the token is missing, malformed, expired, or doesn't
// match the configured issuer/audience/signature.
type BearerVerifyFunc func(ctx context.Context, instanceAuth config.InstanceAuth, token string) error

// instanceAuthMiddleware enforces authentication for one multi-instance
// route, branching on that instance's auth.Type:
//   - basic: the client must present a matching key in apiKeyHeaderName —
//     the instance's actual Neo4j credentials are the static service
//     account already baked into its driver (see cmd/neo4j-mcp/main.go),
//     never anything the client sends.
//   - basic_passthrough: the client's own HTTP Basic credentials are
//     required and forwarded to Neo4j as-is, exactly like today's
//     single-instance HTTP mode.
//   - bearer: the client's Bearer token is verified via verifyBearer before
//     being forwarded to Neo4j. If verifyBearer is nil (bearer verification
//     not yet wired up — see Neo4jMCPServer.SetBearerVerifier), every
//     request to a bearer-type instance is rejected with 501 rather than
//     silently skipping verification.
//
// Unlike authMiddleware, this has no unauthenticated-method allowlist:
// multi-instance mode exists specifically to require the client to
// authenticate to this server (closing the gap a static per-instance
// service account would otherwise leave), so every request on every
// instance route needs valid auth, with no ping/tools-list exception.
func instanceAuthMiddleware(instanceName string, instAuth config.InstanceAuth, embedding *config.EmbeddingConfig, apiKeyHeaderName, scheme string, verifyBearer BearerVerifyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithInstanceSelection(r.Context(), instanceName)
			if embedding != nil {
				ctx = auth.WithEmbeddingConfig(ctx, embedding)
			}

			switch instAuth.Type {
			case config.InstanceAuthBasic:
				presented := r.Header.Get(apiKeyHeaderName)
				if presented == "" || !matchesAnyAPIKey(presented, instAuth.APIKeys) {
					http.Error(w, "Unauthorized: invalid or missing API key", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r.WithContext(ctx))

			case config.InstanceAuthBasicPassthrough:
				user, pass, ok := r.BasicAuth()
				if !ok || user == "" || pass == "" {
					w.Header().Set("WWW-Authenticate", `Basic realm="Neo4j MCP Server"`)
					http.Error(w, "Unauthorized: Basic authentication required", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r.WithContext(auth.WithBasicAuth(ctx, user, pass)))

			case config.InstanceAuthBearer:
				// resource_metadata (RFC 9728 §5.1) tells a spec-compliant MCP
				// client where to fetch this instance's own protected-resource
				// metadata document — naming the identity provider it should
				// log in with — before it ever presents a token here.
				meta := metadataURL(scheme, r, instanceName)

				token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				token = strings.TrimSpace(token)
				if !found || token == "" {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, meta))
					http.Error(w, "Unauthorized: Bearer token required", http.StatusUnauthorized)
					return
				}
				if verifyBearer == nil {
					http.Error(w, "Bearer verification is not yet configured for this instance", http.StatusNotImplemented)
					return
				}
				if err := verifyBearer(r.Context(), instAuth, token); err != nil {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_token", resource_metadata=%q`, meta))
					http.Error(w, "Unauthorized: invalid bearer token", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r.WithContext(auth.WithBearerToken(ctx, token)))

			default:
				// Unreachable in practice: config.ValidateInstances rejects any
				// other Type before the server ever starts.
				http.Error(w, "Internal Server Error: unknown instance auth type", http.StatusInternalServerError)
			}
		})
	}
}

// protectedResourceMetadataPath is the RFC 9728 well-known path for one
// instance's own protected-resource metadata document: the well-known
// segment inserted before the resource's own path
// ("/<name>/mcp" -> "/.well-known/oauth-protected-resource/<name>/mcp").
// Because instance selection is part of the URL path rather than a shared
// header, each bearer-type instance gets its own metadata document naming
// exactly its own issuer — a header-based design would have had to name
// every configured issuer in one shared document instead.
func protectedResourceMetadataPath(instanceName string) string {
	return "/.well-known/oauth-protected-resource/" + instanceName + "/mcp"
}

// resourceURL and metadataURL build absolute URLs from the incoming
// request's Host header (rather than from a startup-computed HTTPHost/
// HTTPPort) so they resolve correctly behind a reverse proxy or load
// balancer that terminates a different host/port than this process binds
// to. scheme reflects this server's own TLS configuration (see
// Neo4jMCPServer.scheme), not any proxy-forwarded scheme.
func resourceURL(scheme string, r *http.Request, instanceName string) string {
	return fmt.Sprintf("%s://%s/%s/mcp", scheme, r.Host, instanceName)
}

func metadataURL(scheme string, r *http.Request, instanceName string) string {
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, protectedResourceMetadataPath(instanceName))
}

// protectedResourceMetadataDocument is the RFC 9728 §3.1 document shape,
// scoped to exactly what a bearer-type instance needs to advertise: this
// server's own resource identity and the single identity provider it
// trusts for that instance.
type protectedResourceMetadataDocument struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// protectedResourceMetadataHandler serves the metadata document for one
// bearer-type instance, so a spec-compliant MCP client can discover which
// identity provider to log in with (per the MCP Authorization spec) before
// it ever presents a token to this server.
func protectedResourceMetadataHandler(scheme, instanceName, issuer string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc := protectedResourceMetadataDocument{
			Resource:             resourceURL(scheme, r, instanceName),
			AuthorizationServers: []string{issuer},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
}

// matchesAnyAPIKey reports whether presented equals any of configured,
// comparing in constant time so a mismatching key doesn't leak timing
// information about how many leading bytes matched.
func matchesAnyAPIKey(presented string, configured []string) bool {
	for _, k := range configured {
		if subtle.ConstantTimeCompare([]byte(presented), []byte(k)) == 1 {
			return true
		}
	}
	return false
}

// authMiddleware enforces HTTP authentication (Bearer token or Basic Auth) for all requests in HTTP mode.
// Tries Bearer token first (from Authorization: Bearer header), then falls back to Basic Auth.
// Credentials are extracted and stored in the request context for tools to create
// per-request Neo4j driver connections, enabling multi-tenant scenarios.
// unauthenticatedMethods is an optional list of JSON-RPC method names (e.g. "ping", "tools/list")
// that are permitted without credentials.
// Returns 401 Unauthorized if credentials are missing or malformed.
func authMiddleware(headerName string, unauthenticatedMethods []string, as analytics.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			if !strings.EqualFold(headerName, "Authorization") {
				val := r.Header.Get(headerName)
				if val != "" {
					r.Header.Set("Authorization", val)
				}
			}

			authHeader := r.Header.Get("Authorization")

			// Try the bearer token first
			if token, found := strings.CutPrefix(authHeader, "Bearer "); found {
				token = strings.TrimSpace(token)

				if token == "" {
					w.Header().Set("WWW-Authenticate", `Bearer realm="Neo4j MCP Server"`)
					http.Error(w, "Unauthorized: Bearer token is empty", http.StatusUnauthorized)
					return
				}

				// Bearer token provided - store in context
				ctx := auth.WithBearerToken(r.Context(), token)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Fall back to basic auth
			user, pass, ok := r.BasicAuth()
			if !ok {
				if len(unauthenticatedMethods) > 0 {
					// Wrap the body once to enforce a size limit for unauthenticated probes.
					r.Body = http.MaxBytesReader(w, r.Body, maxUnauthenticatedBodyBytes)

					for _, method := range unauthenticatedMethods {
						ok, err := isUnauthenticatedMethodRequest(r, method)
						if err != nil {
							if errors.Is(err, errRequestBodyTooLarge) {
								http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
								return
							}
							// For other read errors or JSON errors, fall through and require auth
							continue
						}
						if ok {
							slog.Info("Unauthenticated method", "method", method)
							as.EmitEvent(as.NewUnauthenticatedJSONRPCEvent(method))
							next.ServeHTTP(w, r)
							return
						}
					}
				}

				w.Header().Add("WWW-Authenticate", `Basic realm="Neo4j MCP Server"`)
				w.Header().Add("WWW-Authenticate", `Bearer realm="Neo4j MCP Server"`)
				http.Error(w, "Unauthorized: Basic or Bearer authentication required", http.StatusUnauthorized)
				return
			}

			// Validate credentials are not empty (consistent with bearer token validation)
			if user == "" || pass == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="Neo4j MCP Server"`)
				http.Error(w, "Unauthorized: Username and password cannot be empty", http.StatusUnauthorized)
				return
			}

			// Basic auth credentials provided - store in context
			ctx := auth.WithBasicAuth(r.Context(), user, pass)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// toolSelectionMiddleware reads the tool-name and tool-category selection
// headers (if present) and stores the parsed values in the request context
// via auth.WithToolSelection, for the server's mcpsdk.ToolAccessFilter to
// consume later in the request lifecycle. It never rejects a request —
// absent or empty headers simply mean no per-request restriction.
func toolSelectionMiddleware(namesHeader, categoriesHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			names := parseCommaList(r.Header.Get(namesHeader))
			categories := parseCommaList(r.Header.Get(categoriesHeader))
			ctx := auth.WithToolSelection(r.Context(), names, categories)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// embeddingConfigMiddleware attaches this single-instance HTTP server's
// top-level GenAI embedding provider config (Neo4jMCPServer.embeddingConfig
// — computed once from Config.EmbeddingProvider/EmbeddingConfiguration,
// never per-request) to every request's context, the single-instance
// equivalent of multi-instance mode's per-route instanceAuthMiddleware
// wiring. A nil embConf (no provider configured) makes this a no-op
// passthrough.
func embeddingConfigMiddleware(embConf *config.EmbeddingConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if embConf == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithEmbeddingConfig(r.Context(), embConf)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// corsMiddleware implements CORS (Cross-Origin Resource Sharing)
// If allowedOrigins is empty, CORS is disabled
// If allowedOrigins is "*", all origins are allowed
// Otherwise, allowedOrigins should be a comma-separated list of allowed origins
func corsMiddleware(allowedOrigins []string, authHeaderName string, toolSelectionHeaderNames ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip CORS if not configured
			if len(allowedOrigins) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")

			// Handle wildcard case
			if slices.Contains(allowedOrigins, "*") {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && slices.Contains(allowedOrigins, origin) {
				// Check if the request origin is allowed
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			// Build allowed headers list, always include Content-Type and Authorization.
			allowedHeaders := []string{"Content-Type", "Authorization"}
			// If a custom auth header is configured, and it's not the default, include it
			if authHeaderName != "" && !strings.EqualFold(authHeaderName, "Authorization") {
				allowedHeaders = append(allowedHeaders, authHeaderName)
			}
			// Include the tool-selection headers so browser-based clients can send them.
			for _, h := range toolSelectionHeaderNames {
				if h != "" {
					allowedHeaders = append(allowedHeaders, h)
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
			w.Header().Set("Access-Control-Max-Age", corsMaxAgeSeconds)

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// pathValidationMiddleware validates that requests are only sent to /mcp path
// Returns 404 for all other paths to avoid hanging connections
func pathValidationMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only /mcp path is valid for this MCP server
			if r.URL.Path != "/mcp" && r.URL.Path != "/mcp/" {
				http.Error(w, "Not Found: This server only handles requests to /mcp", http.StatusNotFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// loggingMiddleware logs HTTP requests for debugging
func loggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			slog.Debug("HTTP Request", // #nosec G706 -- logging HTTP request metadata, no user input in format string
				"method", r.Method,
				"url", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"content_length", r.ContentLength,
				"host", r.Host,
				"query", r.URL.RawQuery,
			)

			// Call the next handler
			next.ServeHTTP(w, r)
		})
	}
}

// isUnauthenticatedMethodRequest reads the JSON-RPC body and returns true if
// the request is a POST whose "method" field matches the given jsonRPCMethod.
// The body is always restored so downstream handlers can read it normally.
// Caller must have already wrapped r.Body with http.MaxBytesReader.
func isUnauthenticatedMethodRequest(r *http.Request, jsonRPCMethod string) (bool, error) {
	if r.Method != http.MethodPost {
		return false, nil
	}
	if r.ContentLength >= 0 && r.ContentLength > maxUnauthenticatedBodyBytes {
		return false, errRequestBodyTooLarge
	}

	buf, err := io.ReadAll(r.Body)
	// Close the original body to free resources.
	if rc := r.Body; rc != nil {
		_ = rc.Close()
	}

	if err != nil {
		// Replace body with an empty reader to avoid further reads.
		r.Body = io.NopCloser(bytes.NewReader(nil))

		// If MaxBytesReader triggered, it typically returns an error containing
		// "request body too large". Map that to a sentinel error so middleware can
		// respond with 413.
		if strings.Contains(err.Error(), "request body too large") {
			return false, errRequestBodyTooLarge
		}

		return false, err
	}

	// Restore the read bytes so downstream handlers can read the body as usual.
	r.Body = io.NopCloser(bytes.NewReader(buf))

	var probe struct {
		Method string `json:"method"`
	}
	if e := json.Unmarshal(buf, &probe); e != nil {
		return false, e
	}

	return probe.Method == jsonRPCMethod, nil
}
