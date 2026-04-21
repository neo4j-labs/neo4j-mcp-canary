// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// --- Test helpers ---

// apocProperty builds one entry of the APOC properties map: a name mapped to a
// map containing the type (plus ignored fields). The handler's simplifyProperties
// extracts just the type string.
func apocProperty(typeName string) map[string]interface{} {
	return map[string]interface{}{
		"type":      typeName,
		"unique":    false,
		"indexed":   false,
		"existence": false,
	}
}

// apocRelationship builds one entry of a node's relationships map: direction,
// target labels, and (optionally) properties. Mirrors the shape apoc.meta.schema
// returns in the `relationships` field of a node value.
func apocRelationship(direction string, labels []string, props map[string]interface{}) map[string]interface{} {
	labelsAny := make([]interface{}, len(labels))
	for i, l := range labels {
		labelsAny[i] = l
	}
	rel := map[string]interface{}{
		"direction": direction,
		"labels":    labelsAny,
		"count":     int64(0), // count is returned by APOC but dropped by the handler
	}
	if props != nil {
		rel["properties"] = props
	}
	return rel
}

// apocRecord builds a single record as returned by the schema query.
// It has two columns:
//
//	key   — string ("Movie", "ACTED_IN", etc.)
//	value — map with type, properties, and (for nodes) relationships
//
// Passing a nil relationships argument omits the field entirely, exercising
// the non-node (relationship-type) branch.
func apocRecord(key, typeName string, props map[string]interface{}, rels map[string]interface{}) *neo4j.Record {
	value := map[string]interface{}{
		"type":       typeName,
		"properties": props,
	}
	if rels != nil {
		value["relationships"] = rels
	}
	return &neo4j.Record{
		Keys:   []string{"key", "value"},
		Values: []any{key, value},
	}
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

// newDepsWithMocks wires up a ToolDependencies with fresh mocks. Analytics is
// stubbed to "disabled" so it never participates — the APOC handler does not
// emit analytics itself.
func newDepsWithMocks(t *testing.T) (*tools.ToolDependencies, *db.MockService, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)
	mockDB := db.NewMockService(ctrl)
	deps := &tools.ToolDependencies{
		DBService:        mockDB,
		AnalyticsService: analyticsService,
		SchemaSampleSize: 100,
	}
	return deps, mockDB, ctrl
}

// --- Handler-level tests ---

func TestGetSchemaHandler_NilDatabaseService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)

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

func TestGetSchemaHandler_SchemaQueryFails(t *testing.T) {
	deps, mockDB, ctrl := newDepsWithMocks(t)
	defer ctrl.Finish()

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection refused"))

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error from handler, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result when schema query fails")
	}
}

func TestGetSchemaHandler_EmptyDatabase(t *testing.T) {
	deps, mockDB, ctrl := newDepsWithMocks(t)
	defer ctrl.Finish()

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*neo4j.Record{}, nil)

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success on empty database, got error: %s", getResultText(t, result))
	}

	text := getResultText(t, result)
	expected := "The get-schema tool executed successfully; however, since the Neo4j instance contains no data, no schema information was returned."
	if text != expected {
		t.Errorf("expected empty database message, got: %s", text)
	}
}

// TestGetSchemaHandler_SampleSizeForwardedToAPOC locks in that the schemaSampleSize
// passed into GetSchemaHandler is forwarded to apoc.meta.schema's `sample` parameter.
// This is the hook that NEO4J_SCHEMA_SAMPLE_SIZE config flows through to.
func TestGetSchemaHandler_SampleSizeForwardedToAPOC(t *testing.T) {
	deps, mockDB, ctrl := newDepsWithMocks(t)
	defer ctrl.Finish()

	var capturedParams map[string]any
	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params map[string]any) ([]*neo4j.Record, error) {
			capturedParams = params
			return []*neo4j.Record{}, nil
		})

	handler := cypher.GetSchemaHandler(deps, 500)
	if _, err := handler(context.Background(), mcp.CallToolRequest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := capturedParams["sampleSize"]
	if !ok {
		t.Fatalf("expected 'sampleSize' param to be forwarded to APOC; got params: %v", capturedParams)
	}
	// The handler passes schemaSampleSize as int32 — confirm value round-trips.
	if got != int32(500) {
		t.Errorf("sampleSize = %v (%T), want int32(500)", got, got)
	}
}

// --- Schema processing tests ---

func TestGetSchemaProcessing(t *testing.T) {
	testCases := []struct {
		name         string
		records      []*neo4j.Record
		expectedJSON string
	}{
		{
			name: "node with properties and no relationships",
			records: []*neo4j.Record{
				apocRecord("Genre", "node", map[string]interface{}{
					"name": apocProperty("STRING"),
				}, nil),
			},
			expectedJSON: `[
				{"key": "Genre", "value": {"type": "node", "properties": {"name": "STRING"}}}
			]`,
		},
		{
			name: "node with outgoing relationship carrying properties",
			records: []*neo4j.Record{
				apocRecord("Person", "node",
					map[string]interface{}{
						"name": apocProperty("STRING"),
						"born": apocProperty("INTEGER"),
					},
					map[string]interface{}{
						"ACTED_IN": apocRelationship("out", []string{"Movie"}, map[string]interface{}{
							"roles": apocProperty("LIST"),
						}),
					},
				),
				apocRecord("Movie", "node", map[string]interface{}{
					"title": apocProperty("STRING"),
				}, nil),
			},
			expectedJSON: `[
				{
					"key": "Person",
					"value": {
						"type": "node",
						"properties": {"name": "STRING", "born": "INTEGER"},
						"relationships": {
							"ACTED_IN": {
								"direction": "out",
								"labels": ["Movie"],
								"properties": {"roles": "LIST"}
							}
						}
					}
				},
				{
					"key": "Movie",
					"value": {"type": "node", "properties": {"title": "STRING"}}
				}
			]`,
		},
		{
			name: "relationship entry has type='relationship' and no relationships map",
			records: []*neo4j.Record{
				apocRecord("ACTED_IN", "relationship", map[string]interface{}{
					"roles": apocProperty("LIST"),
				}, nil),
			},
			expectedJSON: `[
				{
					"key": "ACTED_IN",
					"value": {"type": "relationship", "properties": {"roles": "LIST"}}
				}
			]`,
		},
		{
			name: "relationship with no properties produces empty props (omitted in JSON)",
			records: []*neo4j.Record{
				apocRecord("DIRECTED", "relationship", map[string]interface{}{}, nil),
			},
			expectedJSON: `[
				{"key": "DIRECTED", "value": {"type": "relationship"}}
			]`,
		},
		{
			name: "node with nil relationships field behaves as no relationships",
			records: []*neo4j.Record{
				apocRecord("Standalone", "node",
					map[string]interface{}{"id": apocProperty("STRING")},
					nil),
			},
			expectedJSON: `[
				{"key": "Standalone", "value": {"type": "node", "properties": {"id": "STRING"}}}
			]`,
		},
		{
			name: "node with multiple outgoing relationship types",
			records: []*neo4j.Record{
				apocRecord("Document", "node",
					map[string]interface{}{"title": apocProperty("STRING")},
					map[string]interface{}{
						"HAS_CHUNK": apocRelationship("out", []string{"Chunk"}, nil),
						"ABOUT":     apocRelationship("out", []string{"Topic"}, map[string]interface{}{"confidence": apocProperty("FLOAT")}),
					},
				),
			},
			expectedJSON: `[
				{
					"key": "Document",
					"value": {
						"type": "node",
						"properties": {"title": "STRING"},
						"relationships": {
							"HAS_CHUNK": {"direction": "out", "labels": ["Chunk"]},
							"ABOUT":     {"direction": "out", "labels": ["Topic"], "properties": {"confidence": "FLOAT"}}
						}
					}
				}
			]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deps, mockDB, ctrl := newDepsWithMocks(t)
			defer ctrl.Finish()

			mockDB.EXPECT().
				ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tc.records, nil)

			handler := cypher.GetSchemaHandler(deps, 100)
			result, err := handler(context.Background(), mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("expected success, got error: %s", getResultText(t, result))
			}

			assertJSONEquals(t, tc.expectedJSON, getResultText(t, result))
		})
	}
}

// --- Invalid data tests ---
//
// These test that malformed records from apoc.meta.schema produce a tool error
// rather than a panic or garbled output.

func TestGetSchemaProcessing_InvalidRecordShape(t *testing.T) {
	testCases := []struct {
		name   string
		record *neo4j.Record
	}{
		{
			name: "missing 'key' column",
			record: &neo4j.Record{
				Keys:   []string{"value"},
				Values: []any{map[string]interface{}{"type": "node"}},
			},
		},
		{
			name: "non-string key",
			record: &neo4j.Record{
				Keys:   []string{"key", "value"},
				Values: []any{12345, map[string]interface{}{"type": "node"}},
			},
		},
		{
			name: "missing 'value' column",
			record: &neo4j.Record{
				Keys:   []string{"key"},
				Values: []any{"Movie"},
			},
		},
		{
			name: "value is not a map",
			record: &neo4j.Record{
				Keys:   []string{"key", "value"},
				Values: []any{"Movie", "not-a-map"},
			},
		},
		{
			name: "missing 'type' field in value",
			record: &neo4j.Record{
				Keys: []string{"key", "value"},
				Values: []any{"Movie", map[string]interface{}{
					"properties": map[string]interface{}{},
				}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deps, mockDB, ctrl := newDepsWithMocks(t)
			defer ctrl.Finish()

			mockDB.EXPECT().
				ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]*neo4j.Record{tc.record}, nil)

			handler := cypher.GetSchemaHandler(deps, 100)
			result, err := handler(context.Background(), mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("expected no error from handler, got: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatal("expected error result for malformed record")
			}
		})
	}
}

// --- Integration-style test with a representative graph ---

// TestGetSchemaProcessing_RealisticGraph exercises a graph-shaped fixture with
// multiple node labels, multiple rel types, and a realistic mix of property
// types. Serves as a smoke test that the pieces fit together.
func TestGetSchemaProcessing_RealisticGraph(t *testing.T) {
	deps, mockDB, ctrl := newDepsWithMocks(t)
	defer ctrl.Finish()

	records := []*neo4j.Record{
		apocRecord("Movie", "node",
			map[string]interface{}{
				"title":    apocProperty("STRING"),
				"released": apocProperty("INTEGER"),
			},
			nil),
		apocRecord("Person", "node",
			map[string]interface{}{
				"name": apocProperty("STRING"),
				"born": apocProperty("INTEGER"),
			},
			map[string]interface{}{
				"ACTED_IN": apocRelationship("out", []string{"Movie"}, map[string]interface{}{
					"roles": apocProperty("LIST"),
				}),
				"DIRECTED": apocRelationship("out", []string{"Movie"}, nil),
			},
		),
		apocRecord("ACTED_IN", "relationship", map[string]interface{}{
			"roles": apocProperty("LIST"),
		}, nil),
		apocRecord("DIRECTED", "relationship", map[string]interface{}{}, nil),
	}

	mockDB.EXPECT().
		ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(records, nil)

	handler := cypher.GetSchemaHandler(deps, 100)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got error: %s", getResultText(t, result))
	}

	assertJSONEquals(t, `[
		{
			"key": "Movie",
			"value": {
				"type": "node",
				"properties": {"title": "STRING", "released": "INTEGER"}
			}
		},
		{
			"key": "Person",
			"value": {
				"type": "node",
				"properties": {"name": "STRING", "born": "INTEGER"},
				"relationships": {
					"ACTED_IN": {"direction": "out", "labels": ["Movie"], "properties": {"roles": "LIST"}},
					"DIRECTED": {"direction": "out", "labels": ["Movie"]}
				}
			}
		},
		{
			"key": "ACTED_IN",
			"value": {"type": "relationship", "properties": {"roles": "LIST"}}
		},
		{
			"key": "DIRECTED",
			"value": {"type": "relationship"}
		}
	]`, getResultText(t, result))
}
