// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

const showVectorIndexByNameQueryText = "SHOW INDEXES YIELD name, type, options WHERE name = $name"

func vectorIndexByNameRecord(indexType string, dims int64) []*neo4j.Record {
	return []*neo4j.Record{{
		Keys: []string{"name", "type", "options"},
		Values: []any{
			"my_index", indexType,
			map[string]any{"indexConfig": map[string]any{"vector.dimensions": dims}},
		},
	}}
}

// openaiEmbeddingCtx is only used by tests that fail before reaching
// generateEmbedding (index lookup failures) — it has no baseUrl, so if a
// test using it ever did reach generateEmbeddingViaOpenAI, it would try a
// real network call to api.openai.com. Tests that exercise generation
// itself use azureOpenAIEmbeddingCtx (Neo4j-routed, mockable) or build an
// openai context inline pointed at an httptest.Server.
func openaiEmbeddingCtx() context.Context {
	return auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
		Provider:      config.EmbeddingProviderOpenAI,
		Configuration: map[string]string{"token": "sk-test", "model": "text-embedding-3-small"},
	})
}

func azureOpenAIEmbeddingCtx() context.Context {
	return auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
		Provider:      config.EmbeddingProviderAzureOpenAI,
		Configuration: map[string]string{"token": "sk-test", "resource": "my-resource", "model": "text-embedding-3-small"},
	})
}

func TestCheckEmbeddingDimensionsHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("missing indexName produces no DB calls", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(openaiEmbeddingCtx(), callToolRequest(map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("no embedding provider configured produces no DB calls", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("index not found", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, map[string]any{"name": "missing-index"}).
			Return(nil, nil)
		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(openaiEmbeddingCtx(), callToolRequest(map[string]any{"indexName": "missing-index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("propagates an index lookup failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, gomock.Any()).Return(nil, errors.New("boom"))
		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(openaiEmbeddingCtx(), callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("index is not a VECTOR index", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, gomock.Any()).
			Return(vectorIndexByNameRecord("FULLTEXT", 0), nil)
		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(openaiEmbeddingCtx(), callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("matching dimensions via ai.text.embed (azure-openai, Neo4j-routed), using the default sample text", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, map[string]any{"name": "my_index"}).
			Return(vectorIndexByNameRecord("VECTOR", 3), nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), embedTextQueryText, map[string]any{
				"text":                   "dimension check",
				"embeddingProvider":      "azure-openai",
				"embeddingConfiguration": map[string]string{"token": "sk-test", "resource": "my-resource", "model": "text-embedding-3-small"},
			}).
			Return(embeddedVectorRecord(0.5, 0.25, 0.125), nil)

		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		result, err := handler(azureOpenAIEmbeddingCtx(), callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}

		var output search.CheckEmbeddingDimensionsOutput
		if err := json.Unmarshal([]byte(getText(t, result)), &output); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		want := search.CheckEmbeddingDimensionsOutput{
			IndexName: "my_index", Provider: "azure-openai", Model: "text-embedding-3-small",
			ConfiguredDimensions: 3, ActualDimensions: 3, Match: true,
		}
		if output != want {
			t.Errorf("output = %+v, want %+v", output, want)
		}
	})

	t.Run("matching dimensions via a direct HTTP call (openai, no ai.text.embed), using a custom sample text", func(t *testing.T) {
		server := openAIEmbeddingsTestServer(t, []float64{0.5, 0.25, 0.125}, "text-embedding-nomic-embed-text-v1.5", "lm-studio-token")

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, gomock.Any()).
			Return(vectorIndexByNameRecord("VECTOR", 3), nil)

		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider: config.EmbeddingProviderOpenAI,
			Configuration: map[string]string{
				"token": "lm-studio-token", "model": "text-embedding-nomic-embed-text-v1.5", "baseUrl": server.URL,
			},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{
			"indexName": "my_index", "sampleText": "custom sample",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}

		var output search.CheckEmbeddingDimensionsOutput
		if err := json.Unmarshal([]byte(getText(t, result)), &output); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		want := search.CheckEmbeddingDimensionsOutput{
			IndexName: "my_index", Provider: "openai", Model: "text-embedding-nomic-embed-text-v1.5",
			ConfiguredDimensions: 3, ActualDimensions: 3, Match: true,
		}
		if output != want {
			t.Errorf("output = %+v, want %+v", output, want)
		}
	})

	t.Run("mismatching dimensions via a direct HTTP call (openai)", func(t *testing.T) {
		server := openAIEmbeddingsTestServer(t, []float64{0.5, 0.25, 0.125}, "text-embedding-nomic-embed-text-v1.5", "lm-studio-token")

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showVectorIndexByNameQueryText, gomock.Any()).
			Return(vectorIndexByNameRecord("VECTOR", 1536), nil)

		deps := &tools.ToolDependencies{DBService: mockDB}
		handler := search.CheckEmbeddingDimensionsHandler(deps)
		ctx := auth.WithEmbeddingConfig(context.Background(), &config.EmbeddingConfig{
			Provider: config.EmbeddingProviderOpenAI,
			Configuration: map[string]string{
				"token": "lm-studio-token", "model": "text-embedding-nomic-embed-text-v1.5", "baseUrl": server.URL,
			},
		})
		result, err := handler(ctx, callToolRequest(map[string]any{"indexName": "my_index"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}

		var output search.CheckEmbeddingDimensionsOutput
		if err := json.Unmarshal([]byte(getText(t, result)), &output); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if output.Match {
			t.Errorf("expected Match=false for a %d/%d dimension mismatch, got true", output.ActualDimensions, output.ConfiguredDimensions)
		}
		if output.ConfiguredDimensions != 1536 || output.ActualDimensions != 3 {
			t.Errorf("output = %+v, want configuredDimensions=1536 actualDimensions=3", output)
		}
	})
}
