// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

const lookupIndexEntityQuery = "SHOW INDEXES YIELD name, entityType, labelsOrTypes WHERE name = $name"

func indexEntityRecord(entityType string, labelsOrTypes []string) []*neo4j.Record {
	labels := make([]any, len(labelsOrTypes))
	for i, l := range labelsOrTypes {
		labels[i] = l
	}
	return []*neo4j.Record{
		{Keys: []string{"name", "entityType", "labelsOrTypes"}, Values: []any{"doc_embeddings", entityType, labels}},
	}
}

func callToolRequest(args map[string]any) *mcpsdk.CallToolRequest {
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: args}}
}

func getText(t *testing.T, result *mcpsdk.CallToolResult) string {
	t.Helper()
	text, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return text.Text
}

func TestVectorSearchHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.VectorSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName": "doc_embeddings", "queryVector": []any{0.1, 0.2}, "topK": 5,
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
			{"missing indexName", map[string]any{"queryVector": []any{0.1}, "topK": 5}},
			{"empty queryVector", map[string]any{"indexName": "doc_embeddings", "queryVector": []any{}, "topK": 5}},
			{"zero topK", map[string]any{"indexName": "doc_embeddings", "queryVector": []any{0.1}, "topK": 0}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockDB := db.NewMockService(ctrl)
				deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
				handler := search.VectorSearchHandler(deps)
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

	t.Run("index lookup failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, map[string]any{"name": "doc_embeddings"}).
			Return(nil, errors.New("boom"))

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.VectorSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName": "doc_embeddings", "queryVector": []any{0.1, 0.2}, "topK": 5,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("rejects an invalid filter operator before running any search", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, gomock.Any()).
			Return(indexEntityRecord("NODE", []string{"Document"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.VectorSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName":   "doc_embeddings",
			"queryVector": []any{0.1, 0.2},
			"topK":        5,
			"filters":     []any{map[string]any{"property": "tenantId", "operator": "!=", "value": "acme"}},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("successful search builds the expected query and params", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, map[string]any{"name": "doc_embeddings"}).
			Return(indexEntityRecord("NODE", []string{"Document"}), nil)

		wantQuery := "MATCH (n:`Document`)\n" +
			"  SEARCH n IN (\n" +
			"    VECTOR INDEX `doc_embeddings`\n" +
			"    FOR $queryVector\n" +
			"    WHERE n.`tenantId` = $f0\n" +
			"    LIMIT $topK\n" +
			"  ) SCORE AS score\n" +
			"RETURN n AS entity, score"
		wantParams := map[string]any{
			"queryVector": []float64{0.1, 0.2},
			"topK":        5,
			"f0":          "acme",
		}
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), wantQuery, wantParams).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.VectorSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName":   "doc_embeddings",
			"queryVector": []any{0.1, 0.2},
			"topK":        5,
			"filters":     []any{map[string]any{"property": "tenantId", "operator": "=", "value": "acme"}},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("returnProperties uses a map projection", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, gomock.Any()).
			Return(indexEntityRecord("NODE", []string{"Document"}), nil)

		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, query string, _ map[string]any) ([]*neo4j.Record, error) {
				if !strings.Contains(query, "RETURN n{.`title`} AS entity, score") {
					t.Errorf("expected map-projection RETURN clause, got query: %s", query)
				}
				return []*neo4j.Record{}, nil
			})
		mockDB.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.VectorSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName":        "doc_embeddings",
			"queryVector":      []any{0.1, 0.2},
			"topK":             5,
			"returnProperties": []any{"title"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})
}
