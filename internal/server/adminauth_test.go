// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

func TestAdminAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("authenticated"))
	})
	mw := adminAuthMiddleware("correct-token")(inner)

	t.Run("no cookie redirects to login", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/admin/login" {
			t.Errorf("Location = %q, want /admin/login", loc)
		}
	})

	t.Run("wrong cookie value redirects to login", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "wrong-token"}) //nolint:gosec // request-side cookie; Secure/HttpOnly/SameSite are response-only attributes
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
	})

	t.Run("correct cookie passes through", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		req.AddCookie(&http.Cookie{Name: adminCookieName, Value: "correct-token"}) //nolint:gosec // request-side cookie; Secure/HttpOnly/SameSite are response-only attributes
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "authenticated" {
			t.Errorf("status = %d, body = %q, want 200/authenticated", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleAdminLogin(t *testing.T) {
	h := handleAdminLogin("correct-token", false)

	t.Run("GET serves the login form", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<form") {
			t.Errorf("status = %d, body missing a form: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST wrong token shows an error, no cookie set", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("token=nope"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h(rec, req)
		if !strings.Contains(rec.Body.String(), "Incorrect token") {
			t.Errorf("body = %s, want an incorrect-token message", rec.Body.String())
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Error("a cookie was set despite an incorrect token")
		}
	})

	t.Run("POST correct token sets cookie and redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader("token=correct-token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != adminCookieName || cookies[0].Value != "correct-token" {
			t.Errorf("cookies = %+v, want one %s=correct-token", cookies, adminCookieName)
		}
		if !cookies[0].HttpOnly {
			t.Error("admin cookie is not HttpOnly")
		}
	})
}

func TestBuildHandler_AdminRouteFailsClosedWithoutToken(t *testing.T) {
	s := &Neo4jMCPServer{
		mcpServer: mcpsdk.NewServer("test-server", "1.0.0"),
		config:    newLiveConfig(baseConfig()), // AdminToken empty by default
		dbService: newLiveDBService(nil),
	}
	handler := s.buildHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when AdminToken is unset (fail closed)", rec.Code)
	}
}

func TestBuildHandler_AdminRouteReachableWithToken(t *testing.T) {
	cfg := baseConfig()
	cfg.AdminToken = "the-token"
	s := &Neo4jMCPServer{
		mcpServer: mcpsdk.NewServer("test-server", "1.0.0"),
		config:    newLiveConfig(cfg),
		dbService: newLiveDBService(nil),
	}
	handler := s.buildHandler()

	// Unauthenticated: redirected to login, not 404 — the route exists.
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 (redirect to login) when AdminToken is set", rec.Code)
	}

	// The login page itself is reachable without a cookie.
	req = httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /admin/login status = %d, want 200", rec.Code)
	}
}
