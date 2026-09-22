// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

// adminCookieName holds the admin token once an operator has logged in via
// /admin/login. This is a deliberately minimal, first-pass session
// mechanism appropriate for a canary/experimental feature — not a general
// session store: the cookie value is compared directly against the
// configured token on every request, there's no session table, no
// expiry beyond the cookie's own MaxAge, and no logout endpoint.
const adminCookieName = "neo4j_mcp_admin_token"

const adminCookieMaxAgeSeconds = 12 * 60 * 60 // 12 hours

// adminAuthMiddleware gates every request behind a valid admin session
// cookie, using a constant-time comparison against the configured token so
// response timing can't be used to guess it. Callers must not mount this at
// all when token is empty — see buildHandler, which only registers /admin/
// when Config.AdminToken is set, so the surface fails closed (404, not 401)
// by simply not existing rather than existing-but-locked.
func adminAuthMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(adminCookieName)
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// handleAdminLogin serves a minimal login form (GET) and validates a
// submitted token (POST), setting the session cookie on success. It is
// deliberately unauthenticated itself — it's how an operator establishes
// the cookie adminAuthMiddleware checks — and deliberately self-contained
// (inline styling, no static asset dependency) since /admin/static/* sits
// behind adminAuthMiddleware and wouldn't be reachable from this page yet.
func handleAdminLogin(token string, secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(adminLoginPageHTML("")))
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(adminLoginPageHTML("Invalid form submission.")))
				return
			}
			submitted := r.FormValue("token")
			if subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(adminLoginPageHTML("Incorrect token.")))
				return
			}
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is intentionally conditional on HTTPTLSEnabled, not hardcoded true, so the admin login also works for a legitimate local/dev deployment without TLS
				Name:     adminCookieName,
				Value:    submitted,
				Path:     "/admin/",
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   adminCookieMaxAgeSeconds,
			})
			http.Redirect(w, r, "/admin/", http.StatusFound)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func adminLoginPageHTML(errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = fmt.Sprintf(`<p style="color:#c0392b">%s</p>`, errMsg)
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>neo4j-mcp-canary admin login</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;max-width:24rem;margin:4rem auto;padding:0 1rem;}
input{display:block;width:100%%;padding:0.5rem;margin:0.5rem 0 1rem;box-sizing:border-box;}
button{background:#0b5fff;color:#fff;border:none;border-radius:4px;padding:0.5rem 1rem;cursor:pointer;}
</style></head>
<body>
<h1>Admin login</h1>
%s
<form method="post" action="/admin/login">
  <label for="token">Admin token</label>
  <input type="password" id="token" name="token" autofocus>
  <button type="submit">Log in</button>
</form>
</body></html>`, errHTML)
}
