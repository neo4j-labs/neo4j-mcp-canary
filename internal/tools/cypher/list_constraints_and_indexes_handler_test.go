// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"errors"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// --- Test helpers ---

// constraintRecord builds a single record as returned by the SHOW
// CONSTRAINTS query used by list-constraints-and-indexes. Pass nil for
// ownedIndex/propertyType to exercise the nullable-column branch.
func constraintRecord(id int64, name, constraintType, entityType string, labelsOrTypes, properties []string, ownedIndex, propertyType any) *neo4j.Record {
	return &neo4j.Record{
		Keys: []string{"id", "name", "type", "entityType", "labelsOrTypes", "properties", "ownedIndex", "propertyType"},
		Values: []any{
			id, name, constraintType, entityType,
			toAnySlice(labelsOrTypes), toAnySlice(properties),
			ownedIndex, propertyType,
		},
	}
}

// indexRecord builds a single record as returned by the SHOW INDEXES query.
// Pass nil for indexProvider/owningConstraint to exercise the
// nullable-column branch.
func indexRecord(id int64, name, state string, populationPercent float64, indexType, entityType string, labelsOrTypes, properties []string, indexProvider, owningConstraint any) *neo4j.Record {
	return &neo4j.Record{
		Keys: []string{"id", "name", "state", "populationPercent", "type", "entityType", "labelsOrTypes", "properties", "indexProvider", "owningConstraint"},
		Values: []any{
			id, name, state, populationPercent, indexType, entityType,
			toAnySlice(labelsOrTypes), toAnySlice(properties),
			indexProvider, owningConstraint,
		},
	}
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func newConstraintsDepsWithMocks(t *testing.T) (*tools.ToolDependencies, *db.MockService, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)
	mockDB := db.NewMockService(ctrl)
	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}
	return deps, mockDB, ctrl
}

// --- Handler-level tests ---

func TestListConstraintsAndIndexesHandler_NilDatabaseService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)

	deps := &tools.ToolDependencies{
		DBService:        nil,
		AnalyticsService: analyticsService,
	}

	handler := cypher.ListConstraintsAndIndexesHandler(deps)
	result, err := handler(context.Background(), &mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for nil database service")
	}
}

func TestListConstraintsAndIndexesHandler_ConstraintsQueryFails(t *testing.T) {
	deps, mockDB, ctrl := newConstraintsDepsWithMocks(t)
	defer ctrl.Finish()

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(1).
		Return(nil, errors.New("connection refused"))

	handler := cypher.ListConstraintsAndIndexesHandler(deps)
	result, err := handler(context.Background(), &mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when constraints query fails")
	}
}

func TestListConstraintsAndIndexesHandler_IndexesQueryFails(t *testing.T) {
	deps, mockDB, ctrl := newConstraintsDepsWithMocks(t)
	defer ctrl.Finish()

	gomock.InOrder(
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("connection refused")),
	)

	handler := cypher.ListConstraintsAndIndexesHandler(deps)
	result, err := handler(context.Background(), &mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when indexes query fails")
	}
}

func TestListConstraintsAndIndexesHandler_PopulatesStructuredContent(t *testing.T) {
	deps, mockDB, ctrl := newConstraintsDepsWithMocks(t)
	defer ctrl.Finish()

	constraintRecords := []*neo4j.Record{
		constraintRecord(3, "person_id_unique", "UNIQUENESS", "NODE", []string{"Person"}, []string{"id"}, "person_id_unique", nil),
		constraintRecord(4, "movie_exists", "NODE_PROPERTY_EXISTENCE", "NODE", []string{"Movie"}, []string{"title"}, nil, nil),
	}
	indexRecords := []*neo4j.Record{
		indexRecord(1, "person_id_unique", "ONLINE", 100.0, "RANGE", "NODE", []string{"Person"}, []string{"id"}, "range-1.0", "person_id_unique"),
		indexRecord(2, "movie_title_fulltext", "POPULATING", 42.5, "FULLTEXT", "NODE", []string{"Movie"}, []string{"title"}, nil, nil),
	}

	gomock.InOrder(
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(constraintRecords, nil),
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).Return(indexRecords, nil),
	)

	handler := cypher.ListConstraintsAndIndexesHandler(deps)
	result, err := handler(context.Background(), &mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	text := getResultText(t, result)
	expected := `{
		"constraints": [
			{
				"id": 3,
				"name": "person_id_unique",
				"type": "UNIQUENESS",
				"entityType": "NODE",
				"labelsOrTypes": ["Person"],
				"properties": ["id"],
				"ownedIndex": "person_id_unique"
			},
			{
				"id": 4,
				"name": "movie_exists",
				"type": "NODE_PROPERTY_EXISTENCE",
				"entityType": "NODE",
				"labelsOrTypes": ["Movie"],
				"properties": ["title"]
			}
		],
		"indexes": [
			{
				"id": 1,
				"name": "person_id_unique",
				"state": "ONLINE",
				"populationPercent": 100.0,
				"type": "RANGE",
				"entityType": "NODE",
				"labelsOrTypes": ["Person"],
				"properties": ["id"],
				"indexProvider": "range-1.0",
				"owningConstraint": "person_id_unique"
			},
			{
				"id": 2,
				"name": "movie_title_fulltext",
				"state": "POPULATING",
				"populationPercent": 42.5,
				"type": "FULLTEXT",
				"entityType": "NODE",
				"labelsOrTypes": ["Movie"],
				"properties": ["title"]
			}
		]
	}`
	assertJSONEquals(t, expected, text)
}
