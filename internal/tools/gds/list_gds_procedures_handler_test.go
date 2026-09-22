// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package gds_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/gds"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestListGdsProceduresHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("successful list-gds-procedures", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Nil()).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().
			Neo4jRecordsToJSON(gomock.Any()).
			Return("[]", nil)

		deps := &tools.ToolDependencies{
			DBService: mockDB,
		}

		handler := gds.ListGdsProceduresHandler(deps)
		request := &mcpsdk.CallToolRequest{}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if result == nil || result.IsError {
			t.Error("Expected success result")
		}
	})

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{
			DBService: nil,
		}

		handler := gds.ListGdsProceduresHandler(deps)
		request := &mcpsdk.CallToolRequest{}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for nil database service")
		}
	})

	t.Run("database query execution failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Nil()).
			Return(nil, errors.New("Invalid Cypher"))

		deps := &tools.ToolDependencies{
			DBService: mockDB,
		}

		handler := gds.ListGdsProceduresHandler(deps)
		request := &mcpsdk.CallToolRequest{}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for query execution failure")
		}
	})

	t.Run("JSON formatting failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)

		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Nil()).
			Return([]*neo4j.Record{}, nil)
		mockDB.EXPECT().
			Neo4jRecordsToJSON(gomock.Any()).
			Return("", errors.New("JSON marshaling failed"))

		deps := &tools.ToolDependencies{
			DBService: mockDB,
		}

		handler := gds.ListGdsProceduresHandler(deps)
		request := &mcpsdk.CallToolRequest{}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for JSON formatting failure")
		}
	})
}

func TestListGdsProceduresHandler_PopulatesStructuredContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDB := db.NewMockService(ctrl)
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Nil()).
		Return([]*neo4j.Record{}, nil)
	canonicalJSON := `[{"name":"gds.pageRank.stream","description":"PageRank","signature":"sig","type":"procedure"}]`
	wantWrapped := `{"procedures":` + canonicalJSON + `}`
	mockDB.EXPECT().Neo4jRecordsToJSON(gomock.Any()).Return(canonicalJSON, nil)

	deps := &tools.ToolDependencies{DBService: mockDB}
	handler := gds.ListGdsProceduresHandler(deps)

	result, err := handler(context.Background(), &mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if text.Text != wantWrapped {
		t.Errorf("Content text = %q, want %q (default OutputFormat should pass JSON through unchanged, wrapped in a \"procedures\" object)", text.Text, wantWrapped)
	}

	structured, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("expected StructuredContent to be json.RawMessage, got %T", result.StructuredContent)
	}
	if string(structured) != wantWrapped {
		t.Errorf("StructuredContent = %q, want %q", string(structured), wantWrapped)
	}
}
