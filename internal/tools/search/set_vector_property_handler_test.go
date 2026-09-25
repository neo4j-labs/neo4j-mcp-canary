// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/dbtype"
	"go.uber.org/mock/gomock"
)

func updatedCountRecord(count int64) []*neo4j.Record {
	return []*neo4j.Record{{Keys: []string{"updated"}, Values: []any{count}}}
}

// noMatchingVectorIndex mocks the dimension-check lookup finding no vector
// index on the target label/property yet, so set-vector-property's check is
// skipped and the write proceeds unchanged — the common case for every test
// below that isn't specifically about that check.
func noMatchingVectorIndex(mockDB *db.MockService) {
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), lookupVectorIndexDimensionsQueryText, gomock.Any()).
		Return(nil, nil)
}

func vectorIndexDimensionsRecord(dims int64) []*neo4j.Record {
	return []*neo4j.Record{{
		Keys:   []string{"options"},
		Values: []any{map[string]any{"indexConfig": map[string]any{"vector.dimensions": dims}}},
	}}
}

// lookupVectorIndexDimensionsQueryText mirrors the unexported query string
// internal/tools/search/embedding.go's vectorIndexDimensionsByLabelOrType
// builds — duplicated here (rather than exported) since asserting the exact
// generated Cypher is this test suite's existing convention for every other
// query this package builds.
const lookupVectorIndexDimensionsQueryText = "SHOW INDEXES YIELD type, entityType, labelsOrTypes, properties, options " +
	"WHERE type = 'VECTOR' AND entityType = $entityType AND $labelOrType IN labelsOrTypes AND $property IN properties"

// embedTextQueryText mirrors embedding.go's embedTextQuery constant.
const embedTextQueryText = "RETURN ai.text.embed($text, $embeddingProvider, $embeddingConfiguration) AS vector"

// embeddedVectorRecord mocks ai.text.embed's real return shape: a
// dbtype.Vector[float32] (a native VECTOR value, not a plain LIST<FLOAT>) —
// confirmed empirically against a live 2026.09 server backed by an
// OpenAI-compatible local embedding server (LM Studio/nomic-embed-text-v1.5).
func embeddedVectorRecord(vector ...float64) []*neo4j.Record {
	elems := make([]float32, len(vector))
	for i, v := range vector {
		elems[i] = float32(v)
	}
	return []*neo4j.Record{{Keys: []string{"vector"}, Values: []any{dbtype.Vector[float32]{Elems: elems}}}}
}

// openAIEmbeddingsTestServer mimics an OpenAI-compatible /embeddings
// endpoint (real OpenAI or a local server like LM Studio/Ollama) — used to
// test EmbeddingProviderOpenAI's direct-HTTP path (see
// generateEmbeddingViaOpenAI) without a real network call. Fails the test
// if the request doesn't match wantModel/wantToken, mirroring what the
// real API would reject with instead of silently accepting a bad request.
func openAIEmbeddingsTestServer(t *testing.T, vector []float64, wantModel, wantToken string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer "+wantToken)
		}
		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if body.Model != wantModel {
			t.Errorf("request model = %q, want %q", body.Model, wantModel)
		}
		if body.Input == "" {
			t.Error("request input is empty")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": vector}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSetVectorPropertyHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	validFilters := []any{map[string]any{"property": "migrated", "operator": "=", "value": true}}

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "vector": []any{0.1, 0.2},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("validation errors produce no DB calls", func(t *testing.T) {
		tests := []struct {
			name string
			args map[string]any
		}{
			{"missing entityType", map[string]any{"label": "Chunk", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"invalid entityType", map[string]any{"entityType": "EDGE", "label": "Chunk", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"neither label nor relationshipType", map[string]any{"entityType": "NODE", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"both label and relationshipType", map[string]any{"entityType": "NODE", "label": "Chunk", "relationshipType": "REL", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			// Regression: entityType NODE with only relationshipType set (label
			// left empty) used to pass the old "not both empty, not both set"
			// check and silently resolve to an empty label, producing a broken
			// `(n:``)` pattern instead of a clear error. Same for the mirrored
			// RELATIONSHIP case below.
			{"NODE with only relationshipType set (mismatched field)", map[string]any{"entityType": "NODE", "relationshipType": "REL", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"RELATIONSHIP with only label set (mismatched field)", map[string]any{"entityType": "RELATIONSHIP", "label": "Chunk", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"zero filters", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": []any{}, "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"missing filters", map[string]any{"entityType": "NODE", "label": "Chunk", "vectorProperty": "embedding", "vector": []any{0.1}}},
			{"missing vectorProperty", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": validFilters, "vector": []any{0.1}}},
			{"empty vector", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{}}},
			{"neither vector nor text", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": validFilters, "vectorProperty": "embedding"}},
			{"both vector and text", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": validFilters, "vectorProperty": "embedding", "vector": []any{0.1}, "text": "hello world"}},
			{"invalid filter operator", map[string]any{"entityType": "NODE", "label": "Chunk", "filters": []any{map[string]any{"property": "migrated", "operator": "!=", "value": true}}, "vectorProperty": "embedding", "vector": []any{0.1}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockDB := db.NewMockService(ctrl)
				deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
				handler := search.SetVectorPropertyHandler(deps)
				result, err := handler(context.Background(), callToolRequest(tt.args))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result == nil || !result.IsError {
					t.Fatalf("expected error result, got: %+v", result)
				}
			})
		}
	})

	t.Run("node happy path builds the expected query and params", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		noMatchingVectorIndex(mockDB)
		wantQuery := "MATCH (n:`Chunk`)\n" +
			"WHERE n.`migrated` = $f0\n" +
			"CALL db.create.setNodeVectorProperty(n, $vectorProperty, $vector)\n" +
			"RETURN count(n) AS updated"
		wantParams := map[string]any{
			"f0":             true,
			"vectorProperty": "embedding",
			"vector":         []float64{0.1, 0.2},
		}
		mockDB.EXPECT().
			ExecuteWriteQuery(gomock.Any(), wantQuery, wantParams).
			Return(updatedCountRecord(3), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "vector": []any{0.1, 0.2},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
		text := getText(t, result)
		if text == "" {
			t.Fatalf("expected non-empty response text")
		}
	})

	t.Run("relationship happy path builds the expected query and params", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		noMatchingVectorIndex(mockDB)
		wantQuery := "MATCH ()-[r:`REVIEWED`]-()\n" +
			"WHERE r.`score` > $f0\n" +
			"CALL db.create.setRelationshipVectorProperty(r, $vectorProperty, $vector)\n" +
			"RETURN count(r) AS updated"
		wantParams := map[string]any{
			"f0":             float64(4),
			"vectorProperty": "embedding",
			"vector":         []float64{0.5, 0.6},
		}
		mockDB.EXPECT().
			ExecuteWriteQuery(gomock.Any(), wantQuery, wantParams).
			Return(updatedCountRecord(7), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType":       "RELATIONSHIP",
			"relationshipType": "REVIEWED",
			"filters":          []any{map[string]any{"property": "score", "operator": ">", "value": 4}},
			"vectorProperty":   "embedding",
			"vector":           []any{0.5, 0.6},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("vector dimension mismatch against an existing index is rejected before any write", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupVectorIndexDimensionsQueryText, gomock.Any()).
			Return(vectorIndexDimensionsRecord(1536), nil)
		// No ExecuteWriteQuery expectation: gomock fails the test if the
		// handler calls it despite the mismatch.

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "vector": []any{0.1, 0.2},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("text without a configured embedding provider is rejected before any DB call", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "text": "hello world",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("text with a Neo4j-routed provider (azure-openai) generates via ai.text.embed, checks, and stores the vector", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), embedTextQueryText, map[string]any{
				"text":                   "hello world",
				"embeddingProvider":      "azure-openai",
				"embeddingConfiguration": map[string]string{"token": "sk-test", "resource": "my-resource", "model": "text-embedding-3-small"},
			}).
			Return(embeddedVectorRecord(0.5, 0.25, 0.125), nil)
		noMatchingVectorIndex(mockDB)
		wantQuery := "MATCH (n:`Chunk`)\n" +
			"WHERE n.`migrated` = $f0\n" +
			"CALL db.create.setNodeVectorProperty(n, $vectorProperty, $vector)\n" +
			"RETURN count(n) AS updated"
		wantParams := map[string]any{
			"f0":             true,
			"vectorProperty": "embedding",
			"vector":         []float64{0.5, 0.25, 0.125},
		}
		mockDB.EXPECT().
			ExecuteWriteQuery(gomock.Any(), wantQuery, wantParams).
			Return(updatedCountRecord(1), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider:      config.EmbeddingProviderAzureOpenAI,
			Configuration: map[string]string{"token": "sk-test", "resource": "my-resource", "model": "text-embedding-3-small"},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "text": "hello world",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("generated embedding failing the dimension check is rejected before any write (Neo4j-routed)", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), embedTextQueryText, gomock.Any()).
			Return(embeddedVectorRecord(0.5, 0.25, 0.125), nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupVectorIndexDimensionsQueryText, gomock.Any()).
			Return(vectorIndexDimensionsRecord(1536), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider:      config.EmbeddingProviderAzureOpenAI,
			Configuration: map[string]string{"token": "sk-test", "resource": "my-resource", "model": "text-embedding-3-small"},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "text": "hello world",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("text with an openai provider generates via a direct HTTP call (no ai.text.embed), checks, and stores the vector", func(t *testing.T) {
		server := openAIEmbeddingsTestServer(t, []float64{0.5, 0.25, 0.125}, "text-embedding-nomic-embed-text-v1.5", "lm-studio-token")

		mockDB := db.NewMockService(ctrl)
		noMatchingVectorIndex(mockDB)
		wantQuery := "MATCH (n:`Chunk`)\n" +
			"WHERE n.`migrated` = $f0\n" +
			"CALL db.create.setNodeVectorProperty(n, $vectorProperty, $vector)\n" +
			"RETURN count(n) AS updated"
		wantParams := map[string]any{
			"f0":             true,
			"vectorProperty": "embedding",
			"vector":         []float64{0.5, 0.25, 0.125},
		}
		mockDB.EXPECT().
			ExecuteWriteQuery(gomock.Any(), wantQuery, wantParams).
			Return(updatedCountRecord(1), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider: config.EmbeddingProviderOpenAI,
			Configuration: map[string]string{
				"token": "lm-studio-token", "model": "text-embedding-nomic-embed-text-v1.5", "baseUrl": server.URL,
			},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "text": "hello world",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("openai provider's HTTP error is surfaced clearly", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
		}))
		t.Cleanup(server.Close)

		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider:      config.EmbeddingProviderOpenAI,
			Configuration: map[string]string{"token": "bad-token", "model": "some-model", "baseUrl": server.URL},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "text": "hello world",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("propagates a write failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		noMatchingVectorIndex(mockDB)
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, context.DeadlineExceeded)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.SetVectorPropertyHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Chunk", "filters": validFilters,
			"vectorProperty": "embedding", "vector": []any{0.1},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})
}
