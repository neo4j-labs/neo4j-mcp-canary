// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"testing"

	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestCreateIndexHandler_NilDatabaseService(t *testing.T) {
	deps := &tools.ToolDependencies{DBService: nil}
	handler := cypher.CreateIndexHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"indexType": "RANGE", "entityType": "NODE", "label": "Person", "properties": []string{"email"},
	}))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for nil database service")
	}
}

func TestCreateIndexHandler_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "vector rejected",
			args: map[string]any{"indexType": "VECTOR", "entityType": "NODE", "label": "Person", "properties": []string{"embedding"}},
		},
		{
			name: "fulltext rejected",
			args: map[string]any{"indexType": "FULLTEXT", "entityType": "NODE", "label": "Person", "properties": []string{"title"}},
		},
		{
			name: "unknown index type",
			args: map[string]any{"indexType": "BOGUS", "entityType": "NODE", "label": "Person", "properties": []string{"title"}},
		},
		{
			name: "text with two properties",
			args: map[string]any{"indexType": "TEXT", "entityType": "NODE", "label": "Person", "properties": []string{"name", "email"}},
		},
		{
			name: "point with zero properties",
			args: map[string]any{"indexType": "POINT", "entityType": "NODE", "label": "Person", "properties": []string{}},
		},
		{
			name: "range with zero properties",
			args: map[string]any{"indexType": "RANGE", "entityType": "NODE", "label": "Person", "properties": []string{}},
		},
		{
			name: "lookup with label set",
			args: map[string]any{"indexType": "LOOKUP", "entityType": "NODE", "label": "Person"},
		},
		{
			name: "lookup with relationshipType set",
			args: map[string]any{"indexType": "LOOKUP", "entityType": "RELATIONSHIP", "relationshipType": "REVIEWED"},
		},
		{
			name: "lookup with properties set",
			args: map[string]any{"indexType": "LOOKUP", "entityType": "NODE", "properties": []string{"email"}},
		},
		{
			name: "lookup with invalid entityType",
			args: map[string]any{"indexType": "LOOKUP", "entityType": "EDGE"},
		},
		{
			name: "range node missing label",
			args: map[string]any{"indexType": "RANGE", "entityType": "NODE", "properties": []string{"email"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockDB := db.NewMockService(ctrl) // no EXPECT() calls set: any DB call fails the test

			deps := &tools.ToolDependencies{DBService: mockDB}
			handler := cypher.CreateIndexHandler(deps)
			result, err := handler(context.Background(), requestWithArgs(tt.args))
			if err != nil {
				t.Fatalf("expected no error from handler, got: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatal("expected error result for invalid input")
			}
		})
	}
}

func TestCreateIndexHandler_RangeCompositeHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE RANGE INDEX `person_composite_range` IF NOT EXISTS FOR (n:`Person`) ON (n.`tenantId`, n.`orderId`)"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "person_composite_range"}).
		Return([]*neo4j.Record{
			indexRecord(1, "person_composite_range", "ONLINE", 100.0, "RANGE", "NODE", []string{"Person"}, []string{"tenantId", "orderId"}, "range-1.0", nil),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateIndexHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "person_composite_range", "indexType": "RANGE", "entityType": "NODE", "label": "Person",
		"properties": []string{"tenantId", "orderId"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateIndexHandler_TextHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE TEXT INDEX `person_name_text` IF NOT EXISTS FOR (n:`Person`) ON (n.`name`)"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "person_name_text"}).
		Return([]*neo4j.Record{
			indexRecord(2, "person_name_text", "ONLINE", 100.0, "TEXT", "NODE", []string{"Person"}, []string{"name"}, "text-2.0", nil),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateIndexHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "person_name_text", "indexType": "TEXT", "entityType": "NODE", "label": "Person",
		"properties": []string{"name"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateIndexHandler_LookupNodeHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE LOOKUP INDEX `node_lookup` IF NOT EXISTS FOR (n) ON EACH labels(n)"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "node_lookup"}).
		Return([]*neo4j.Record{
			indexRecord(3, "node_lookup", "ONLINE", 100.0, "LOOKUP", "NODE", []string{}, []string{}, "lookup-1.0", nil),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateIndexHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "node_lookup", "indexType": "LOOKUP", "entityType": "NODE",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateIndexHandler_LookupRelationshipHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE LOOKUP INDEX `rel_lookup` IF NOT EXISTS FOR ()-[r]-() ON EACH type(r)"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "rel_lookup"}).
		Return([]*neo4j.Record{
			indexRecord(4, "rel_lookup", "ONLINE", 100.0, "LOOKUP", "RELATIONSHIP", []string{}, []string{}, "lookup-1.0", nil),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateIndexHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "rel_lookup", "indexType": "LOOKUP", "entityType": "RELATIONSHIP",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}
