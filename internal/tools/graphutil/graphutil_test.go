// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	dbmocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestQuoteIdentifier(t *testing.T) {
	assert.Equal(t, "`Person`", graphutil.QuoteIdentifier("Person"))
	assert.Equal(t, "`a``b`", graphutil.QuoteIdentifier("a`b"))
	// A label crafted to break out of the identifier stays inside it.
	assert.Equal(t, "`X``) DETACH DELETE (n //`", graphutil.QuoteIdentifier("X`) DETACH DELETE (n //"))
}

func TestLabelAndTypeExpressions(t *testing.T) {
	labels, err := graphutil.LabelExpr([]string{"A", "B"})
	require.NoError(t, err)
	assert.Equal(t, ":`A`&`B`", labels)

	list, err := graphutil.LabelList([]string{"A", "B"})
	require.NoError(t, err)
	assert.Equal(t, ":`A`:`B`", list)

	types, err := graphutil.TypeExpr([]string{"X", "Y"})
	require.NoError(t, err)
	assert.Equal(t, ":`X`|`Y`", types)

	empty, err := graphutil.LabelExpr(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = graphutil.LabelExpr([]string{"A", " "})
	assert.ErrorContains(t, err, "blank")
}

func TestFilterConditions(t *testing.T) {
	t.Run("renders whitelisted operators with bound values", func(t *testing.T) {
		b := graphutil.NewBuilder()
		var filters []graphutil.Filter
		require.NoError(t, json.Unmarshal([]byte(`[
			{"property": "age", "operator": ">=", "value": 30},
			{"property": "name", "operator": "starts with", "value": "Al"},
			{"property": "email", "operator": "IS NULL"},
			{"property": "tag", "operator": "IN", "value": ["a", "b"]}]`), &filters))
		conds, err := graphutil.FilterConditions("n", filters, b)
		require.NoError(t, err)
		assert.Equal(t, []string{"n.`age` >= $p0", "n.`name` STARTS WITH $p1", "n.`email` IS NULL", "n.`tag` IN $p2"}, conds)
		assert.Equal(t, int64(30), b.Params()["p0"], "whole numbers must stay integers")
		assert.Equal(t, "Al", b.Params()["p1"])
	})

	for name, tc := range map[string]struct {
		filter graphutil.Filter
		want   string
	}{
		"unknown operator":  {graphutil.Filter{Property: "a", Operator: "; DROP", Value: 1}, "is not supported; use one of"},
		"empty property":    {graphutil.Filter{Operator: "=", Value: 1}, "property must not be empty"},
		"missing value":     {graphutil.Filter{Property: "a", Operator: "="}, "IS NULL"},
		"IN needs an array": {graphutil.Filter{Property: "a", Operator: "IN", Value: "x"}, "must be an array"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := graphutil.FilterConditions("n", []graphutil.Filter{tc.filter}, graphutil.NewBuilder())
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestNodeRef(t *testing.T) {
	t.Run("labels alone are not a predicate", func(t *testing.T) {
		assert.False(t, graphutil.NodeRef{Labels: []string{"A"}}.HasPredicate())
		assert.True(t, graphutil.NodeRef{ID: "4:x:1"}.HasPredicate())
		assert.True(t, graphutil.NodeRef{Match: map[string]any{"k": "v"}}.HasPredicate())
	})

	t.Run("pattern binds every value as a parameter", func(t *testing.T) {
		b := graphutil.NewBuilder()
		pattern, conds, err := graphutil.NodeRef{ID: "4:x:1", Labels: []string{"A", "B"}, Match: map[string]any{"name": "O'Brien`"}}.Pattern("n", b)
		require.NoError(t, err)
		assert.Equal(t, "(n:`A`&`B`)", pattern)
		assert.Equal(t, []string{"elementId(n) = $p0", "n.`name` = $p1"}, conds)
		assert.Equal(t, map[string]any{"p0": "4:x:1", "p1": "O'Brien`"}, b.Params())
	})

	t.Run("inline folds conditions into the node pattern", func(t *testing.T) {
		inline, err := graphutil.NodeRef{Labels: []string{"A"}, Match: map[string]any{"k": "v"}}.Inline("s", graphutil.NewBuilder())
		require.NoError(t, err)
		assert.Equal(t, "(s:`A` WHERE s.`k` = $p0)", inline)
	})

	t.Run("null match values are rejected with a fix", func(t *testing.T) {
		_, _, err := graphutil.NodeRef{Match: map[string]any{"k": nil}}.Pattern("n", graphutil.NewBuilder())
		assert.ErrorContains(t, err, "IS NULL")
	})
}

func TestIntInRange(t *testing.T) {
	got, err := graphutil.IntInRange("limit", 0, 25, 1, 200)
	require.NoError(t, err)
	assert.Equal(t, 25, got)

	got, err = graphutil.IntInRange("limit", 50, 25, 1, 200)
	require.NoError(t, err)
	assert.Equal(t, 50, got)

	for _, n := range []int{-1, 201} {
		_, err = graphutil.IntInRange("limit", n, 25, 1, 200)
		assert.ErrorContains(t, err, "limit must be between 1 and 200")
	}
}

func TestQuantifiedRel(t *testing.T) {
	out, err := graphutil.QuantifiedRel("OUTGOING", ":`T`", 1, 3)
	require.NoError(t, err)
	assert.Equal(t, "-[:`T`]->{1,3}", out)
	in, _ := graphutil.QuantifiedRel("INCOMING", "", 2, 2)
	assert.Equal(t, "<-[]-{2,2}", in)
	both, _ := graphutil.QuantifiedRel("BOTH", "", 1, 1)
	assert.Equal(t, "-[]-{1,1}", both)
	_, err = graphutil.QuantifiedRel("SIDEWAYS", "", 1, 1)
	assert.ErrorContains(t, err, "OUTGOING, INCOMING or BOTH")
}

type testInput struct {
	Name string `json:"name"`
}

func call(t *testing.T, deps *tools.ToolDependencies, build func(*testInput) (graphutil.Query, error), args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	h := graphutil.NewHandler(deps, "test-tool", build)
	res, err := h(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: args}})
	require.NoError(t, err)
	return res
}

func errorText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	require.True(t, res.IsError)
	text, ok := mcpsdk.AsTextContent(res.Content[0])
	require.True(t, ok)
	return text.Text
}

func TestNewHandler(t *testing.T) {
	build := func(in *testInput) (graphutil.Query, error) {
		if in.Name == "bad" {
			return graphutil.Query{}, errors.New("name must not be bad")
		}
		return graphutil.Query{Cypher: "MATCH (n) RETURN n", Params: map[string]any{"p0": in.Name}, Write: in.Name == "write", ErrorHint: "try again"}, nil
	}

	t.Run("reports a missing database service", func(t *testing.T) {
		res := call(t, &tools.ToolDependencies{}, build, nil)
		assert.Contains(t, errorText(t, res), "not initialized")
	})

	t.Run("build errors reach the caller prefixed with the tool name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		res := call(t, &tools.ToolDependencies{DBService: dbmocks.NewMockService(ctrl)}, build, map[string]any{"name": "bad"})
		assert.Equal(t, "test-tool: name must not be bad", errorText(t, res))
	})

	t.Run("invalid argument types are reported with guidance", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		res := call(t, &tools.ToolDependencies{DBService: dbmocks.NewMockService(ctrl)}, build, map[string]any{"name": 5})
		assert.Contains(t, errorText(t, res), "input schema")
	})

	t.Run("reads run read-only with the Cypher 25 prefix and caps", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockDB := dbmocks.NewMockService(ctrl)
		result := &database.QueryResult{}
		mockDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), "CYPHER 25\nMATCH (n) RETURN n", map[string]any{"p0": "x"}, 7, 9).Return(result, nil)
		mockDB.EXPECT().QueryResultToJSON(result).Return(`{"rows":[]}`, nil)

		res := call(t, &tools.ToolDependencies{DBService: mockDB, CypherMaxRows: 7, CypherMaxBytes: 9}, build, map[string]any{"name": "x"})
		assert.False(t, res.IsError)
		assert.NotNil(t, res.StructuredContent)
	})

	t.Run("writes run in a write transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockDB := dbmocks.NewMockService(ctrl)
		result := &database.QueryResult{}
		mockDB.EXPECT().ExecuteWriteQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(result, nil)
		mockDB.EXPECT().QueryResultToJSON(result).Return(`{"rows":[]}`, nil)

		res := call(t, &tools.ToolDependencies{DBService: mockDB}, build, map[string]any{"name": "write"})
		assert.False(t, res.IsError)
	})

	t.Run("database errors get the tool's hint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockDB := dbmocks.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

		res := call(t, &tools.ToolDependencies{DBService: mockDB}, build, map[string]any{"name": "x"})
		assert.Equal(t, "test-tool failed: boom. try again", errorText(t, res))
	})

	t.Run("memory-limit errors say how to narrow the request and that retrying won't help", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockDB := dbmocks.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, errors.New("Neo.TransientError.General.MemoryPoolOutOfMemoryError (limit reached)"))

		res := call(t, &tools.ToolDependencies{DBService: mockDB}, build, map[string]any{"name": "x"})
		assert.Contains(t, errorText(t, res), "more memory than the server allows")
		assert.Contains(t, errorText(t, res), "relationshipTypes")
		assert.Contains(t, errorText(t, res), "will fail the same way")
	})

	t.Run("timeouts say how to narrow the request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockDB := dbmocks.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, context.DeadlineExceeded)

		res := call(t, &tools.ToolDependencies{DBService: mockDB}, build, map[string]any{"name": "x"})
		assert.Contains(t, errorText(t, res), "timed out")
		assert.Contains(t, errorText(t, res), "maxDepth")
	})
}

func TestDropNullProperties(t *testing.T) {
	in := `{"rows":[{"node":{"id":"1","labels":["P"],"properties":{"name":null,"age":30,"big":9007199254740993}},` +
		`"path":{"nodes":[{"id":"2","properties":{"x":null,"y":"keep"}}]},"message":null,"relationship":null}],` +
		`"rowCount":1,"truncated":true,"truncationReason":"rows","maxRows":5,"hint":"h"}`

	out, err := graphutil.DropNullProperties(in)
	require.NoError(t, err)

	assert.JSONEq(t, `{"rows":[{"node":{"id":"1","labels":["P"],"properties":{"age":30,"big":9007199254740993}},`+
		`"path":{"nodes":[{"id":"2","properties":{"y":"keep"}}]},"message":null,"relationship":null}],`+
		`"rowCount":1,"truncated":true,"truncationReason":"rows","maxRows":5,"hint":"h"}`, out)
	assert.Contains(t, out, "9007199254740993", "large integers must not be rounded through float64")
	assert.Less(t, strings.Index(out, `"rows"`), strings.Index(out, `"rowCount"`), "envelope field order is preserved")

	_, err = graphutil.DropNullProperties("not json")
	assert.Error(t, err)
}
