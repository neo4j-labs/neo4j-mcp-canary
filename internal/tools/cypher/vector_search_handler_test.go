// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"errors"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestVectorSearchHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	vectorSearchQuery := "CALL db.index.vector.queryNodes($indexName, $topK, $queryVector)\nYIELD node, score\nRETURN node, score"

	t.Run("successful vector search", func(t *testing.T) {
		queryVector := []float64{0.1, 0.2, 0.3}
		expectedParams := map[string]any{
			"indexName":   "my-vector-index",
			"topK":       10,
			"queryVector": queryVector,
		}

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), vectorSearchQuery, expectedParams).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().
			Neo4jRecordsToJSON(gomock.Any()).
			Return(`[{"node": {"name": "Alice"}, "score": 0.95}]`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "my-vector-index",
					"queryVector": []any{0.1, 0.2, 0.3},
					"topK":       float64(10),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Errorf("expected success, got error result")
		}
	})

	t.Run("returns error when indexName is empty", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "",
					"queryVector": []any{0.1, 0.2, 0.3},
					"topK":       float64(10),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("expected error result for empty indexName")
		}
	})

	t.Run("returns error when queryVector is empty", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "my-vector-index",
					"queryVector": []any{},
					"topK":       float64(10),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("expected error result for empty queryVector")
		}
	})

	t.Run("defaults topK to 10 when zero or negative", func(t *testing.T) {
		queryVector := []float64{0.1, 0.2, 0.3}
		expectedParams := map[string]any{
			"indexName":   "my-vector-index",
			"topK":       10,
			"queryVector": queryVector,
		}

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), vectorSearchQuery, expectedParams).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().
			Neo4jRecordsToJSON(gomock.Any()).
			Return(`[]`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "my-vector-index",
					"queryVector": []any{0.1, 0.2, 0.3},
					"topK":       float64(0),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Errorf("expected success, got error result")
		}
	})

	t.Run("returns error when database query fails", func(t *testing.T) {
		queryVector := []float64{0.1, 0.2, 0.3}
		expectedParams := map[string]any{
			"indexName":   "nonexistent-index",
			"topK":       10,
			"queryVector": queryVector,
		}

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), vectorSearchQuery, expectedParams).
			Return(nil, errors.New("There is no such vector index: nonexistent-index"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "nonexistent-index",
					"queryVector": []any{0.1, 0.2, 0.3},
					"topK":       float64(10),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("expected error result for nonexistent index")
		}
	})

	t.Run("returns error when database service is nil", func(t *testing.T) {
		deps := &tools.ToolDependencies{
			DBService:        nil,
			AnalyticsService: analyticsService,
		}

		handler := cypher.VectorSearchHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"indexName":   "my-vector-index",
					"queryVector": []any{0.1, 0.2, 0.3},
					"topK":       float64(10),
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("expected error result for nil database service")
		}
	})
}
