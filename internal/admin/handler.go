// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"

	"github.com/google/uuid"
)

//go:embed static
var embeddedStatic embed.FS

// New builds the authenticated dashboard's http.Handler: the static page,
// its JSON API, and the tool-call playground proxy. Callers (internal/server)
// are responsible for authenticating a request — via their own
// adminAuthMiddleware, which needs the real AdminToken value this package
// never sees — before routing anything here. New itself performs no auth.
func New(backend Backend) http.Handler {
	mux := http.NewServeMux()

	staticContent, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		// Only possible if the embed directive above doesn't match what
		// was actually embedded at build time — a programming error, not a
		// runtime condition.
		panic(err)
	}
	mux.Handle("/admin/static/", http.StripPrefix("/admin/static/", http.FileServerFS(staticContent)))

	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/" && r.URL.Path != "/admin" {
			http.NotFound(w, r)
			return
		}
		data, err := embeddedStatic.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "dashboard page missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/admin/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config": backend.GetConfig(),
			"tools":  backend.ListTools(),
		})
	})

	mux.HandleFunc("/admin/api/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var edits EditableConfig
		if err := json.NewDecoder(r.Body).Decode(&edits); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, backend.Preview(edits))
	})

	mux.HandleFunc("/admin/api/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var edits EditableConfig
		if err := json.NewDecoder(r.Body).Decode(&edits); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		outcome, err := backend.Apply(r.Context(), edits)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, outcome)
	})

	mux.HandleFunc("/admin/api/playground/call", handlePlaygroundCall(backend))

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// playgroundCallRequest is the dashboard's simplified request shape for a
// tool-call attempt — the handler wraps it into a real MCP JSON-RPC
// "tools/call" request internally.
type playgroundCallRequest struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// handlePlaygroundCall builds a real MCP "tools/call" JSON-RPC request from
// the dashboard's simplified form and serves it through backend.ServeMCP —
// the exact same handler/middleware chain a real client hits — copying the
// operator-supplied Authorization header across, so the call is subject to
// the same auth/tool-selection behavior any other client would see. The raw
// JSON-RPC response is returned to the browser unchanged.
func handlePlaygroundCall(backend Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req playgroundCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		rpcBody, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      uuid.NewString(),
			"method":  "tools/call",
			"params": map[string]any{
				"name":      req.Tool,
				"arguments": req.Arguments,
			},
		})
		if err != nil {
			http.Error(w, "failed to build tool call: "+err.Error(), http.StatusInternalServerError)
			return
		}

		inner, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/mcp", bytes.NewReader(rpcBody))
		if err != nil {
			http.Error(w, "failed to build tool call request: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// The MCP SDK's streamable-HTTP handler validates the Host header
		// (DNS-rebinding protection) — a synthetic request built from a bare
		// path has none, so copy the real inbound request's Host to make
		// this look like the same-origin request it actually is.
		inner.Host = r.Host
		inner.Header.Set("Content-Type", "application/json")
		inner.Header.Set("Accept", "application/json, text/event-stream")
		if authz := r.Header.Get("Authorization"); authz != "" {
			inner.Header.Set("Authorization", authz)
		}

		rec := httptest.NewRecorder()
		backend.ServeMCP(rec, inner)

		w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
}
