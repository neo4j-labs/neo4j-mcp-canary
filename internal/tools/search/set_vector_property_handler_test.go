// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func updatedCountRecord(count int64) []*neo4j.Record {
	return []*neo4j.Record{{Keys: []string{"updated"}, Values: []any{count}}}
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

	t.Run("propagates a write failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
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
