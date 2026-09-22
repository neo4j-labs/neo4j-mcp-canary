// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// White-box tests for eventing's unexported pure helpers, kept separate from
// the black-box Emitter behaviour tests in eventing_test.go and
// tool_calls_test.go.
package eventing

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
)

func TestRecordsToConnectionEventInfo(t *testing.T) {
	tests := []struct {
		name    string
		records []*neo4j.Record
		want    analytics.ConnectionEventInfo
	}{
		{
			name:    "no records defaults to unknown",
			records: nil,
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "Neo4j Kernel and Cypher records populate all fields",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", "enterprise", []any{"5.18.0"}}},
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Cypher", "enterprise", []any{"5", "25"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "5.18.0",
				Edition:       "enterprise",
				CypherVersion: []string{"5", "25"},
			},
		},
		{
			name: "missing name column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"edition", "versions"}, Values: []any{"enterprise", []any{"5.18.0"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "non-string name column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{42, "enterprise", []any{"5.18.0"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "missing edition column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"name", "versions"}, Values: []any{"Neo4j Kernel", []any{"5.18.0"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "non-string edition column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", 1, []any{"5.18.0"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "missing versions column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition"}, Values: []any{"Neo4j Kernel", "enterprise"}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "non-slice versions column is skipped, defaults kept",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", "enterprise", "5.18.0"}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "empty Neo4j Kernel versions leaves version unset but keeps edition",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", "enterprise", []any{}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "enterprise",
				CypherVersion: []string{"unknown"},
			},
		},
		{
			name: "unrecognized component name is ignored",
			records: []*neo4j.Record{
				{Keys: []string{"name", "edition", "versions"}, Values: []any{"Some Other Component", "enterprise", []any{"1.0"}}},
			},
			want: analytics.ConnectionEventInfo{
				Neo4jVersion:  "unknown",
				Edition:       "unknown",
				CypherVersion: []string{"unknown"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recordsToConnectionEventInfo(tt.records)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractCypherVectorInfo(t *testing.T) {
	req := func(query string) *mcpsdk.CallToolRequest {
		return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": query}}}
	}

	t.Run("missing query argument returns nil", func(t *testing.T) {
		request := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: map[string]any{}}}
		assert.Nil(t, extractCypherVectorInfo(request))
	})

	t.Run("non-string query argument returns nil", func(t *testing.T) {
		request := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": 42}}}
		assert.Nil(t, extractCypherVectorInfo(request))
	})

	t.Run("plain query with no vector/fulltext calls returns nil", func(t *testing.T) {
		assert.Nil(t, extractCypherVectorInfo(req("MATCH (n) RETURN n")))
	})

	t.Run("detects vector search case-insensitively", func(t *testing.T) {
		got := extractCypherVectorInfo(req("CALL DB.INDEX.VECTOR.QUERYNODES('idx', 5, $embedding)"))
		if assert.NotNil(t, got) {
			assert.True(t, *got.VectorSearch)
			assert.False(t, *got.VectorPropertySet)
			assert.False(t, *got.FullTextSearch)
		}
	})

	t.Run("detects vector property set on nodes", func(t *testing.T) {
		got := extractCypherVectorInfo(req("CALL db.create.setNodeVectorProperty(n, 'embedding', $v)"))
		if assert.NotNil(t, got) {
			assert.True(t, *got.VectorPropertySet)
		}
	})

	t.Run("detects vector property set on relationships", func(t *testing.T) {
		got := extractCypherVectorInfo(req("CALL db.create.setRelationshipVectorProperty(r, 'embedding', $v)"))
		if assert.NotNil(t, got) {
			assert.True(t, *got.VectorPropertySet)
		}
	})

	t.Run("detects full-text search on nodes and relationships", func(t *testing.T) {
		got := extractCypherVectorInfo(req("CALL db.index.fulltext.queryNodes('idx', 'term')"))
		if assert.NotNil(t, got) {
			assert.True(t, *got.FullTextSearch)
		}

		got = extractCypherVectorInfo(req("CALL db.index.fulltext.queryRelationships('idx', 'term')"))
		if assert.NotNil(t, got) {
			assert.True(t, *got.FullTextSearch)
		}
	})
}
