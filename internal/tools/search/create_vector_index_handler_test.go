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

const showIndexByNameQuery = "SHOW INDEXES YIELD id, name, state, populationPercent, type, entityType, labelsOrTypes, properties, indexProvider, owningConstraint WHERE name = $name"

// fakeIndexRow mirrors the internal search package's own indexInfoRecord
// test helper (internal/tools/search/index_lookup_test.go), duplicated here
// because that helper lives in the internal package and this file is in the
// external search_test package.
func fakeIndexRow(name, indexType, entityType string, labelsOrTypes, properties []string) []*neo4j.Record {
	toAny := func(ss []string) []any {
		out := make([]any, len(ss))
		for i, s := range ss {
			out[i] = s
		}
		return out
	}
	return []*neo4j.Record{{
		Keys: []string{"id", "name", "state", "populationPercent", "type", "entityType", "labelsOrTypes", "properties", "indexProvider", "owningConstraint"},
		Values: []any{
			int64(1), name, "ONLINE", float64(100), indexType, entityType,
			toAny(labelsOrTypes), toAny(properties), "vector-2.0", nil,
		},
	}}
}

func TestCreateVectorIndexHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := search.CreateVectorIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536,
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
			{"missing entityType", map[string]any{"label": "Document", "property": "embedding", "dimensions": 1536}},
			{"invalid entityType", map[string]any{"entityType": "EDGE", "label": "Document", "property": "embedding", "dimensions": 1536}},
			{"NODE with neither label nor relationshipType", map[string]any{"entityType": "NODE", "property": "embedding", "dimensions": 1536}},
			{"NODE with both label and relationshipType", map[string]any{"entityType": "NODE", "label": "Document", "relationshipType": "REL", "property": "embedding", "dimensions": 1536}},
			{"RELATIONSHIP without relationshipType", map[string]any{"entityType": "RELATIONSHIP", "property": "embedding", "dimensions": 1536}},
			// Regression: entityType NODE with only relationshipType set (label
			// left empty) used to pass the old "not both empty, not both set"
			// check and silently resolve to an empty label, producing a broken
			// `(n:``)` pattern instead of a clear error. Same for the mirrored
			// RELATIONSHIP case below.
			{"NODE with only relationshipType set (mismatched field)", map[string]any{"entityType": "NODE", "relationshipType": "REL", "property": "embedding", "dimensions": 1536}},
			{"RELATIONSHIP with only label set (mismatched field)", map[string]any{"entityType": "RELATIONSHIP", "label": "Document", "property": "embedding", "dimensions": 1536}},
			{"missing property", map[string]any{"entityType": "NODE", "label": "Document", "dimensions": 1536}},
			{"dimensions too low", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 0}},
			{"dimensions too high", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 4097}},
			{"invalid similarityFunction", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536, "similarityFunction": "manhattan"}},
			{"invalid quantizationType", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536, "quantizationType": "lossy"}},
			{"negative hnswM", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536, "hnswM": -1}},
			{"negative hnswEfConstruction", map[string]any{"entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536, "hnswEfConstruction": -1}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				mockDB := db.NewMockService(ctrl)
				deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
				handler := search.CreateVectorIndexHandler(deps)
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

	t.Run("creates with defaults and applies all 5 OPTIONS keys", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		wantQuery := "CREATE VECTOR INDEX `vec_idx` IF NOT EXISTS FOR (n:`Document`) ON n.`embedding`\n" +
			"OPTIONS { indexConfig: { `vector.dimensions`: 1536, `vector.similarity_function`: 'cosine', `vector.quantization.type`: 'binary', `vector.hnsw.m`: 16, `vector.hnsw.ef_construction`: 100 } }"
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantQuery, nil).Return(nil, nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, map[string]any{"name": "vec_idx"}).
			Return(fakeIndexRow("vec_idx", "VECTOR", "NODE", []string{"Document"}, []string{"embedding"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateVectorIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"name": "vec_idx", "entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("creates with filterableProperties, custom config, and relationship entity", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		wantQuery := "CREATE VECTOR INDEX `vec_idx2` IF NOT EXISTS FOR ()-[r:`REVIEWED`]-() ON r.`embedding`\n" +
			"WITH [r.`chunkId`, r.`docId`]\n" +
			"OPTIONS { indexConfig: { `vector.dimensions`: 768, `vector.similarity_function`: 'euclidean', `vector.quantization.type`: 'scalar', `vector.hnsw.m`: 32, `vector.hnsw.ef_construction`: 200 } }"
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantQuery, nil).Return(nil, nil)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, map[string]any{"name": "vec_idx2"}).
			Return(fakeIndexRow("vec_idx2", "VECTOR", "RELATIONSHIP", []string{"REVIEWED"}, []string{"embedding"}), nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateVectorIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"name":                 "vec_idx2",
			"entityType":           "RELATIONSHIP",
			"relationshipType":     "REVIEWED",
			"property":             "embedding",
			"dimensions":           768,
			"similarityFunction":   "EUCLIDEAN",
			"quantizationType":     "scalar",
			"hnswM":                32,
			"hnswEfConstruction":   200,
			"filterableProperties": []any{"chunkId", "docId"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	t.Run("propagates a create failure without fetching back", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), gomock.Any(), nil).Return(nil, context.DeadlineExceeded)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := search.CreateVectorIndexHandler(deps)
		result, err := handler(context.Background(), callToolRequest(map[string]any{
			"name": "vec_idx3", "entityType": "NODE", "label": "Document", "property": "embedding", "dimensions": 1536,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})
}
