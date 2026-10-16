// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package traversal

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var startA = graphutil.NodeRef{ID: "a"}

func TestBuildTraverse(t *testing.T) {
	t.Run("nodes mode uses ANY SHORTEST so reachability stays cheap", func(t *testing.T) {
		q, err := buildTraverse(&TraverseInput{Start: startA, RelationshipTypes: []string{"DEPENDS_ON"}, MaxDepth: 4})
		require.NoError(t, err)
		assert.False(t, q.Write)
		assert.Equal(t, "MATCH p = ANY SHORTEST (s WHERE elementId(s) = $p0)-[:`DEPENDS_ON`]->{1,4}(t)\n"+
			"RETURN elementId(s) AS startId, {id: elementId(t), labels: labels(t)} AS node, length(p) AS depth\n"+
			"ORDER BY depth\nLIMIT $p1", q.Cypher)
		assert.Equal(t, map[string]any{"p0": "a", "p1": 50}, q.Params)
	})

	t.Run("paths mode with direction and target", func(t *testing.T) {
		q, err := buildTraverse(&TraverseInput{
			Start: startA, Direction: "INCOMING", ReturnMode: "paths", MinDepth: 2, MaxDepth: 3,
			Target: &graphutil.NodeRef{Labels: []string{"Svc"}},
		})
		require.NoError(t, err)
		assert.Contains(t, q.Cypher, "<-[]-{2,3}(t:`Svc`)")
		assert.Contains(t, q.Cypher, "AS path")
	})

	for name, tc := range map[string]struct {
		in   TraverseInput
		want string
	}{
		"start needs a key":     {TraverseInput{Start: graphutil.NodeRef{Labels: []string{"A"}}}, "start must select a node"},
		"depth is capped":       {TraverseInput{Start: startA, MaxDepth: 11}, "maxDepth must be between 1 and 10"},
		"min cannot pass max":   {TraverseInput{Start: startA, MinDepth: 3, MaxDepth: 2}, "minDepth cannot exceed maxDepth"},
		"bad direction":         {TraverseInput{Start: startA, Direction: "UP"}, "OUTGOING, INCOMING or BOTH"},
		"bad return mode":       {TraverseInput{Start: startA, ReturnMode: "graph"}, `"nodes" or "paths"`},
		"limit is capped":       {TraverseInput{Start: startA, Limit: 501}, "limit must be between 1 and 500"},
		"blank relationship ty": {TraverseInput{Start: startA, RelationshipTypes: []string{""}}, "blank"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildTraverse(&tc.in)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuildExpandNeighbors(t *testing.T) {
	q, err := buildExpandNeighbors(&ExpandNeighborsInput{
		Start: startA, RelationshipTypes: []string{"KNOWS", "WORKS_WITH"}, Direction: "OUTGOING",
		Neighbor: &graphutil.NodeRef{Labels: []string{"Person"}}, RelationshipProperties: []string{"since"},
	})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "MATCH (s)-[r:`KNOWS`|`WORKS_WITH`]->(n:`Person`) WHERE elementId(s) = $p0")
	assert.Contains(t, q.Cypher, "properties: r{.`since`}}")
	assert.Contains(t, q.Cypher, "CASE WHEN startNode(r) = s THEN 'OUTGOING' ELSE 'INCOMING' END")

	q, err = buildExpandNeighbors(&ExpandNeighborsInput{Start: startA})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "(s)-[r]-(n)", "direction defaults to BOTH")

	_, err = buildExpandNeighbors(&ExpandNeighborsInput{Start: startA, Limit: 500})
	assert.ErrorContains(t, err, "limit must be between 1 and 200")
}

func TestBuildPaths(t *testing.T) {
	to := graphutil.NodeRef{ID: "z"}

	q, err := buildFindShortestPath(&FindShortestPathInput{From: startA, To: to})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "MATCH p = ANY SHORTEST (a WHERE elementId(a) = $p0)-[]-{1,6}(z WHERE elementId(z) = $p1)")
	assert.Contains(t, q.Cypher, "WHERE a <> z")

	q, err = buildFindShortestPath(&FindShortestPathInput{From: startA, To: to, AllShortest: true, Direction: "OUTGOING"})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "ALL SHORTEST")
	assert.Contains(t, q.Cypher, "->{1,6}")

	_, err = buildFindShortestPath(&FindShortestPathInput{From: startA})
	assert.ErrorContains(t, err, "to must select a node")
	_, err = buildFindShortestPath(&FindShortestPathInput{From: startA, To: to, MaxDepth: 16})
	assert.ErrorContains(t, err, "maxDepth must be between 1 and 15")

	q, err = buildFindAllPaths(&FindAllPathsInput{From: startA, To: to, MinDepth: 2, MaxDepth: 5})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "-[]-{2,5}")
	assert.Contains(t, q.Cypher, "ORDER BY length(p)")
	assert.Equal(t, 20, q.Params["p2"])

	_, err = buildFindAllPaths(&FindAllPathsInput{From: startA, To: to, MaxDepth: 9})
	assert.ErrorContains(t, err, "maxDepth must be between 1 and 8")
	_, err = buildFindAllPaths(&FindAllPathsInput{From: startA, To: to, Limit: 101})
	assert.ErrorContains(t, err, "limit must be between 1 and 100")
}

func TestSpecsAreReadOnly(t *testing.T) {
	for _, spec := range []struct {
		name     string
		readOnly bool
	}{
		{TraverseSpec().Name, TraverseSpec().Annotations.ReadOnlyHint},
		{ExpandNeighborsSpec().Name, ExpandNeighborsSpec().Annotations.ReadOnlyHint},
		{FindShortestPathSpec().Name, FindShortestPathSpec().Annotations.ReadOnlyHint},
		{FindAllPathsSpec().Name, FindAllPathsSpec().Annotations.ReadOnlyHint},
	} {
		assert.True(t, spec.readOnly, spec.name)
	}
}
