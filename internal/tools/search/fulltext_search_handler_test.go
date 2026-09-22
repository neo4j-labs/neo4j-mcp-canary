// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"errors"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestFullTextSearchHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.FullTextSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName": "doc_fulltext", "queryString": "neo4j", "topK": 5,
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
			{"missing indexName", map[string]any{"queryString": "neo4j", "topK": 5}},
			{"empty queryString", map[string]any{"indexName": "doc_fulltext", "queryString": "", "topK": 5}},
			{"zero topK", map[string]any{"indexName": "doc_fulltext", "queryString": "neo4j", "topK": 0}},
			{"negative skip", map[string]any{"indexName": "doc_fulltext", "queryString": "neo4j", "topK": 5, "skip": -1}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockDB := db.NewMockService(ctrl)
				deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
				handler := search.FullTextSearchHandler(deps)
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
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, map[string]any{"name": "doc_fulltext"}).
			Return(nil, errors.New("boom"))

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.FullTextSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName": "doc_fulltext", "queryString": "neo4j", "topK": 5,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("successful search with analyzer and skip builds the expected query", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, map[string]any{"name": "doc_fulltext"}).
			Return(indexEntityRecord("NODE", []string{"Document", "Article"}), nil)

		wantQuery := "MATCH (n:`Document`|`Article`)\n" +
			"  SEARCH n IN (\n" +
			"    FULLTEXT INDEX `doc_fulltext`\n" +
			"    FOR $queryString\n" +
			"    WITH ANALYZER $analyzer\n" +
			"    SKIP $skip\n" +
			"    LIMIT $topK\n" +
			"  ) SCORE AS score\n" +
			"RETURN n AS entity, score"
		wantParams := map[string]any{
			"queryString": "neo4j",
			"topK":        5,
			"analyzer":    "english",
			"skip":        20,
		}
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), wantQuery, wantParams).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.FullTextSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName":   "doc_fulltext",
			"queryString": "neo4j",
			"topK":        5,
			"analyzer":    "english",
			"skip":        20,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("successful search without optional fields omits their clauses", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), lookupIndexEntityQuery, gomock.Any()).
			Return(indexEntityRecord("RELATIONSHIP", []string{"REVIEWED"}), nil)

		wantQuery := "MATCH ()-[r:`REVIEWED`]-()\n" +
			"  SEARCH r IN (\n" +
			"    FULLTEXT INDEX `doc_fulltext`\n" +
			"    FOR $queryString\n" +
			"    LIMIT $topK\n" +
			"  ) SCORE AS score\n" +
			"RETURN r AS entity, score"
		wantParams := map[string]any{"queryString": "neo4j", "topK": 5}
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), wantQuery, wantParams).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.FullTextSearchHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"indexName":   "doc_fulltext",
			"queryString": "neo4j",
			"topK":        5,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})
}
