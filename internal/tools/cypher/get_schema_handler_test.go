// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// --- Test helpers ---

// nodeRecord creates a *neo4j.Record matching the shape returned by db.schema.nodeTypeProperties().
func nodeRecord(labels []string, propName string, propTypes []string) *neo4j.Record {
	labelsAny := make([]any, len(labels))
	for i, l := range labels {
		labelsAny[i] = l
	}
	typesAny := make([]any, len(propTypes))
	for i, t := range propTypes {
		typesAny[i] = t
	}
	return &neo4j.Record{
		Keys:   []string{"nodeLabels", "propertyName", "propertyTypes"},
		Values: []any{labelsAny, propName, typesAny},
	}
}

// relRecord creates a *neo4j.Record matching the shape returned by db.schema.relTypeProperties().
// relType should be in the ":`TYPE`" format that the procedure returns.
func relRecord(relType string, propName string, propTypes []string) *neo4j.Record {
	typesAny := make([]any, len(propTypes))
	for i, t := range propTypes {
		typesAny[i] = t
	}
	return &neo4j.Record{
		Keys:   []string{"relType", "propertyName", "propertyTypes"},
		Values: []any{relType, propName, typesAny},
	}
}

// relRecordNoProps creates a *neo4j.Record for a relationship type with no properties.
// db.schema.relTypeProperties() returns null for propertyName and propertyTypes in this case.
func relRecordNoProps(relType string) *neo4j.Record {
	return &neo4j.Record{
		Keys:   []string{"relType", "propertyName", "propertyTypes"},
		Values: []any{relType, nil, nil},
	}
}

// nodeRecordNoProps creates a *neo4j.Record for a node label with no properties.
// db.schema.nodeTypeProperties() returns null for propertyName and propertyTypes in this case.
func nodeRecordNoProps(labels []string) *neo4j.Record {
	labelsAny := make([]any, len(labels))
	for i, l := range labels {
		labelsAny[i] = l
	}
	return &neo4j.Record{
		Keys:   []string{"nodeLabels", "propertyName", "propertyTypes"},
		Values: []any{labelsAny, nil, nil},
	}
}

// patternRecord creates a *neo4j.Record matching the relationship patterns query result.
func patternRecord(from, relType, to string) *neo4j.Record {
	return &neo4j.Record{
		Keys:   []string{"fromLabel", "relType", "toLabel"},
		Values: []any{from, relType, to},
	}
}

// indexRecord creates a *neo4j.Record matching the SHOW INDEXES query result.
func indexRecord(name, indexType, entityType string, labelsOrTypes, properties []string, options map[string]any) *neo4j.Record {
	labelsAny := make([]any, len(labelsOrTypes))
	for i, l := range labelsOrTypes {
		labelsAny[i] = l
	}
	propsAny := make([]any, len(properties))
	for i, p := range properties {
		propsAny[i] = p
	}
	return &neo4j.Record{
		Keys:   []string{"name", "type", "entityType", "labelsOrTypes", "properties", "state", "options"},
		Values: []any{name, indexType, entityType, labelsAny, propsAny, "ONLINE", options},
	}
}

// vectorOptions returns the nested options map for a vector index.
func vectorOptions(dimensions int64, similarityFunction string) map[string]any {
	return map[string]any{
		"indexConfig": map[string]any{
			"vector.dimensions":          dimensions,
			"vector.similarity_function": similarityFunction,
		},
	}
}

// --- Expectations helpers ---

// expectFourQueries sets up the standard 4 sequential ExecuteReadQuery expectations
// that the handler makes: nodeProperties, relProperties, patterns, indexes.
func expectFourQueries(mockDB *db.MockService,
	nodeRecords, relRecords, patternRecords, indexRecords []*neo4j.Record,
	nodeErr, relErr, patternErr, indexErr error,
) {
	gomock.InOrder(
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nodeRecords, nodeErr),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(relRecords, relErr),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(patternRecords, patternErr),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(indexRecords, indexErr),
	)
}

// getResultText extracts the text content from a successful tool result.
func getResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatal("expected result content to be TextContent")
	}
	return textContent.Text
}

// assertJSONEquals compares two JSON strings for structural equality.
func assertJSONEquals(t *testing.T, expected, actual string) {
	t.Helper()
	var expectedData, actualData any
	if err := json.Unmarshal([]byte(expected), &expectedData); err != nil {
		t.Fatalf("failed to unmarshal expected JSON: %v\nJSON: %s", err, expected)
	}
	if err := json.Unmarshal([]byte(actual), &actualData); err != nil {
		t.Fatalf("failed to unmarshal actual JSON: %v\nJSON: %s", err, actual)
	}
	expectedFormatted, _ := json.MarshalIndent(expectedData, "", "  ")
	actualFormatted, _ := json.MarshalIndent(actualData, "", "  ")
	if string(expectedFormatted) != string(actualFormatted) {
		t.Errorf("JSON mismatch.\nExpected:\n%s\nGot:\n%s", string(expectedFormatted), string(actualFormatted))
	}
}

// --- Sampling record helpers ---

// sampledNodeRecord creates a record matching the sampling query result format.
// Columns: label (string), key (string), types (list of strings from valueType()).
func sampledNodeRecord(label, key string, types []string) *neo4j.Record {
	typesAny := make([]any, len(types))
	for i, t := range types {
		typesAny[i] = t
	}
	return &neo4j.Record{
		Keys:   []string{"label", "key", "types"},
		Values: []any{label, key, typesAny},
	}
}

// sampledRelRecord creates a record matching the sampling query result format.
// Columns: relType (string), key (string), types (list of strings from valueType()).
func sampledRelRecord(relType, key string, types []string) *neo4j.Record {
	typesAny := make([]any, len(types))
	for i, t := range types {
		typesAny[i] = t
	}
	return &neo4j.Record{
		Keys:   []string{"relType", "key", "types"},
		Values: []any{relType, key, typesAny},
	}
}

// --- Handler-level tests ---

func TestGetSchemaHandler_NilDatabaseService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)

	deps := &tools.ToolDependencies{
		DBService:        nil,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for nil database service")
	}
}

func TestGetSchemaHandler_NodePropertiesQueryFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Only the first query is made before the handler returns an error
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when nodeProperties query fails")
	}
}

func TestGetSchemaHandler_RelPropertiesQueryFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	gomock.InOrder(
		// nodeProperties succeeds
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
			}, nil),
		// relProperties fails
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("permission denied")),
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when relProperties query fails")
	}
}

func TestGetSchemaHandler_EmptyDatabase(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Empty database: both node and rel queries return no records.
	// The handler short-circuits before making the patterns and indexes queries.
	gomock.InOrder(
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	text := getResultText(t, result)
	expected := "The get-schema tool executed successfully; however, since the Neo4j instance contains no data, no schema information was returned."
	if text != expected {
		t.Errorf("expected empty database message, got: %s", text)
	}
}

func TestGetSchemaHandler_PatternsQueryGracefulDegradation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	expectFourQueries(mockDB,
		[]*neo4j.Record{
			nodeRecord([]string{"Movie"}, "title", []string{"String"}),
		},
		[]*neo4j.Record{
			relRecord(":`ACTED_IN`", "roles", []string{"StringArray"}),
		},
		nil, // patterns result (ignored because of error)
		[]*neo4j.Record{},
		nil, nil,
		errors.New("unsupported query"), // patterns error
		nil,
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success - patterns failure should degrade gracefully")
	}

	// Relationship should still appear (from relProperties) but without from/to
	assertJSONEquals(t, `{
		"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}],
		"relationships": [{"type": "ACTED_IN", "properties": {"roles": "LIST<STRING>"}}]
	}`, getResultText(t, result))
}

// --- Bloom filtering integration test with realistic data ---

func TestGetSchemaProcessing_BloomNodesFullyExcluded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Simulates a real database with Bloom metadata alongside user data
	expectFourQueries(mockDB,
		[]*neo4j.Record{
			nodeRecord([]string{"Article"}, "title", []string{"String"}),
			nodeRecord([]string{"Article"}, "id", []string{"String"}),
			nodeRecord([]string{"Organization"}, "name", []string{"String"}),
			nodeRecord([]string{"_Bloom_Perspective_"}, "id", []string{"String"}),
			nodeRecord([]string{"_Bloom_Perspective_"}, "name", []string{"String"}),
			nodeRecord([]string{"_Bloom_Perspective_"}, "data", []string{"String"}),
			nodeRecord([]string{"_Bloom_Perspective_"}, "roles", []string{"StringArray"}),
			nodeRecord([]string{"_Bloom_Scene_"}, "id", []string{"String"}),
			nodeRecord([]string{"_Bloom_Scene_"}, "name", []string{"String"}),
			nodeRecord([]string{"_Bloom_Scene_"}, "visualisation", []string{"String"}),
			nodeRecord([]string{"_Bloom_Scene_"}, "style", []string{"String"}),
			nodeRecord([]string{"_Bloom_Scene_"}, "nodes", []string{"String"}),
		},
		[]*neo4j.Record{
			relRecord(":`MENTIONS`", "count", []string{"Long"}),
			relRecord(":`_Bloom_HAS_SCENE_`", "order", []string{"Long"}),
		},
		[]*neo4j.Record{
			patternRecord("Article", "MENTIONS", "Organization"),
			patternRecord("_Bloom_Perspective_", "_Bloom_HAS_SCENE_", "_Bloom_Scene_"),
		},
		[]*neo4j.Record{
			indexRecord("article-id", "RANGE", "NODE",
				[]string{"Article"}, []string{"id"}, map[string]any{}),
			indexRecord("bloom-perspective-id", "RANGE", "NODE",
				[]string{"_Bloom_Perspective_"}, []string{"id"}, map[string]any{}),
		},
		nil, nil, nil, nil,
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	// Bloom nodes, relationships, and indexes should all be absent
	assertJSONEquals(t, `{
		"nodes": [
			{"label": "Article", "properties": {"title": "STRING", "id": "STRING"}},
			{"label": "Organization", "properties": {"name": "STRING"}}
		],
		"relationships": [
			{"type": "MENTIONS", "from": "Article", "to": "Organization", "properties": {"count": "INTEGER"}}
		],
		"indexes": [
			{"name": "article-id", "type": "RANGE", "entityType": "NODE", "labelsOrTypes": ["Article"], "properties": ["id"]}
		]
	}`, getResultText(t, result))
}

// --- Fallback (sampling) tests ---

func TestGetSchemaHandler_FallbackOnTimeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Expect the schema timeout fallback analytics event
	analyticsService.EXPECT().IsEnabled().Return(true)
	analyticsService.EXPECT().NewSchemaTimeoutFallbackEvent(gomock.Any(), gomock.Eq(100))
	analyticsService.EXPECT().EmitEvent(gomock.Any())

	// The primary nodeProperties query fails (simulating timeout on a large graph).
	// Then the fallback sampling queries run and succeed.
	gomock.InOrder(
		// 1. Primary nodeProperties query fails
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("context deadline exceeded")),
		// 2. Fallback: sample node properties
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				sampledNodeRecord("Movie", "title", []string{"STRING"}),
				sampledNodeRecord("Movie", "released", []string{"INTEGER"}),
				sampledNodeRecord("Person", "name", []string{"STRING"}),
			}, nil),
		// 3. Fallback: sample relationship properties
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				sampledRelRecord("ACTED_IN", "roles", []string{"LIST<STRING>"}),
			}, nil),
		// 4. Fallback: sample relationship patterns
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				patternRecord("Person", "ACTED_IN", "Movie"),
			}, nil),
		// 5. Fallback: indexes (fast metadata query)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaTimeout:    1 * time.Nanosecond, // Ultra-short timeout to force fallback
		SchemaSampleSize: 100,
	}

	// Sleep to ensure the timeout context is expired before the handler checks it
	time.Sleep(time.Millisecond)

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	assertJSONEquals(t, `{
		"nodes": [
			{"label": "Movie", "properties": {"title": "STRING", "released": "INTEGER"}},
			{"label": "Person", "properties": {"name": "STRING"}}
		],
		"relationships": [
			{"type": "ACTED_IN", "from": "Person", "to": "Movie", "properties": {"roles": "LIST<STRING>"}}
		]
	}`, getResultText(t, result))
}

func TestGetSchemaHandler_FallbackTemporalTypeNormalization(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Expect the schema timeout fallback analytics event
	analyticsService.EXPECT().IsEnabled().Return(true)
	analyticsService.EXPECT().NewSchemaTimeoutFallbackEvent(gomock.Any(), gomock.Eq(100))
	analyticsService.EXPECT().EmitEvent(gomock.Any())

	// Primary query times out, fallback uses valueType() which returns space-separated temporal types.
	gomock.InOrder(
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("context deadline exceeded")),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				sampledNodeRecord("Event", "createdAt", []string{"ZONED DATETIME"}),
				sampledNodeRecord("Event", "localTime", []string{"LOCAL TIME"}),
				sampledNodeRecord("Event", "localDt", []string{"LOCAL DATETIME"}),
				sampledNodeRecord("Event", "zonedTime", []string{"ZONED TIME"}),
			}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaTimeout:    1 * time.Nanosecond,
		SchemaSampleSize: 100,
	}

	time.Sleep(time.Millisecond)

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	// Verify that valueType() temporal types are normalized to the underscore format
	assertJSONEquals(t, `{
		"nodes": [{
			"label": "Event",
			"properties": {
				"createdAt": "DATE_TIME",
				"localTime": "LOCAL_TIME",
				"localDt": "LOCAL_DATE_TIME",
				"zonedTime": "ZONED_TIME"
			}
		}]
	}`, getResultText(t, result))
}

func TestGetSchemaHandler_NoFallbackWhenTimeoutDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// With SchemaTimeout = 0, no timeout is applied. A query failure is treated as a hard error.
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaTimeout:    0, // Timeout disabled
		SchemaSampleSize: 100,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when timeout is disabled and query fails")
	}
}

func TestGetSchemaHandler_PrimarySucceedsWithinTimeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Primary queries succeed within the timeout — fallback should NOT be triggered.
	expectFourQueries(mockDB,
		[]*neo4j.Record{
			nodeRecord([]string{"Movie"}, "title", []string{"String"}),
		},
		[]*neo4j.Record{},
		[]*neo4j.Record{},
		[]*neo4j.Record{},
		nil, nil, nil, nil,
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaTimeout:    30 * time.Second, // Generous timeout
		SchemaSampleSize: 100,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	assertJSONEquals(t, `{
		"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}]
	}`, getResultText(t, result))
}

func TestGetSchemaHandler_FallbackHeterogeneousTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	// Expect the schema timeout fallback analytics event
	analyticsService.EXPECT().IsEnabled().Return(true)
	analyticsService.EXPECT().NewSchemaTimeoutFallbackEvent(gomock.Any(), gomock.Eq(100))
	analyticsService.EXPECT().EmitEvent(gomock.Any())

	// Sampling discovers a property with multiple types (heterogeneous data).
	gomock.InOrder(
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("context deadline exceeded")),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{
				sampledNodeRecord("Thing", "value", []string{"STRING", "INTEGER"}),
			}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*neo4j.Record{}, nil),
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaTimeout:    1 * time.Nanosecond,
		SchemaSampleSize: 100,
	}

	time.Sleep(time.Millisecond)

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	// Heterogeneous types should be joined with " | " and sorted
	assertJSONEquals(t, `{
		"nodes": [{"label": "Thing", "properties": {"value": "INTEGER | STRING"}}]
	}`, getResultText(t, result))
}

func TestGetSchemaHandler_IndexesQueryGracefulDegradation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	expectFourQueries(mockDB,
		[]*neo4j.Record{
			nodeRecord([]string{"Movie"}, "title", []string{"String"}),
		},
		[]*neo4j.Record{},
		[]*neo4j.Record{},
		nil, // indexes result (ignored because of error)
		nil, nil, nil,
		errors.New("SHOW INDEXES not supported"), // indexes error
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success - indexes failure should degrade gracefully")
	}

	// Schema should still be present, just no indexes field
	assertJSONEquals(t, `{
		"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}]
	}`, getResultText(t, result))
}

// --- Schema processing tests ---

func TestGetSchemaProcessing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)

	testCases := []struct {
		name           string
		nodeRecords    []*neo4j.Record
		relRecords     []*neo4j.Record
		patternRecords []*neo4j.Record
		indexRecords   []*neo4j.Record
		expectedJSON   string
	}{
		{
			name: "movies graph - nodes, relationships, patterns",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
				nodeRecord([]string{"Movie"}, "released", []string{"Long"}),
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
				nodeRecord([]string{"Person"}, "born", []string{"Long"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`ACTED_IN`", "roles", []string{"StringArray"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Person", "ACTED_IN", "Movie"),
				patternRecord("Person", "DIRECTED", "Movie"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "Movie", "properties": {"title": "STRING", "released": "INTEGER"}},
					{"label": "Person", "properties": {"name": "STRING", "born": "INTEGER"}}
				],
				"relationships": [
					{"type": "ACTED_IN", "from": "Person", "to": "Movie", "properties": {"roles": "LIST<STRING>"}},
					{"type": "DIRECTED", "from": "Person", "to": "Movie"}
				]
			}`,
		},
		{
			name: "node with no relationships",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Genre"}, "name", []string{"String"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "Genre", "properties": {"name": "STRING"}}]
			}`,
		},
		{
			name: "multi-label node contributes properties to each label",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Person", "Actor"}, "name", []string{"String"}),
				nodeRecord([]string{"Person"}, "born", []string{"Long"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "Actor", "properties": {"name": "STRING"}},
					{"label": "Person", "properties": {"name": "STRING", "born": "INTEGER"}}
				]
			}`,
		},
		{
			name: "relationship type backtick formatting is cleaned",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"A"}, "id", []string{"Long"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`HAS_PARENT`", "since", []string{"Date"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("A", "HAS_PARENT", "A"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "A", "properties": {"id": "INTEGER"}}],
				"relationships": [{"type": "HAS_PARENT", "from": "A", "to": "A", "properties": {"since": "DATE"}}]
			}`,
		},
		{
			name: "heterogeneous property types are joined with pipe",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Thing"}, "value", []string{"String", "Long"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "Thing", "properties": {"value": "STRING | INTEGER"}}]
			}`,
		},
		{
			name: "all property types are normalized correctly",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"AllTypes"}, "s", []string{"String"}),
				nodeRecord([]string{"AllTypes"}, "i", []string{"Long"}),
				nodeRecord([]string{"AllTypes"}, "f", []string{"Double"}),
				nodeRecord([]string{"AllTypes"}, "b", []string{"Boolean"}),
				nodeRecord([]string{"AllTypes"}, "d", []string{"Date"}),
				nodeRecord([]string{"AllTypes"}, "dt", []string{"DateTime"}),
				nodeRecord([]string{"AllTypes"}, "ldt", []string{"LocalDateTime"}),
				nodeRecord([]string{"AllTypes"}, "lt", []string{"LocalTime"}),
				nodeRecord([]string{"AllTypes"}, "t", []string{"Time"}),
				nodeRecord([]string{"AllTypes"}, "p", []string{"Point"}),
				nodeRecord([]string{"AllTypes"}, "dur", []string{"Duration"}),
				nodeRecord([]string{"AllTypes"}, "sa", []string{"StringArray"}),
				nodeRecord([]string{"AllTypes"}, "ia", []string{"LongArray"}),
				nodeRecord([]string{"AllTypes"}, "fa", []string{"DoubleArray"}),
				nodeRecord([]string{"AllTypes"}, "ba", []string{"BooleanArray"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{
					"label": "AllTypes",
					"properties": {
						"s": "STRING",
						"i": "INTEGER",
						"f": "FLOAT",
						"b": "BOOLEAN",
						"d": "DATE",
						"dt": "DATE_TIME",
						"ldt": "LOCAL_DATE_TIME",
						"lt": "LOCAL_TIME",
						"t": "ZONED_TIME",
						"p": "POINT",
						"dur": "DURATION",
						"sa": "LIST<STRING>",
						"ia": "LIST<INTEGER>",
						"fa": "LIST<FLOAT>",
						"ba": "LIST<BOOLEAN>"
					}
				}]
			}`,
		},
		{
			name: "vector embedding property with vector index",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Document"}, "title", []string{"String"}),
				nodeRecord([]string{"Document"}, "embedding", []string{"DoubleArray"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords: []*neo4j.Record{
				indexRecord("doc-embeddings", "VECTOR", "NODE",
					[]string{"Document"}, []string{"embedding"},
					vectorOptions(1536, "cosine")),
			},
			expectedJSON: `{
				"nodes": [{"label": "Document", "properties": {"title": "STRING", "embedding": "LIST<FLOAT>"}}],
				"indexes": [{
					"name": "doc-embeddings",
					"type": "VECTOR",
					"entityType": "NODE",
					"labelsOrTypes": ["Document"],
					"properties": ["embedding"],
					"dimensions": 1536,
					"similarityFunction": "cosine"
				}]
			}`,
		},
		{
			name: "multiple indexes including vector and range",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Document"}, "title", []string{"String"}),
				nodeRecord([]string{"Document"}, "embedding", []string{"DoubleArray"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords: []*neo4j.Record{
				indexRecord("doc-embeddings", "VECTOR", "NODE",
					[]string{"Document"}, []string{"embedding"},
					vectorOptions(1536, "cosine")),
				indexRecord("doc-title-range", "RANGE", "NODE",
					[]string{"Document"}, []string{"title"},
					map[string]any{}),
			},
			expectedJSON: `{
				"nodes": [{"label": "Document", "properties": {"title": "STRING", "embedding": "LIST<FLOAT>"}}],
				"indexes": [
					{"name": "doc-embeddings", "type": "VECTOR", "entityType": "NODE", "labelsOrTypes": ["Document"], "properties": ["embedding"], "dimensions": 1536, "similarityFunction": "cosine"},
					{"name": "doc-title-range", "type": "RANGE", "entityType": "NODE", "labelsOrTypes": ["Document"], "properties": ["title"]}
				]
			}`,
		},
		{
			name: "vector index on relationship",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Chunk"}, "text", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`SIMILAR_TO`", "score", []string{"Double"}),
				relRecord(":`SIMILAR_TO`", "embedding", []string{"FloatArray"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Chunk", "SIMILAR_TO", "Chunk"),
			},
			indexRecords: []*neo4j.Record{
				indexRecord("similarity-vec", "VECTOR", "RELATIONSHIP",
					[]string{"SIMILAR_TO"}, []string{"embedding"},
					vectorOptions(768, "euclidean")),
			},
			expectedJSON: `{
				"nodes": [{"label": "Chunk", "properties": {"text": "STRING"}}],
				"relationships": [{"type": "SIMILAR_TO", "from": "Chunk", "to": "Chunk", "properties": {"score": "FLOAT", "embedding": "LIST<FLOAT>"}}],
				"indexes": [{
					"name": "similarity-vec",
					"type": "VECTOR",
					"entityType": "RELATIONSHIP",
					"labelsOrTypes": ["SIMILAR_TO"],
					"properties": ["embedding"],
					"dimensions": 768,
					"similarityFunction": "euclidean"
				}]
			}`,
		},
		{
			name: "vector index with missing options gracefully omits dimensions",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Doc"}, "vec", []string{"DoubleArray"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords: []*neo4j.Record{
				indexRecord("vec-idx", "VECTOR", "NODE",
					[]string{"Doc"}, []string{"vec"},
					map[string]any{}),
			},
			expectedJSON: `{
				"nodes": [{"label": "Doc", "properties": {"vec": "LIST<FLOAT>"}}],
				"indexes": [{"name": "vec-idx", "type": "VECTOR", "entityType": "NODE", "labelsOrTypes": ["Doc"], "properties": ["vec"]}]
			}`,
		},
		{
			name: "relationship properties without patterns (nil pattern records)",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"A"}, "id", []string{"Long"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`KNOWS`", "since", []string{"Date"}),
			},
			patternRecords: nil,
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "A", "properties": {"id": "INTEGER"}}],
				"relationships": [{"type": "KNOWS", "properties": {"since": "DATE"}}]
			}`,
		},
		{
			name: "same relationship type between multiple label pairs",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
				nodeRecord([]string{"Company"}, "name", []string{"String"}),
				nodeRecord([]string{"School"}, "name", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`AFFILIATED_WITH`", "since", []string{"Date"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Person", "AFFILIATED_WITH", "Company"),
				patternRecord("Person", "AFFILIATED_WITH", "School"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "Company", "properties": {"name": "STRING"}},
					{"label": "Person", "properties": {"name": "STRING"}},
					{"label": "School", "properties": {"name": "STRING"}}
				],
				"relationships": [
					{"type": "AFFILIATED_WITH", "from": "Person", "to": "Company", "properties": {"since": "DATE"}},
					{"type": "AFFILIATED_WITH", "from": "Person", "to": "School", "properties": {"since": "DATE"}}
				]
			}`,
		},
		{
			name: "duplicate pattern records are deduplicated",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"A"}, "x", []string{"String"}),
			},
			relRecords: []*neo4j.Record{},
			patternRecords: []*neo4j.Record{
				patternRecord("A", "LINKS", "A"),
				patternRecord("A", "LINKS", "A"),
				patternRecord("A", "LINKS", "A"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "A", "properties": {"x": "STRING"}}],
				"relationships": [{"type": "LINKS", "from": "A", "to": "A"}]
			}`,
		},
		{
			name: "unknown property type passes through as uppercase",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Node"}, "custom", []string{"SomeFutureType"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "Node", "properties": {"custom": "SOMEFUTURETYPE"}}]
			}`,
		},
		{
			name: "relationship types with null propertyName (no properties)",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`ACTED_IN`", "roles", []string{"StringArray"}),
				relRecordNoProps(":`DIRECTED`"),
				relRecordNoProps(":`PRODUCED`"),
				relRecord(":`REVIEWED`", "rating", []string{"Long"}),
				relRecord(":`REVIEWED`", "summary", []string{"String"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Person", "ACTED_IN", "Movie"),
				patternRecord("Person", "DIRECTED", "Movie"),
				patternRecord("Person", "PRODUCED", "Movie"),
				patternRecord("Person", "REVIEWED", "Movie"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "Movie", "properties": {"title": "STRING"}},
					{"label": "Person", "properties": {"name": "STRING"}}
				],
				"relationships": [
					{"type": "ACTED_IN", "from": "Person", "to": "Movie", "properties": {"roles": "LIST<STRING>"}},
					{"type": "DIRECTED", "from": "Person", "to": "Movie"},
					{"type": "PRODUCED", "from": "Person", "to": "Movie"},
					{"type": "REVIEWED", "from": "Person", "to": "Movie", "properties": {"rating": "INTEGER", "summary": "STRING"}}
				]
			}`,
		},
		{
			name: "node label with null propertyName (no properties)",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
				nodeRecordNoProps([]string{"EmptyLabel"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "EmptyLabel"},
					{"label": "Person", "properties": {"name": "STRING"}}
				]
			}`,
		},
		{
			name: "relationship types with null propertyName (no properties)",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`ACTED_IN`", "roles", []string{"StringArray"}),
				relRecordNoProps(":`DIRECTED`"),
				relRecordNoProps(":`PRODUCED`"),
				relRecord(":`REVIEWED`", "rating", []string{"Long"}),
				relRecord(":`REVIEWED`", "summary", []string{"String"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Person", "ACTED_IN", "Movie"),
				patternRecord("Person", "DIRECTED", "Movie"),
				patternRecord("Person", "PRODUCED", "Movie"),
				patternRecord("Person", "REVIEWED", "Movie"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "Movie", "properties": {"title": "STRING"}},
					{"label": "Person", "properties": {"name": "STRING"}}
				],
				"relationships": [
					{"type": "ACTED_IN", "from": "Person", "to": "Movie", "properties": {"roles": "LIST<STRING>"}},
					{"type": "DIRECTED", "from": "Person", "to": "Movie"},
					{"type": "PRODUCED", "from": "Person", "to": "Movie"},
					{"type": "REVIEWED", "from": "Person", "to": "Movie", "properties": {"rating": "INTEGER", "summary": "STRING"}}
				]
			}`,
		},
		{
			name: "node label with null propertyName (no properties)",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Person"}, "name", []string{"String"}),
				nodeRecordNoProps([]string{"EmptyLabel"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [
					{"label": "EmptyLabel"},
					{"label": "Person", "properties": {"name": "STRING"}}
				]
			}`,
		},
		{
			name: "Bloom nodes are excluded from schema output",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
				nodeRecord([]string{"_Bloom_Perspective_"}, "id", []string{"String"}),
				nodeRecord([]string{"_Bloom_Perspective_"}, "name", []string{"String"}),
				nodeRecord([]string{"_Bloom_Scene_"}, "id", []string{"String"}),
				nodeRecord([]string{"_Bloom_Scene_"}, "visualisation", []string{"String"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}]
			}`,
		},
		{
			name: "Bloom relationships are excluded from schema output",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
				nodeRecord([]string{"_Bloom_Perspective_"}, "id", []string{"String"}),
				nodeRecord([]string{"_Bloom_Scene_"}, "id", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`ACTED_IN`", "roles", []string{"StringArray"}),
				relRecord(":`_Bloom_HAS_SCENE_`", "order", []string{"Long"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Person", "ACTED_IN", "Movie"),
				patternRecord("_Bloom_Perspective_", "_Bloom_HAS_SCENE_", "_Bloom_Scene_"),
			},
			indexRecords: []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}],
				"relationships": [{"type": "ACTED_IN", "from": "Person", "to": "Movie", "properties": {"roles": "LIST<STRING>"}}]
			}`,
		},
		{
			name: "Bloom indexes are excluded from schema output",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords: []*neo4j.Record{
				indexRecord("movie-title-range", "RANGE", "NODE",
					[]string{"Movie"}, []string{"title"}, map[string]any{}),
				indexRecord("bloom-perspective-id", "RANGE", "NODE",
					[]string{"_Bloom_Perspective_"}, []string{"id"}, map[string]any{}),
			},
			expectedJSON: `{
				"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}],
				"indexes": [{"name": "movie-title-range", "type": "RANGE", "entityType": "NODE", "labelsOrTypes": ["Movie"], "properties": ["title"]}]
			}`,
		},
		{
			name: "mixed index with both internal and user labels is kept",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Movie"}, "title", []string{"String"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords: []*neo4j.Record{
				indexRecord("mixed-fulltext", "FULLTEXT", "NODE",
					[]string{"Movie", "_Bloom_Perspective_"}, []string{"name"}, map[string]any{}),
			},
			expectedJSON: `{
				"nodes": [{"label": "Movie", "properties": {"title": "STRING"}}],
				"indexes": [{"name": "mixed-fulltext", "type": "FULLTEXT", "entityType": "NODE", "labelsOrTypes": ["Movie", "_Bloom_Perspective_"], "properties": ["name"]}]
			}`,
		},
		{
			name: "full-text indexes on nodes and relationships",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"Article"}, "title", []string{"String"}),
				nodeRecord([]string{"Article"}, "body", []string{"String"}),
				nodeRecord([]string{"Comment"}, "text", []string{"String"}),
			},
			relRecords: []*neo4j.Record{
				relRecord(":`HAS_COMMENT`", "content", []string{"String"}),
			},
			patternRecords: []*neo4j.Record{
				patternRecord("Article", "HAS_COMMENT", "Comment"),
			},
			indexRecords: []*neo4j.Record{
				indexRecord("article-fulltext", "FULLTEXT", "NODE",
					[]string{"Article"}, []string{"title", "body"}, map[string]any{}),
				indexRecord("comment-fulltext", "FULLTEXT", "NODE",
					[]string{"Comment"}, []string{"text"}, map[string]any{}),
				indexRecord("rel-fulltext", "FULLTEXT", "RELATIONSHIP",
					[]string{"HAS_COMMENT"}, []string{"content"}, map[string]any{}),
			},
			expectedJSON: `{
				"nodes": [
					{"label": "Article", "properties": {"title": "STRING", "body": "STRING"}},
					{"label": "Comment", "properties": {"text": "STRING"}}
				],
				"relationships": [
					{"type": "HAS_COMMENT", "from": "Article", "to": "Comment", "properties": {"content": "STRING"}}
				],
				"indexes": [
					{"name": "article-fulltext", "type": "FULLTEXT", "entityType": "NODE", "labelsOrTypes": ["Article"], "properties": ["title", "body"]},
					{"name": "comment-fulltext", "type": "FULLTEXT", "entityType": "NODE", "labelsOrTypes": ["Comment"], "properties": ["text"]},
					{"name": "rel-fulltext", "type": "FULLTEXT", "entityType": "RELATIONSHIP", "labelsOrTypes": ["HAS_COMMENT"], "properties": ["content"]}
				]
			}`,
		},
		{
			name: "FloatArray normalized same as DoubleArray",
			nodeRecords: []*neo4j.Record{
				nodeRecord([]string{"N"}, "a", []string{"FloatArray"}),
				nodeRecord([]string{"N"}, "b", []string{"DoubleArray"}),
			},
			relRecords:     []*neo4j.Record{},
			patternRecords: []*neo4j.Record{},
			indexRecords:   []*neo4j.Record{},
			expectedJSON: `{
				"nodes": [{"label": "N", "properties": {"a": "LIST<FLOAT>", "b": "LIST<FLOAT>"}}]
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := db.NewMockService(ctrl)

			expectFourQueries(mockDB,
				tc.nodeRecords, tc.relRecords, tc.patternRecords, tc.indexRecords,
				nil, nil, nil, nil,
			)

			deps := &tools.ToolDependencies{
				DBService:        mockDB,
				AnalyticsService: analyticsService,
			}

			handler := cypher.GetSchemaHandler(deps, 100)
			result, err := handler(context.Background(), mcp.CallToolRequest{})

			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("expected success result, got error: %s", getResultText(t, result))
			}

			assertJSONEquals(t, tc.expectedJSON, getResultText(t, result))
		})
	}
}

// --- Invalid data tests ---

// These test that malformed records from the db.schema procedures produce
// a tool error rather than a panic or garbled output.
// All 4 queries execute successfully (no query-level errors); the failure
// happens during processing in buildSchemaResponse.

func TestGetSchemaProcessing_InvalidNodeData(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)

	testCases := []struct {
		name        string
		nodeRecords []*neo4j.Record
	}{
		{
			name: "missing nodeLabels column",
			nodeRecords: []*neo4j.Record{
				{
					Keys:   []string{"propertyName", "propertyTypes"},
					Values: []any{"title", []any{"String"}},
				},
			},
		},
		{
			name: "invalid nodeLabels type (not a list)",
			nodeRecords: []*neo4j.Record{
				{
					Keys:   []string{"nodeLabels", "propertyName", "propertyTypes"},
					Values: []any{"NotAList", "title", []any{"String"}},
				},
			},
		},
		{
			name: "invalid propertyName type (not a string)",
			nodeRecords: []*neo4j.Record{
				{
					Keys:   []string{"nodeLabels", "propertyName", "propertyTypes"},
					Values: []any{[]any{"Movie"}, 12345, []any{"String"}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := db.NewMockService(ctrl)

			// All 4 queries execute; the error surfaces during processing
			expectFourQueries(mockDB,
				tc.nodeRecords,
				[]*neo4j.Record{},
				[]*neo4j.Record{},
				[]*neo4j.Record{},
				nil, nil, nil, nil,
			)

			deps := &tools.ToolDependencies{
				DBService:        mockDB,
				AnalyticsService: analyticsService,
			}

			handler := cypher.GetSchemaHandler(deps, 100)
			result, err := handler(context.Background(), mcp.CallToolRequest{})

			if err != nil {
				t.Fatalf("expected no error from handler, got: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatal("expected error result for invalid node data")
			}
		})
	}
}

func TestGetSchemaProcessing_InvalidRelData(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)

	testCases := []struct {
		name       string
		relRecords []*neo4j.Record
	}{
		{
			name: "missing relType column",
			relRecords: []*neo4j.Record{
				{
					Keys:   []string{"propertyName", "propertyTypes"},
					Values: []any{"roles", []any{"StringArray"}},
				},
			},
		},
		{
			name: "invalid relType type (not a string)",
			relRecords: []*neo4j.Record{
				{
					Keys:   []string{"relType", "propertyName", "propertyTypes"},
					Values: []any{12345, "roles", []any{"StringArray"}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDB := db.NewMockService(ctrl)

			// nodeRecords must have data so we don't hit the empty-database short circuit
			expectFourQueries(mockDB,
				[]*neo4j.Record{
					nodeRecord([]string{"X"}, "id", []string{"Long"}),
				},
				tc.relRecords,
				[]*neo4j.Record{},
				[]*neo4j.Record{},
				nil, nil, nil, nil,
			)

			deps := &tools.ToolDependencies{
				DBService:        mockDB,
				AnalyticsService: analyticsService,
			}

			handler := cypher.GetSchemaHandler(deps, 100)
			result, err := handler(context.Background(), mcp.CallToolRequest{})

			if err != nil {
				t.Fatalf("expected no error from handler, got: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatal("expected error result for invalid rel data")
			}
		})
	}
}

// --- Full integration-style test with realistic RAG data ---

func TestGetSchemaProcessing_RealisticGraphWithVectors(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	mockDB := db.NewMockService(ctrl)

	expectFourQueries(mockDB,
		// Node properties: Document nodes with text content and vector embeddings
		[]*neo4j.Record{
			nodeRecord([]string{"Document"}, "title", []string{"String"}),
			nodeRecord([]string{"Document"}, "content", []string{"String"}),
			nodeRecord([]string{"Document"}, "embedding", []string{"DoubleArray"}),
			nodeRecord([]string{"Chunk"}, "text", []string{"String"}),
			nodeRecord([]string{"Chunk"}, "embedding", []string{"DoubleArray"}),
			nodeRecord([]string{"Topic"}, "name", []string{"String"}),
		},
		// Relationship properties
		[]*neo4j.Record{
			relRecord(":`HAS_CHUNK`", "position", []string{"Long"}),
			relRecord(":`ABOUT`", "confidence", []string{"Double"}),
			relRecord(":`SIMILAR_TO`", "score", []string{"Double"}),
		},
		// Patterns
		[]*neo4j.Record{
			patternRecord("Document", "HAS_CHUNK", "Chunk"),
			patternRecord("Document", "ABOUT", "Topic"),
			patternRecord("Chunk", "ABOUT", "Topic"),
			patternRecord("Chunk", "SIMILAR_TO", "Chunk"),
		},
		// Indexes
		[]*neo4j.Record{
			indexRecord("chunk-embedding-idx", "VECTOR", "NODE",
				[]string{"Chunk"}, []string{"embedding"},
				vectorOptions(1536, "cosine")),
			indexRecord("doc-embedding-idx", "VECTOR", "NODE",
				[]string{"Document"}, []string{"embedding"},
				vectorOptions(1536, "cosine")),
			indexRecord("doc-title-range", "RANGE", "NODE",
				[]string{"Document"}, []string{"title"},
				map[string]any{}),
			indexRecord("topic-name-text", "TEXT", "NODE",
				[]string{"Topic"}, []string{"name"},
				map[string]any{}),
		},
		nil, nil, nil, nil,
	)

	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
	}

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	assertJSONEquals(t, `{
		"nodes": [
			{"label": "Chunk", "properties": {"text": "STRING", "embedding": "LIST<FLOAT>"}},
			{"label": "Document", "properties": {"title": "STRING", "content": "STRING", "embedding": "LIST<FLOAT>"}},
			{"label": "Topic", "properties": {"name": "STRING"}}
		],
		"relationships": [
			{"type": "ABOUT", "from": "Chunk", "to": "Topic", "properties": {"confidence": "FLOAT"}},
			{"type": "ABOUT", "from": "Document", "to": "Topic", "properties": {"confidence": "FLOAT"}},
			{"type": "HAS_CHUNK", "from": "Document", "to": "Chunk", "properties": {"position": "INTEGER"}},
			{"type": "SIMILAR_TO", "from": "Chunk", "to": "Chunk", "properties": {"score": "FLOAT"}}
		],
		"indexes": [
			{"name": "chunk-embedding-idx", "type": "VECTOR", "entityType": "NODE", "labelsOrTypes": ["Chunk"], "properties": ["embedding"], "dimensions": 1536, "similarityFunction": "cosine"},
			{"name": "doc-embedding-idx", "type": "VECTOR", "entityType": "NODE", "labelsOrTypes": ["Document"], "properties": ["embedding"], "dimensions": 1536, "similarityFunction": "cosine"},
			{"name": "doc-title-range", "type": "RANGE", "entityType": "NODE", "labelsOrTypes": ["Document"], "properties": ["title"]},
			{"name": "topic-name-text", "type": "TEXT", "entityType": "NODE", "labelsOrTypes": ["Topic"], "properties": ["name"]}
		]
	}`, getResultText(t, result))
}
