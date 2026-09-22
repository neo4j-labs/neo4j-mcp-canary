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

func TestDropConstraintHandler_NilDatabaseService(t *testing.T) {
	deps := &tools.ToolDependencies{DBService: nil}
	handler := cypher.DropConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{"name": "person_email_unique"}))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for nil database service")
	}
}

func TestDropConstraintHandler_EmptyName(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl) // no EXPECT() calls set: empty name must short-circuit before any DB call

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.DropConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{"name": ""}))
	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for empty name")
	}
}

func TestDropConstraintHandler_Existed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "person_email_unique"}).
		Return([]*neo4j.Record{{Keys: []string{"name"}, Values: []any{"person_email_unique"}}}, nil)
	mockDB.EXPECT().
		ExecuteWriteQuery(gomock.Any(), "DROP CONSTRAINT `person_email_unique` IF EXISTS", nil).
		Return(nil, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.DropConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{"name": "person_email_unique"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
	assertJSONEquals(t, `{"name": "person_email_unique", "existed": true}`, getResultText(t, result))
}

func TestDropConstraintHandler_DidNotExist(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "missing_constraint"}).
		Return([]*neo4j.Record{}, nil)
	mockDB.EXPECT().
		ExecuteWriteQuery(gomock.Any(), "DROP CONSTRAINT `missing_constraint` IF EXISTS", nil).
		Return(nil, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.DropConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{"name": "missing_constraint"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
	assertJSONEquals(t, `{"name": "missing_constraint", "existed": false}`, getResultText(t, result))
}
