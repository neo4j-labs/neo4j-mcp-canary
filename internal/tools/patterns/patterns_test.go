// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package patterns

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pnode(alias string, labels ...string) PatternNode {
	return PatternNode{Alias: alias, NodeRef: graphutil.NodeRef{Labels: labels}}
}

func TestBuildMatchPattern(t *testing.T) {
	in := func(mode string) *MatchPatternInput {
		return &MatchPatternInput{
			Nodes: []PatternNode{
				{Alias: "p", NodeRef: graphutil.NodeRef{Labels: []string{"Person"}, Match: map[string]any{"name": "Alice"}}},
				pnode("c", "Company"),
			},
			Relationships: []PatternRel{{From: "p", To: "c", Types: []string{"WORKS_AT"}}},
			Mode:          mode,
		}
	}

	t.Run("rows", func(t *testing.T) {
		q, err := buildMatchPattern(in(""))
		require.NoError(t, err)
		assert.False(t, q.Write)
		assert.Contains(t, q.Cypher, "MATCH (`p`)-[r0:`WORKS_AT`]->(`c`) WHERE `p`:`Person` AND `p`.`name` = $p0 AND `c`:`Company` AND `p` <> `c`")
		assert.Contains(t, q.Cypher, "RETURN {`p`: {id: elementId(`p`)")
		assert.Contains(t, q.Cypher, "LIMIT $p1")
	})

	t.Run("count and exists", func(t *testing.T) {
		q, err := buildMatchPattern(in("count"))
		require.NoError(t, err)
		assert.Contains(t, q.Cypher, "RETURN count(*) AS count")
		q, err = buildMatchPattern(in("exists"))
		require.NoError(t, err)
		assert.True(t, len(q.Cypher) > 0 && q.Cypher[:len("RETURN EXISTS {")] == "RETURN EXISTS {")
		assert.Contains(t, q.Cypher, "AS exists")
	})

	t.Run("allowSameNode drops the distinctness conditions", func(t *testing.T) {
		i := in("")
		i.AllowSameNode = true
		q, err := buildMatchPattern(i)
		require.NoError(t, err)
		assert.NotContains(t, q.Cypher, "<>")
	})

	t.Run("hostile aliases are quoted", func(t *testing.T) {
		q, err := buildMatchPattern(&MatchPatternInput{Nodes: []PatternNode{pnode("a`) DETACH DELETE (b //")}})
		require.NoError(t, err)
		assert.Contains(t, q.Cypher, "(`a``) DETACH DELETE (b //`)")
	})

	for name, tc := range map[string]struct {
		mutate func(*MatchPatternInput)
		want   string
	}{
		"no nodes":           {func(i *MatchPatternInput) { i.Nodes = nil }, "nodes is required"},
		"blank alias":        {func(i *MatchPatternInput) { i.Nodes[0].Alias = " " }, "alias is required"},
		"duplicate alias":    {func(i *MatchPatternInput) { i.Nodes[1].Alias = "p" }, "used more than once"},
		"unknown alias":      {func(i *MatchPatternInput) { i.Relationships[0].To = "zzz" }, `alias "zzz" which is not defined in nodes; the defined aliases are "c", "p"`},
		"disconnected":       {func(i *MatchPatternInput) { i.Relationships = nil }, "not connected"},
		"too many nodes":     {func(i *MatchPatternInput) { i.Nodes = append(i.Nodes, make([]PatternNode, 5)...) }, "too large"},
		"bad mode":           {func(i *MatchPatternInput) { i.Mode = "all" }, `"rows", "count" or "exists"`},
		"limit out of range": {func(i *MatchPatternInput) { i.Limit = 201 }, "limit must be between 1 and 200"},
	} {
		t.Run(name, func(t *testing.T) {
			i := in("")
			tc.mutate(i)
			_, err := buildMatchPattern(i)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuildFindCommonConnections(t *testing.T) {
	q, err := buildFindCommonConnections(&FindCommonConnectionsInput{
		Nodes:      []graphutil.NodeRef{{ID: "a"}, {ID: "b"}},
		Direction:  "OUTGOING",
		Connection: &graphutil.NodeRef{Labels: []string{"Library"}},
	})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "MATCH (n0)-[r0]->(c), (n1)-[r1]->(c)")
	assert.Contains(t, q.Cypher, "c <> n0")
	assert.Contains(t, q.Cypher, "n1 <> n0")
	assert.Contains(t, q.Cypher, "c:`Library`")
	assert.Contains(t, q.Cypher, "[type(r0), type(r1)] AS relationshipTypes")

	_, err = buildFindCommonConnections(&FindCommonConnectionsInput{Nodes: []graphutil.NodeRef{{ID: "a"}}})
	assert.ErrorContains(t, err, "between 2 and 5")
	_, err = buildFindCommonConnections(&FindCommonConnectionsInput{Nodes: []graphutil.NodeRef{{ID: "a"}, {Labels: []string{"X"}}}})
	assert.ErrorContains(t, err, "nodes[1] must select a node")
	_, err = buildFindCommonConnections(&FindCommonConnectionsInput{Nodes: []graphutil.NodeRef{{ID: "a"}, {ID: "b"}}, Direction: "UP"})
	assert.ErrorContains(t, err, "OUTGOING, INCOMING or BOTH")
}

func TestBuildFindMotif(t *testing.T) {
	deps := []string{"DEPENDS_ON"}

	q, err := buildFindMotif(&FindMotifInput{Motif: "fan-in", RelationshipTypes: deps, NodeLabels: []string{"Svc"}, MinDegree: 4})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "MATCH (h:`Svc`)")
	assert.Contains(t, q.Cypher, "COUNT { (h)<-[:`DEPENDS_ON`]-() } AS degree")
	assert.Equal(t, 4, q.Params["p0"])

	q, err = buildFindMotif(&FindMotifInput{Motif: "star"})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "COUNT { (h)-[]-() }")

	q, err = buildFindMotif(&FindMotifInput{Motif: "triangle", RelationshipTypes: deps})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "elementId(a) < elementId(b) AND elementId(b) < elementId(c)")

	q, err = buildFindMotif(&FindMotifInput{Motif: "triangle", Start: &graphutil.NodeRef{ID: "x"}})
	require.NoError(t, err)
	assert.NotContains(t, q.Cypher, "elementId(a) <", "an anchored triangle must not require the anchor to be the smallest id")

	q, err = buildFindMotif(&FindMotifInput{Motif: "cycle", CycleLength: 4, NodeLabels: []string{"Svc"}})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "(a:`Svc`)-[]->{4,4}(a)")
	assert.Contains(t, q.Cypher, "elementId(x) >= elementId(ns[0])")

	for name, tc := range map[string]struct {
		in   FindMotifInput
		want string
	}{
		"unknown motif":    {FindMotifInput{Motif: "clique"}, `motif must be one of "star"`},
		"degree cap":       {FindMotifInput{Motif: "star", MinDegree: 200_000_000}, "minDegree must be between 1 and 100000000"},
		"cycle too long":   {FindMotifInput{Motif: "cycle", CycleLength: 7}, "cycleLength must be between 2 and 6"},
		"limit cap":        {FindMotifInput{Motif: "star", Limit: 101}, "limit must be between 1 and 100"},
		"start with match": {FindMotifInput{Motif: "star", Start: &graphutil.NodeRef{Match: map[string]any{"a": nil}}}, "IS NULL"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildFindMotif(&tc.in)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuildFindRelationshipChain(t *testing.T) {
	q, err := buildFindRelationshipChain(&FindRelationshipChainInput{
		Start: graphutil.NodeRef{ID: "p"},
		Steps: []ChainStep{
			{Type: "WORKS_AT"},
			{Type: "LOCATED_IN", Direction: "INCOMING", Node: &graphutil.NodeRef{Labels: []string{"City"}}},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "MATCH p = (n0 WHERE elementId(n0) = $p0)-[:`WORKS_AT`]->(n1)<-[:`LOCATED_IN`]-(n2:`City`)")

	_, err = buildFindRelationshipChain(&FindRelationshipChainInput{Start: graphutil.NodeRef{ID: "p"}})
	assert.ErrorContains(t, err, "between 1 and 6")
	_, err = buildFindRelationshipChain(&FindRelationshipChainInput{Steps: []ChainStep{{}}})
	assert.ErrorContains(t, err, "start must select a node")
	_, err = buildFindRelationshipChain(&FindRelationshipChainInput{Start: graphutil.NodeRef{ID: "p"}, Steps: []ChainStep{{Direction: "UP"}}})
	assert.ErrorContains(t, err, "steps[0]")
}

func TestBuildFindSubgraph(t *testing.T) {
	q, err := buildFindSubgraph(&FindSubgraphInput{Start: graphutil.NodeRef{ID: "a"}, RelationshipTypes: []string{"T"}, MaxDepth: 3, MaxNodes: 20})
	require.NoError(t, err)
	assert.False(t, q.Write)
	assert.Contains(t, q.Cypher, "MATCH ANY SHORTEST (s)-[:`T`]-{1,3}(n)")
	assert.Contains(t, q.Cypher, "MATCH (x)-[r:`T`]->(y)")
	assert.Contains(t, q.Cypher, "everything[..$p1]")

	_, err = buildFindSubgraph(&FindSubgraphInput{})
	assert.ErrorContains(t, err, "start must select a node")
	_, err = buildFindSubgraph(&FindSubgraphInput{Start: graphutil.NodeRef{ID: "a"}, MaxDepth: 5})
	assert.ErrorContains(t, err, "maxDepth must be between 1 and 4")
	_, err = buildFindSubgraph(&FindSubgraphInput{Start: graphutil.NodeRef{ID: "a"}, MaxNodes: 201})
	assert.ErrorContains(t, err, "maxNodes must be between 1 and 200")
}
