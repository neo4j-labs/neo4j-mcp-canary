// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeBackend is a minimal, fully in-memory Backend implementation for
// testing the HTTP handler layer in isolation from any real server.
type fakeBackend struct {
	config        RedactedConfig
	tools         []ToolInfo
	previewResult PreviewResult
	applyResult   ApplyOutcome
	applyErr      error
	lastApplyEdit EditableConfig

	servedRequests []*http.Request
	serveResponse  func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeBackend) GetConfig() RedactedConfig { return f.config }

func (f *fakeBackend) Apply(_ context.Context, edits EditableConfig) (ApplyOutcome, error) {
	f.lastApplyEdit = edits
	return f.applyResult, f.applyErr
}

func (f *fakeBackend) Preview(edits EditableConfig) PreviewResult {
	f.lastApplyEdit = edits
	return f.previewResult
}

func (f *fakeBackend) ListTools() []ToolInfo { return f.tools }

func (f *fakeBackend) ServeMCP(w http.ResponseWriter, r *http.Request) {
	f.servedRequests = append(f.servedRequests, r)
	if f.serveResponse != nil {
		f.serveResponse(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{}}`))
}

func TestHandler_GetConfig(t *testing.T) {
	backend := &fakeBackend{
		config: RedactedConfig{EditableConfig: EditableConfig{ReadOnly: true}, HasAdminToken: true},
		tools:  []ToolInfo{{Name: "read-cypher", Label: "Read Cypher", Category: "cypher", ReadOnly: true}},
	}
	h := New(backend)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Config RedactedConfig `json:"config"`
		Tools  []ToolInfo     `json:"tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !body.Config.ReadOnly || !body.Config.HasAdminToken {
		t.Errorf("config = %+v, want ReadOnly and HasAdminToken true", body.Config)
	}
	if len(body.Tools) != 1 || body.Tools[0].Name != "read-cypher" {
		t.Errorf("tools = %+v, want one read-cypher entry", body.Tools)
	}
}

func TestHandler_GetConfig_NeverLeaksASecretValue(t *testing.T) {
	// RedactedConfig/EditableConfig structurally have no Password/AdminToken
	// value field at all (see backend.go) — this test guards against a
	// future field addition accidentally reintroducing one, by asserting
	// the raw JSON never contains a suspicious "token"/"password" value key.
	backend := &fakeBackend{config: RedactedConfig{HasAdminToken: true}}
	h := New(backend)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	raw := rec.Body.String()
	for _, forbidden := range []string{"\"adminToken\":\"", "\"password\":\""} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("response leaked a secret-shaped field: found %q in %s", forbidden, raw)
		}
	}
}

func TestHandler_Preview(t *testing.T) {
	backend := &fakeBackend{
		previewResult: PreviewResult{
			Changes: []FieldDiff{{Field: "ReadOnly", Old: "false", New: "true"}},
			Tiers:   []string{"instant"},
		},
	}
	h := New(backend)

	body := `{"readOnly":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/preview", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !backend.lastApplyEdit.ReadOnly {
		t.Error("Preview did not receive the decoded EditableConfig")
	}
	var result PreviewResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Field != "ReadOnly" {
		t.Errorf("result = %+v, want one ReadOnly change", result)
	}
}

func TestHandler_Apply(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		backend := &fakeBackend{applyResult: ApplyOutcome{Tiers: []string{"instant"}, Bounced: false}}
		h := New(backend)

		req := httptest.NewRequest(http.MethodPost, "/admin/api/apply", strings.NewReader(`{"readOnly":true}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
		}
		var outcome ApplyOutcome
		if err := json.Unmarshal(rec.Body.Bytes(), &outcome); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(outcome.Tiers) != 1 || outcome.Tiers[0] != "instant" {
			t.Errorf("outcome = %+v, want Tiers=[instant]", outcome)
		}
	})

	t.Run("backend error surfaces as 400", func(t *testing.T) {
		backend := &fakeBackend{applyErr: fmt.Errorf("invalid configuration: boom")}
		h := New(backend)

		req := httptest.NewRequest(http.MethodPost, "/admin/api/apply", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "boom") {
			t.Errorf("body = %s, want it to mention the error", rec.Body.String())
		}
	})

	t.Run("wrong method is rejected", func(t *testing.T) {
		backend := &fakeBackend{}
		h := New(backend)
		req := httptest.NewRequest(http.MethodGet, "/admin/api/apply", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rec.Code)
		}
	})
}

func TestHandler_PlaygroundCall_BuildsRealToolCallAndForwardsAuth(t *testing.T) {
	backend := &fakeBackend{}
	backend.serveResponse = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("inner request path = %q, want /mcp", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Basic abc123" {
			t.Errorf("inner request Authorization = %q, want forwarded value", got)
		}
		var rpc struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("failed to decode inner JSON-RPC body: %v", err)
		}
		if rpc.Method != "tools/call" || rpc.Params.Name != "read-cypher" {
			t.Errorf("inner request = %+v, want a tools/call for read-cypher", rpc)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"x","result":{"ok":true}}`))
	}
	h := New(backend)

	body := `{"tool":"read-cypher","arguments":{"query":"RETURN 1"}}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/playground/call", strings.NewReader(body))
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(backend.servedRequests) != 1 {
		t.Fatalf("ServeMCP called %d times, want 1", len(backend.servedRequests))
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("response body = %s, want the inner MCP response passed through", rec.Body.String())
	}
}

func TestHandler_StaticAssetsAndIndexPage(t *testing.T) {
	h := New(&fakeBackend{})

	for _, path := range []string{"/admin/", "/admin/static/style.css", "/admin/static/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}
}
