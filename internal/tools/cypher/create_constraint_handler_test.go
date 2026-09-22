// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"strings"
	"testing"

	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// constraintRecord and toAnySlice are shared with
// list_constraints_and_indexes_handler_test.go (same cypher_test package).

func requestWithArgs(args map[string]any) *mcpsdk.CallToolRequest {
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: args}}
}

func TestCreateConstraintHandler_NilDatabaseService(t *testing.T) {
	deps := &tools.ToolDependencies{DBService: nil}
	handler := cypher.CreateConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"entityType": "NODE", "label": "Person", "properties": []string{"email"}, "constraintType": "UNIQUENESS",
	}))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for nil database service")
	}
}

func TestCreateConstraintHandler_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "node missing label",
			args: map[string]any{"entityType": "NODE", "properties": []string{"email"}, "constraintType": "UNIQUENESS"},
		},
		{
			name: "relationship with label set",
			args: map[string]any{"entityType": "RELATIONSHIP", "relationshipType": "REVIEWED", "label": "Person", "properties": []string{"rating"}, "constraintType": "UNIQUENESS"},
		},
		{
			name: "property existence with two properties",
			args: map[string]any{"entityType": "NODE", "label": "Person", "properties": []string{"email", "name"}, "constraintType": "PROPERTY_EXISTENCE"},
		},
		{
			name: "property existence with zero properties",
			args: map[string]any{"entityType": "NODE", "label": "Person", "properties": []string{}, "constraintType": "PROPERTY_EXISTENCE"},
		},
		{
			name: "uniqueness with zero properties",
			args: map[string]any{"entityType": "NODE", "label": "Person", "properties": []string{}, "constraintType": "UNIQUENESS"},
		},
		{
			name: "key with zero properties",
			args: map[string]any{"entityType": "NODE", "label": "Person", "properties": []string{}, "constraintType": "KEY"},
		},
		{
			name: "unknown constraint type",
			args: map[string]any{"entityType": "NODE", "label": "Person", "properties": []string{"email"}, "constraintType": "BOGUS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockDB := db.NewMockService(ctrl) // no EXPECT() calls set: any DB call fails the test

			deps := &tools.ToolDependencies{DBService: mockDB}
			handler := cypher.CreateConstraintHandler(deps)
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

func TestCreateConstraintHandler_UniquenessHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE CONSTRAINT `person_email_unique` IF NOT EXISTS FOR (n:`Person`) REQUIRE (n.`email`) IS UNIQUE"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "person_email_unique"}).
		Return([]*neo4j.Record{
			constraintRecord(1, "person_email_unique", "UNIQUENESS", "NODE", []string{"Person"}, []string{"email"}, "person_email_unique", ""),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "person_email_unique", "entityType": "NODE", "label": "Person",
		"properties": []string{"email"}, "constraintType": "UNIQUENESS",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateConstraintHandler_KeyHappyPath_Relationship(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE CONSTRAINT `review_key` IF NOT EXISTS FOR ()-[r:`REVIEWED`]-() REQUIRE (r.`tenantId`, r.`orderId`) IS RELATIONSHIP KEY"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "review_key"}).
		Return([]*neo4j.Record{
			constraintRecord(2, "review_key", "RELATIONSHIP_KEY", "RELATIONSHIP", []string{"REVIEWED"}, []string{"tenantId", "orderId"}, "review_key", ""),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "review_key", "entityType": "RELATIONSHIP", "relationshipType": "REVIEWED",
		"properties": []string{"tenantId", "orderId"}, "constraintType": "KEY",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateConstraintHandler_PropertyExistenceHappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	wantCypher := "CREATE CONSTRAINT `person_email_exists` IF NOT EXISTS FOR (n:`Person`) REQUIRE n.`email` IS NOT NULL"
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), wantCypher, nil).Return(nil, nil)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), map[string]any{"name": "person_email_exists"}).
		Return([]*neo4j.Record{
			constraintRecord(3, "person_email_exists", "PROPERTY_EXISTENCE", "NODE", []string{"Person"}, []string{"email"}, "", "STRING"),
		}, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"name": "person_email_exists", "entityType": "NODE", "label": "Person",
		"properties": []string{"email"}, "constraintType": "PROPERTY_EXISTENCE",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
}

func TestCreateConstraintHandler_GeneratedNameWhenOmitted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := db.NewMockService(ctrl)

	var capturedCypher string
	mockDB.EXPECT().ExecuteWriteQuery(gomock.Any(), gomock.Any(), nil).DoAndReturn(
		func(_ context.Context, cypherStr string, _ map[string]any) ([]*neo4j.Record, error) {
			capturedCypher = cypherStr
			return nil, nil
		})
	mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		[]*neo4j.Record{constraintRecord(4, "mcp_generated", "UNIQUENESS", "NODE", []string{"Person"}, []string{"email"}, "mcp_generated", "")}, nil,
	)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := cypher.CreateConstraintHandler(deps)
	result, err := handler(context.Background(), requestWithArgs(map[string]any{
		"entityType": "NODE", "label": "Person", "properties": []string{"email"}, "constraintType": "UNIQUENESS",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}
	if capturedCypher == "" {
		t.Fatal("expected ExecuteWriteQuery to have been called")
	}
	if !strings.Contains(capturedCypher, "CREATE CONSTRAINT `mcp_") {
		t.Errorf("expected generated mcp_ name in cypher, got: %s", capturedCypher)
	}
}
