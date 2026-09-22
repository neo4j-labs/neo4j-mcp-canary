// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search_test

import (
	"context"
	"strings"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestCreateFullTextIndexHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.CreateFullTextIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "labels": []any{"Document"}, "properties": []any{"title"},
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
			{"missing entityType", map[string]any{"labels": []any{"Document"}, "properties": []any{"title"}}},
			{"invalid entityType", map[string]any{"entityType": "EDGE", "labels": []any{"Document"}, "properties": []any{"title"}}},
			{"NODE with empty labels", map[string]any{"entityType": "NODE", "properties": []any{"title"}}},
			{"NODE with relationshipTypes set", map[string]any{"entityType": "NODE", "labels": []any{"Document"}, "relationshipTypes": []any{"REL"}, "properties": []any{"title"}}},
			{"RELATIONSHIP with empty relationshipTypes", map[string]any{"entityType": "RELATIONSHIP", "properties": []any{"title"}}},
			{"RELATIONSHIP with labels set", map[string]any{"entityType": "RELATIONSHIP", "relationshipTypes": []any{"REVIEWED"}, "labels": []any{"Document"}, "properties": []any{"title"}}},
			{"empty properties", map[string]any{"entityType": "NODE", "labels": []any{"Document"}, "properties": []any{}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockDB := db.NewMockService(ctrl)
				deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
				handler := search.CreateFullTextIndexHandler(deps)
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

	t.Run("creates without analyzer or eventuallyConsistent omits the OPTIONS clause", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		wantQuery := "CREATE FULLTEXT INDEX `ft_idx` IF NOT EXISTS FOR (n:`Document`|`Article`) ON EACH [n.`title`, n.`body`]"
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantQuery, nil).Return(nil, nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, map[string]any{"name": "ft_idx"}).
			Return(fakeIndexRow("ft_idx", "FULLTEXT", "NODE", []string{"Document", "Article"}, []string{"title", "body"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateFullTextIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"name": "ft_idx", "entityType": "NODE", "labels": []any{"Document", "Article"}, "properties": []any{"title", "body"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("creates with analyzer and eventuallyConsistent set includes both OPTIONS keys", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		wantQuery := "CREATE FULLTEXT INDEX `ft_idx2` IF NOT EXISTS FOR ()-[r:`REVIEWED`]-() ON EACH [r.`comment`]\n" +
			"OPTIONS { indexConfig: { `fulltext.analyzer`: 'english', `fulltext.eventually_consistent`: true } }"
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantQuery, nil).Return(nil, nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, map[string]any{"name": "ft_idx2"}).
			Return(fakeIndexRow("ft_idx2", "FULLTEXT", "RELATIONSHIP", []string{"REVIEWED"}, []string{"comment"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateFullTextIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"name":                 "ft_idx2",
			"entityType":           "RELATIONSHIP",
			"relationshipTypes":    []any{"REVIEWED"},
			"properties":           []any{"comment"},
			"analyzer":             "english",
			"eventuallyConsistent": true,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("generates a name when none is supplied", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteWriteQuery(gomock.Any(), gomock.Any(), nil).
			DoAndReturn(func(_ context.Context, query string, _ map[string]any) ([]*neo4j.Record, error) {
				if !strings.Contains(query, "`mcp_") {
					t.Errorf("expected a generated mcp_ name in query: %s", query)
				}
				return nil, nil
			})
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, gomock.Any()).
			Return(fakeIndexRow("mcp_generated", "FULLTEXT", "NODE", []string{"Document"}, []string{"title"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateFullTextIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "labels": []any{"Document"}, "properties": []any{"title"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})
}
