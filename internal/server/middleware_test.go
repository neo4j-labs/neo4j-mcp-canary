// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	analytics_mocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db_mocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"go.uber.org/mock/gomock"
)

// mockHandler is a simple handler that returns 200 OK
func mockHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
}

// mockNeo4jMCPServer creates a mock Neo4jMCPServer for testing
func mockNeo4jMCPServer(t *testing.T) *Neo4jMCPServer {
	t.Helper()
	ctrl := gomock.NewController(t)

	cfg := &config.Config{
		URI:           "bolt://localhost:7687",
		Username:      "neo4j",
		Password:      "password",
		Database:      "neo4j",
		TransportMode: config.TransportModeHTTP,
		Telemetry:     false, // Disable telemetry in tests
	}

	mockDBService := db_mocks.NewMockService(ctrl)
	mockAnalyticsService := analytics_mocks.NewMockService(ctrl)

	mcpServer := mcpsdk.NewServer("test-server", "1.0.0")

	return &Neo4jMCPServer{
		mcpServer:    mcpServer,
		config:       cfg,
		dbService:    mockDBService,
		anService:    mockAnalyticsService,
		version:      "1.0.0",
		gdsInstalled: false,
	}
}

// authCheckHandler verifies if credentials are in context
func authCheckHandler(t *testing.T, expectAuth bool, expectedUser, expectedPass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := auth.GetBasicAuthCredentials(r.Context())
		if expectAuth {
			if !ok {
				t.Error("Expected auth credentials in context, but none found")
			}
			if user != expectedUser {
				t.Errorf("Expected user %q, got %q", expectedUser, user)
			}
			if pass != expectedPass {
				t.Errorf("Expected pass %q, got %q", expectedPass, pass)
			}
		} else if ok {
			t.Error("Expected no auth credentials in context, but found some")
		}
		w.WriteHeader(http.StatusOK)
	})
}

// bearerTokenCheckHandler verifies if bearer token is in context
func bearerTokenCheckHandler(t *testing.T, expectToken bool, expectedToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := auth.GetBearerToken(r.Context())
		if expectToken {
			if !ok {
				t.Error("Expected bearer token in context, but none found")
			}
			if token != expectedToken {
				t.Errorf("Expected token %q, got %q", expectedToken, token)
			}
		} else if ok {
			t.Error("Expected no bearer token in context, but found some")
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddleware_WithValidBasicCredentials(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(authCheckHandler(t, true, "testuser", "testpass"))

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("testuser", "testpass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithoutCredentials(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401 when no credentials provided
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}

	// Should have WWW-Authenticate header
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("Expected WWW-Authenticate header to be set")
	}
}

func TestAuthMiddleware_WithEmptyBasicCredentials(t *testing.T) {
	testCases := []struct {
		name     string
		username string
		password string
	}{
		{"both empty", "", ""},
		{"empty username", "", "password"},
		{"empty password", "username", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := authMiddleware("Authorization", nil, nil)(mockHandler())

			req := httptest.NewRequest("GET", "/", nil)
			req.SetBasicAuth(tc.username, tc.password)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			// Empty credentials should be rejected
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got %d", rec.Code)
			}

			// Should have WWW-Authenticate header
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("Expected WWW-Authenticate header to be set")
			}
		})
	}
}

func TestAuthMiddleware_WithValidBearerToken(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(bearerTokenCheckHandler(t, true, "test-token-123"))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithBearerTokenAndExtraSpaces(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(bearerTokenCheckHandler(t, true, "test-token-456"))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer   test-token-456  ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithEmptyBearerToken(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}

	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Error("Expected WWW-Authenticate header to include Bearer")
	}
}

func TestAuthMiddleware_FallbackToBasicAuth(t *testing.T) {
	// When no bearer token, should fall back to basic auth
	handler := authMiddleware("Authorization", nil, nil)(authCheckHandler(t, true, "testuser", "testpass"))

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("testuser", "testpass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithCustomHeaderName(t *testing.T) {
	// Create a mock server with custom auth header name
	mock := mockNeo4jMCPServer(t)
	mock.config.AuthHeaderName = "X-Test-Auth"

	handler := mock.chainMiddleware([]string{}, bearerTokenCheckHandler(t, true, "custom-token-789"))

	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("X-Test-Auth", "Bearer custom-token-789")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_CustomHeaderName_OverridesAuthHeader(t *testing.T) {
	// When both Authorization and custom header are present, custom header should take precedence
	mock := mockNeo4jMCPServer(t)
	mock.config.AuthHeaderName = "X-Test-Auth"

	handler := mock.chainMiddleware([]string{}, bearerTokenCheckHandler(t, true, "new-token-123"))

	req := httptest.NewRequest("GET", "/mcp", nil)
	// Existing Authorization header with an old token
	req.Header.Set("Authorization", "Bearer old-token-999")
	// Custom header with the token that should take precedence
	req.Header.Set("X-Test-Auth", "Bearer new-token-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}
}

func TestCORSMiddleware_NoConfiguration(t *testing.T) {
	handler := corsMiddleware([]string{}, "")(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// No CORS headers should be set
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("Expected no CORS headers when CORS is not configured")
	}
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	handler := corsMiddleware([]string{"*"}, "")(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected Access-Control-Allow-Origin: *, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSMiddleware_SpecificOriginMatching(t *testing.T) {
	allowedOrigins := []string{"http://example.com", "http://localhost:3000"}
	handler := corsMiddleware(allowedOrigins, "")(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("Expected Access-Control-Allow-Origin: http://example.com, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSMiddleware_SpecificOriginNotMatching(t *testing.T) {
	allowedOrigins := []string{"http://example.com"}
	handler := corsMiddleware(allowedOrigins, "")(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Origin should not be set for non-matching origins
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("Expected no Access-Control-Allow-Origin header for non-matching origin")
	}

	// But other CORS headers should still be present
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Expected Access-Control-Allow-Methods header to be set")
	}
}

func TestCORSMiddleware_MultipleOrigins(t *testing.T) {
	allowedOrigins := []string{"http://example.com", "http://localhost:3000", "http://test.com"}
	handler := corsMiddleware(allowedOrigins, "")(mockHandler())

	testCases := []struct {
		origin   string
		expected string
	}{
		{"http://example.com", "http://example.com"},
		{"http://localhost:3000", "http://localhost:3000"},
		{"http://test.com", "http://test.com"},
		{"http://notallowed.com", ""},
	}

	for _, tc := range testCases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200 for origin %s, got %d", tc.origin, rec.Code)
		}

		actual := rec.Header().Get("Access-Control-Allow-Origin")
		if actual != tc.expected {
			t.Errorf("For origin %s, expected Access-Control-Allow-Origin: %q, got %q", tc.origin, tc.expected, actual)
		}
	}
}

func TestCORSMiddleware_PreflightRequest(t *testing.T) {
	allowedOrigins := []string{"http://example.com"}
	handler := corsMiddleware(allowedOrigins, "X-Auth")(mockHandler())

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("Expected status 204 for OPTIONS request, got %d", rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("Expected Access-Control-Allow-Origin: http://example.com, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Expected Access-Control-Allow-Methods header to be set")
	}

	if rec.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Authorization, X-Auth" {
		t.Error("Expected Access-Control-Allow-Headers header to be set")
	}

	if rec.Header().Get("Access-Control-Max-Age") != corsMaxAgeSeconds {
		t.Errorf("Expected Access-Control-Max-Age: %s, got %q", corsMaxAgeSeconds, rec.Header().Get("Access-Control-Max-Age"))
	}
}

func TestCORSMiddleware_MissingOriginHeader(t *testing.T) {
	allowedOrigins := []string{"http://example.com"}
	handler := corsMiddleware(allowedOrigins, "")(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	// No Origin header set
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// No origin header should be set when request has no Origin
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("Expected no Access-Control-Allow-Origin header when request has no Origin")
	}
}

func TestLoggingMiddleware(t *testing.T) {
	handler := loggingMiddleware()(mockHandler())

	req := httptest.NewRequest("GET", "/test?foo=bar", nil)
	req.Header.Set("User-Agent", "test-agent")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Logging middleware should not modify the response
	if rec.Body.String() != "OK" {
		t.Errorf("Expected body 'OK', got %q", rec.Body.String())
	}
}

func TestAddMiddleware_FullChain(t *testing.T) {
	allowedOrigins := []string{"http://example.com"}
	mockServer := mockNeo4jMCPServer(t)
	handler := mockServer.chainMiddleware(allowedOrigins, authCheckHandler(t, true, "user", "pass"))

	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Origin", "http://example.com")
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify CORS headers are set
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("Expected CORS header to be set")
	}
}

func TestAddMiddleware_FullChain_NoAuth(t *testing.T) {
	allowedOrigins := []string{"http://example.com"}
	mockServer := mockNeo4jMCPServer(t)
	handler := mockServer.chainMiddleware(allowedOrigins, mockHandler())

	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Origin", "http://example.com")
	// No auth credentials
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401 when no credentials provided
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}
}

func TestParseAllowedOrigins_Empty(t *testing.T) {
	result := parseAllowedOrigins("")
	if len(result) != 0 {
		t.Errorf("Expected empty slice, got %v", result)
	}
}

func TestParseAllowedOrigins_Wildcard(t *testing.T) {
	result := parseAllowedOrigins("*")
	if len(result) != 1 || result[0] != "*" {
		t.Errorf("Expected [*], got %v", result)
	}
}

func TestParseAllowedOrigins_SingleOrigin(t *testing.T) {
	result := parseAllowedOrigins("http://example.com")
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Errorf("Expected [http://example.com], got %v", result)
	}
}

func TestParseAllowedOrigins_MultipleOrigins(t *testing.T) {
	result := parseAllowedOrigins("http://example.com,http://localhost:3000,http://test.com")
	expected := []string{"http://example.com", "http://localhost:3000", "http://test.com"}

	if len(result) != len(expected) {
		t.Errorf("Expected %d origins, got %d", len(expected), len(result))
	}

	for i, exp := range expected {
		if result[i] != exp {
			t.Errorf("Expected origin[%d] = %q, got %q", i, exp, result[i])
		}
	}
}

func TestParseAllowedOrigins_WithSpaces(t *testing.T) {
	result := parseAllowedOrigins("http://example.com , http://localhost:3000 , http://test.com")
	expected := []string{"http://example.com", "http://localhost:3000", "http://test.com"}

	if len(result) != len(expected) {
		t.Errorf("Expected %d origins, got %d", len(expected), len(result))
	}

	for i, exp := range expected {
		if result[i] != exp {
			t.Errorf("Expected origin[%d] = %q, got %q", i, exp, result[i])
		}
	}
}

func TestPathValidationMiddleware_ValidPath(t *testing.T) {
	handler := pathValidationMiddleware()(mockHandler())

	req := httptest.NewRequest("GET", "/mcp", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /mcp path, got %d", rec.Code)
	}

	if rec.Body.String() != "OK" {
		t.Errorf("Expected body 'OK', got %q", rec.Body.String())
	}
}

func TestPathValidationMiddleware_InvalidPaths(t *testing.T) {
	testCases := []struct {
		name string
		path string
	}{
		{"root path", "/"},
		{"other path", "/api"},
		{"nested path", "/mcp/test"},
		{"similar path", "/mcpserver"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := pathValidationMiddleware()(mockHandler())

			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("Expected status 404 for path %s, got %d", tc.path, rec.Code)
			}

			expectedBody := "Not Found: This server only handles requests to /mcp\n"
			if rec.Body.String() != expectedBody {
				t.Errorf("Expected body %q, got %q", expectedBody, rec.Body.String())
			}
		})
	}
}

func TestPathValidationMiddleware_InFullChain(t *testing.T) {
	// Test that path validation happens before auth check
	// Invalid paths should return 404 without requiring auth
	allowedOrigins := []string{}
	mockServer := mockNeo4jMCPServer(t)
	handler := mockServer.chainMiddleware(allowedOrigins, mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	// No auth credentials
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 404 for invalid path, not 401 for missing auth
	// This proves path validation happens first in the middleware chain
	if rec.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 for invalid path (before auth check), got %d", rec.Code)
	}
}

func TestPathValidationMiddleware_TrailingSlashAllowed(t *testing.T) {
	handler := pathValidationMiddleware()(mockHandler())

	req := httptest.NewRequest("GET", "/mcp/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /mcp/ path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AllowsUnauthenticatedPing(t *testing.T) {
	// Build middleware chain with no allowed origins and a simple handler
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.AllowUnauthenticatedPing = true

	mockAnalytics := mockServer.anService.(*analytics_mocks.MockService)
	mockAnalytics.EXPECT().NewUnauthenticatedJSONRPCEvent("ping").Times(1)
	mockAnalytics.EXPECT().EmitEvent(gomock.Any()).Times(1)

	callback := mockServer.chainMiddleware([]string{}, mockHandler())

	// Create a POST request to /mcp with JSON-RPC ping body and no auth header
	body := `{"jsonrpc":"2.0","method":"ping","params":null,"id":4}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	callback.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for unauthenticated ping, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BlocksUnauthenticatedPingWhenDisabled(t *testing.T) {
	mockServer := mockNeo4jMCPServer(t)
	handler := mockServer.chainMiddleware([]string{}, mockHandler())

	body := `{"jsonrpc":"2.0","method":"ping","params":null,"id":4}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 when unauthenticated ping is disabled, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidBasicAuthHeader(t *testing.T) {
	handler := authMiddleware("Authorization", nil, nil)(mockHandler())

	req := httptest.NewRequest("GET", "/", nil)
	// Invalid basic auth header
	req.Header.Set("Authorization", "Basic invalid-base64")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401 for invalid auth header
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}

	// Should have WWW-Authenticate header
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("Expected WWW-Authenticate header to be set")
	}
}

func TestAuthMiddleware_AllowsUnauthenticatedToolsList(t *testing.T) {
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.AllowUnauthenticatedToolsList = true
	handler := mockServer.chainMiddleware([]string{}, mockHandler())

	mockAnalytics := mockServer.anService.(*analytics_mocks.MockService)
	mockAnalytics.EXPECT().NewUnauthenticatedJSONRPCEvent("tools/list").Times(1)
	mockAnalytics.EXPECT().EmitEvent(gomock.Any()).Times(1)

	body := `{"jsonrpc":"2.0","method":"tools/list","params":null,"id":1}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for unauthenticated tools/list, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BlocksUnauthenticatedToolsListWhenDisabled(t *testing.T) {
	mockServer := mockNeo4jMCPServer(t)
	handler := mockServer.chainMiddleware([]string{}, mockHandler())

	body := `{"jsonrpc":"2.0","method":"tools/list","params":null,"id":1}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 when unauthenticated tools/list is disabled, got %d", rec.Code)
	}
}

func TestAuthMiddleware_RejectsTooLargeUnauthenticatedPing(t *testing.T) {
	// This test constructs a POST /mcp request whose body exceeds the
	// maxUnauthenticatedBodyBytes limit. We set ContentLength to -1 so the
	// middleware will actually read the body (and hit MaxBytesReader) instead
	// of short-circuiting on ContentLength.
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.AllowUnauthenticatedPing = true
	handler := mockServer.chainMiddleware([]string{}, mockHandler())

	// Build a JSON ping body and pad it to exceed the max allowed size
	pad := strings.Repeat("x", maxUnauthenticatedBodyBytes+10)
	body := `{"jsonrpc":"2.0","method":"ping","params":null,"id":4,"pad":"` + pad + `"}`

	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// Force the middleware to read from the body instead of using ContentLength
	req.ContentLength = -1

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Expected status 413 Payload Too Large for oversized unauthenticated ping, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AllowsUnauthenticatedInitialize(t *testing.T) {
	// Build middleware chain with no allowed origins and a simple handler
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.AllowUnauthenticatedInitialize = true

	mockAnalytics := mockServer.anService.(*analytics_mocks.MockService)
	mockAnalytics.EXPECT().NewUnauthenticatedJSONRPCEvent("initialize").Times(1)
	mockAnalytics.EXPECT().EmitEvent(gomock.Any()).Times(1)

	callback := mockServer.chainMiddleware([]string{}, mockHandler())

	// Create a POST request to /mcp with JSON-RPC ping body and no auth header
	body := `{"jsonrpc":"2.0","method":"initialize","params":null,"id":1}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	callback.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for unauthenticated initialize, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AllowsUnauthenticatedNotificationsInitialize(t *testing.T) {
	// Build middleware chain with no allowed origins and a simple handler
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.AllowUnauthenticatedNotificationsInitialize = true

	mockAnalytics := mockServer.anService.(*analytics_mocks.MockService)
	mockAnalytics.EXPECT().NewUnauthenticatedJSONRPCEvent("notifications/initialized").Times(1)
	mockAnalytics.EXPECT().EmitEvent(gomock.Any()).Times(1)

	callback := mockServer.chainMiddleware([]string{}, mockHandler())

	// Create a POST request to /mcp with JSON-RPC ping body and no auth header
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	callback.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for unauthenticated initialize, got %d", rec.Code)
	}
}

// instanceSelectionCheckHandler verifies the expected instance name landed
// in context via auth.WithInstanceSelection.
func instanceSelectionCheckHandler(t *testing.T, expectedName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := auth.GetInstanceSelection(r.Context())
		if !ok {
			t.Error("Expected instance selection in context, but none found")
		}
		if name != expectedName {
			t.Errorf("Expected instance name %q, got %q", expectedName, name)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestInstanceAuthMiddleware_Basic(t *testing.T) {
	instAuth := config.InstanceAuth{Type: config.InstanceAuthBasic, APIKeys: []string{"good-key"}}

	t.Run("valid API key is accepted and instance selection is set", func(t *testing.T) {
		handler := instanceAuthMiddleware("prod", instAuth, "X-Api-Key", "https", nil)(instanceSelectionCheckHandler(t, "prod"))

		req := httptest.NewRequest("POST", "/prod/mcp", nil)
		req.Header.Set("X-Api-Key", "good-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("missing API key is rejected", func(t *testing.T) {
		handler := instanceAuthMiddleware("prod", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/prod/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})

	t.Run("wrong API key is rejected", func(t *testing.T) {
		handler := instanceAuthMiddleware("prod", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/prod/mcp", nil)
		req.Header.Set("X-Api-Key", "wrong-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})

	t.Run("a different instance's API key is rejected", func(t *testing.T) {
		handler := instanceAuthMiddleware("prod", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/prod/mcp", nil)
		req.Header.Set("X-Api-Key", "some-other-instances-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})
}

func TestInstanceAuthMiddleware_BasicPassthrough(t *testing.T) {
	instAuth := config.InstanceAuth{Type: config.InstanceAuthBasicPassthrough}

	t.Run("valid basic credentials are forwarded and instance selection is set", func(t *testing.T) {
		handler := instanceAuthMiddleware("staging", instAuth, "X-Api-Key", "https", nil)(authCheckHandler(t, true, "user", "pass"))

		req := httptest.NewRequest("POST", "/staging/mcp", nil)
		req.SetBasicAuth("user", "pass")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("missing basic credentials are rejected", func(t *testing.T) {
		handler := instanceAuthMiddleware("staging", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/staging/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})
}

func TestInstanceAuthMiddleware_Bearer(t *testing.T) {
	instAuth := config.InstanceAuth{
		Type: config.InstanceAuthBearer, Issuer: "https://idp.example.com/", JWKSURI: "https://idp.example.com/jwks.json", Audience: "api://neo4j-mcp",
	}

	t.Run("missing bearer token is rejected", func(t *testing.T) {
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})

	t.Run("no verifier configured yet returns 501, not a silent pass", func(t *testing.T) {
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		req.Header.Set("Authorization", "Bearer some-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Errorf("Expected status 501, got %d", rec.Code)
		}
	})

	t.Run("verifier rejecting the token returns 401", func(t *testing.T) {
		verifier := func(_ context.Context, _ config.InstanceAuth, _ string) error {
			return errors.New("invalid signature")
		}
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", verifier)(mockHandler())

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		req.Header.Set("Authorization", "Bearer bad-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})

	t.Run("verifier accepting the token forwards it and sets instance selection", func(t *testing.T) {
		verifier := func(_ context.Context, a config.InstanceAuth, token string) error {
			if a.Issuer != instAuth.Issuer || token != "good-token" {
				t.Errorf("verifier received unexpected auth=%+v token=%q", a, token)
			}
			return nil
		}
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", verifier)(bearerTokenCheckHandler(t, true, "good-token"))

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		req.Header.Set("Authorization", "Bearer good-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})
}

func TestMatchesAnyAPIKey(t *testing.T) {
	configured := []string{"key-a", "key-b"}

	if !matchesAnyAPIKey("key-a", configured) {
		t.Error("expected key-a to match")
	}
	if !matchesAnyAPIKey("key-b", configured) {
		t.Error("expected key-b to match")
	}
	if matchesAnyAPIKey("key-c", configured) {
		t.Error("expected key-c not to match")
	}
	if matchesAnyAPIKey("", configured) {
		t.Error("expected empty string not to match")
	}
	if matchesAnyAPIKey("key-a", nil) {
		t.Error("expected no match against an empty configured list")
	}
}

func TestBuildMultiInstanceHandler_RoutesByPath(t *testing.T) {
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.HTTPAPIKeyHeaderName = "X-Api-Key"
	mockServer.config.Instances = []config.NeoInstance{
		{Name: "prod", URI: "neo4j://prod:7687", Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, APIKeys: []string{"prod-key"}}},
		{Name: "staging", URI: "neo4j://staging:7687", Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, APIKeys: []string{"staging-key"}}},
	}

	// Echoes the resolved instance name in the response body, so each
	// sub-test below can assert which instance's route it actually reached.
	echoInstanceHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := auth.GetInstanceSelection(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(name))
	})

	handler := mockServer.buildMultiInstanceHandler(nil, echoInstanceHandler)

	t.Run("routes /prod/mcp to the prod instance", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/prod/mcp", nil)
		req.Header.Set("X-Api-Key", "prod-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "prod" {
			t.Errorf("Expected response body %q, got %q", "prod", rec.Body.String())
		}
	})

	t.Run("routes /staging/mcp to the staging instance", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/staging/mcp", nil)
		req.Header.Set("X-Api-Key", "staging-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", rec.Code)
		}
		if rec.Body.String() != "staging" {
			t.Errorf("Expected response body %q, got %q", "staging", rec.Body.String())
		}
	})

	t.Run("prod's API key is rejected against staging's route", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/staging/mcp", nil)
		req.Header.Set("X-Api-Key", "prod-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rec.Code)
		}
	})

	t.Run("an unconfigured instance path 404s", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/does-not-exist/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", rec.Code)
		}
	})
}

func TestInstanceAuthMiddleware_Bearer_WWWAuthenticateResourceMetadata(t *testing.T) {
	instAuth := config.InstanceAuth{
		Type: config.InstanceAuthBearer, Issuer: "https://idp.example.com/", JWKSURI: "https://idp.example.com/jwks.json", Audience: "api://neo4j-mcp",
	}
	wantMeta := `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/analytics/mcp"`

	t.Run("missing token includes resource_metadata", func(t *testing.T) {
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", nil)(mockHandler())

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		req.Host = "mcp.example.com"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, wantMeta) {
			t.Errorf("WWW-Authenticate = %q, want it to contain %q", got, wantMeta)
		}
	})

	t.Run("rejected token includes resource_metadata and invalid_token", func(t *testing.T) {
		verifier := func(_ context.Context, _ config.InstanceAuth, _ string) error {
			return errors.New("bad token")
		}
		handler := instanceAuthMiddleware("analytics", instAuth, "X-Api-Key", "https", verifier)(mockHandler())

		req := httptest.NewRequest("POST", "/analytics/mcp", nil)
		req.Host = "mcp.example.com"
		req.Header.Set("Authorization", "Bearer bad-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		got := rec.Header().Get("WWW-Authenticate")
		if !strings.Contains(got, wantMeta) {
			t.Errorf("WWW-Authenticate = %q, want it to contain %q", got, wantMeta)
		}
		if !strings.Contains(got, `error="invalid_token"`) {
			t.Errorf("WWW-Authenticate = %q, want it to contain invalid_token", got)
		}
	})
}

func TestProtectedResourceMetadataHandler(t *testing.T) {
	handler := protectedResourceMetadataHandler("https", "analytics", "https://idp.example.com/")

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/analytics/mcp", nil)
	req.Host = "mcp.example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var doc protectedResourceMetadataDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if doc.Resource != "https://mcp.example.com/analytics/mcp" {
		t.Errorf("resource = %q, want %q", doc.Resource, "https://mcp.example.com/analytics/mcp")
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://idp.example.com/" {
		t.Errorf("authorization_servers = %v, want [\"https://idp.example.com/\"]", doc.AuthorizationServers)
	}
}

func TestBuildMultiInstanceHandler_MetadataRouteOnlyForBearerInstances(t *testing.T) {
	mockServer := mockNeo4jMCPServer(t)
	mockServer.config.HTTPAPIKeyHeaderName = "X-Api-Key"
	mockServer.config.Instances = []config.NeoInstance{
		{Name: "prod", URI: "neo4j://prod:7687", Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, APIKeys: []string{"prod-key"}}},
		{Name: "analytics", URI: "neo4j://analytics:7687", Auth: config.InstanceAuth{
			Type: config.InstanceAuthBearer, Issuer: "https://idp.example.com/", JWKSURI: "https://idp.example.com/jwks.json", Audience: "aud",
		}},
	}

	handler := mockServer.buildMultiInstanceHandler(nil, mockHandler())

	t.Run("bearer instance metadata route is served", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/analytics/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("basic instance has no metadata route", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/prod/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", rec.Code)
		}
	})
}
