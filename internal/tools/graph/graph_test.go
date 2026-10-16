// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateNode(t *testing.T) {
	t.Run("plain create binds properties as a parameter", func(t *testing.T) {
		q, err := buildCreateNode(&CreateNodeInput{Labels: []string{"Person", "Admin"}, Properties: map[string]any{"name": "Alice"}})
		require.NoError(t, err)
		assert.True(t, q.Write)
		assert.Contains(t, q.Cypher, "CREATE (n:`Person`:`Admin` $p0)")
		assert.Equal(t, map[string]any{"p0": map[string]any{"name": "Alice"}}, q.Params)
	})

	t.Run("mergeOn merges on the identifying properties only", func(t *testing.T) {
		q, err := buildCreateNode(&CreateNodeInput{Labels: []string{"Person"}, Properties: map[string]any{"email": "a@x", "name": "A"}, MergeOn: []string{"email"}})
		require.NoError(t, err)
		assert.Contains(t, q.Cypher, "MERGE (n:`Person` {`email`: $p0})\nSET n += $p1")
		assert.Equal(t, "a@x", q.Params["p0"])
	})

	t.Run("hostile labels cannot break out of the identifier", func(t *testing.T) {
		q, err := buildCreateNode(&CreateNodeInput{Labels: []string{"X`) DETACH DELETE (m //"}})
		require.NoError(t, err)
		assert.Contains(t, q.Cypher, "(n:`X``) DETACH DELETE (m //`")
	})

	for name, tc := range map[string]struct {
		in   CreateNodeInput
		want string
	}{
		"labels required":         {CreateNodeInput{}, "labels is required"},
		"mergeOn must be present": {CreateNodeInput{Labels: []string{"P"}, MergeOn: []string{"id"}}, `mergeOn property "id" must also be present`},
		"empty property name":     {CreateNodeInput{Labels: []string{"P"}, Properties: map[string]any{"": 1}}, "empty property name"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildCreateNode(&tc.in)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBuildFindAndCountNodes(t *testing.T) {
	q, err := buildFindNodes(&FindNodesInput{
		NodeRef: graphutil.NodeRef{Labels: []string{"Person"}, Match: map[string]any{"city": "Oslo"}},
		OrderBy: "name", OrderDirection: "desc", ReturnProperties: []string{"name"}, Limit: 10, Skip: 5,
	})
	require.NoError(t, err)
	assert.False(t, q.Write)
	assert.Equal(t, "MATCH (n:`Person`) WHERE n.`city` = $p0\n"+
		"RETURN {id: elementId(n), labels: labels(n), properties: n{.`name`}} AS node\n"+
		"ORDER BY n.`name` DESC\nSKIP $p1\nLIMIT $p2", q.Cypher)
	assert.Equal(t, map[string]any{"p0": "Oslo", "p1": 5, "p2": 10}, q.Params)

	_, err = buildFindNodes(&FindNodesInput{Limit: 1000})
	assert.ErrorContains(t, err, "limit must be between 1 and 200")
	_, err = buildFindNodes(&FindNodesInput{OrderBy: "x", OrderDirection: "sideways"})
	assert.ErrorContains(t, err, "ASC or DESC")

	q, err = buildCountNodes(&CountNodesInput{})
	require.NoError(t, err)
	assert.Equal(t, "MATCH (n)\nRETURN count(n) AS count", q.Cypher)
}

func TestBuildGetNode(t *testing.T) {
	_, err := buildGetNode(&GetNodeInput{NodeRef: graphutil.NodeRef{Labels: []string{"Person"}}})
	assert.ErrorContains(t, err, "provide id, match or filters")

	q, err := buildGetNode(&GetNodeInput{NodeRef: graphutil.NodeRef{ID: "4:x:1"}})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "WITH n LIMIT 5")
	assert.Contains(t, q.Cypher, "relationshipCounts")
}

func TestBuildUpdateNode(t *testing.T) {
	t.Run("refuses to select by label alone", func(t *testing.T) {
		_, err := buildUpdateNode(&UpdateNodeInput{NodeRef: graphutil.NodeRef{Labels: []string{"P"}}, Set: map[string]any{"a": 1}})
		assert.ErrorContains(t, err, "labels alone would select every node")
	})
	t.Run("needs something to change", func(t *testing.T) {
		_, err := buildUpdateNode(&UpdateNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}})
		assert.ErrorContains(t, err, "nothing to update")
	})
	t.Run("bounds maxAffected", func(t *testing.T) {
		_, err := buildUpdateNode(&UpdateNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}, Set: map[string]any{"a": 1}, MaxAffected: 5000})
		assert.ErrorContains(t, err, "maxAffected must be between 1 and 1000")
	})
	t.Run("guarded write with set, remove and labels", func(t *testing.T) {
		q, err := buildUpdateNode(&UpdateNodeInput{
			NodeRef: graphutil.NodeRef{ID: "1"}, Set: map[string]any{"a": 1}, Remove: []string{"old"},
			AddLabels: []string{"New"}, RemoveLabels: []string{"Old"},
		})
		require.NoError(t, err)
		assert.True(t, q.Write)
		assert.Contains(t, q.Cypher, "matched <= $p2 AS allowed")
		assert.Contains(t, q.Cypher, "SET n += $p1\n  REMOVE n.`old`, n:`Old`\n  SET n:`New`")
		assert.Equal(t, 1, q.Params["p2"], "maxAffected defaults to 1")
	})
	t.Run("dryRun is a read that changes nothing", func(t *testing.T) {
		q, err := buildUpdateNode(&UpdateNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}, Set: map[string]any{"a": 1}, DryRun: true})
		require.NoError(t, err)
		assert.False(t, q.Write)
		assert.NotContains(t, q.Cypher, "SET")
		assert.Contains(t, q.Cypher, "count(n) AS matched")
	})
}

func TestBuildDeleteNode(t *testing.T) {
	_, err := buildDeleteNode(&DeleteNodeInput{})
	assert.ErrorContains(t, err, "provide id, match or filters")

	q, err := buildDeleteNode(&DeleteNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}})
	require.NoError(t, err)
	assert.True(t, q.Write)
	assert.Contains(t, q.Cypher, "  DELETE n\n")
	assert.NotContains(t, q.Cypher, "DETACH")
	assert.Contains(t, q.ErrorHint, "detach=true")

	q, err = buildDeleteNode(&DeleteNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}, Detach: true, MaxAffected: 10})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "DETACH DELETE n")
	assert.Equal(t, 10, q.Params["p1"])

	q, err = buildDeleteNode(&DeleteNodeInput{NodeRef: graphutil.NodeRef{ID: "1"}, DryRun: true})
	require.NoError(t, err)
	assert.False(t, q.Write)
	assert.NotContains(t, q.Cypher, "DELETE")
}

func TestBuildCreateRelationship(t *testing.T) {
	good := CreateRelationshipInput{From: graphutil.NodeRef{ID: "1"}, To: graphutil.NodeRef{ID: "2"}, Type: "KNOWS", Properties: map[string]any{"since": 2020}}

	q, err := buildCreateRelationship(&good)
	require.NoError(t, err)
	assert.True(t, q.Write)
	assert.Contains(t, q.Cypher, "MERGE (src)-[r:`KNOWS`]->(dst)")
	assert.Contains(t, q.Cypher, "OPTIONAL CALL (sources, targets)")

	good.AllowDuplicate = true
	q, err = buildCreateRelationship(&good)
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "CREATE (src)-[r:`KNOWS` $p")
	assert.NotContains(t, q.Cypher, "MERGE")

	for name, mutate := range map[string]func(*CreateRelationshipInput){
		"type required":    func(in *CreateRelationshipInput) { in.Type = "" },
		"from needs a key": func(in *CreateRelationshipInput) { in.From = graphutil.NodeRef{Labels: []string{"P"}} },
		"to needs a key":   func(in *CreateRelationshipInput) { in.To = graphutil.NodeRef{} },
	} {
		t.Run(name, func(t *testing.T) {
			in := CreateRelationshipInput{From: graphutil.NodeRef{ID: "1"}, To: graphutil.NodeRef{ID: "2"}, Type: "KNOWS"}
			mutate(&in)
			_, err := buildCreateRelationship(&in)
			assert.Error(t, err)
		})
	}
}

func TestRelationshipMatchClause(t *testing.T) {
	b := graphutil.NewBuilder()
	match, err := RelRef{
		Type: "KNOWS", From: &graphutil.NodeRef{Labels: []string{"Person"}, ID: "1"}, Match: map[string]any{"since": 2020},
	}.matchClause(b)
	require.NoError(t, err)
	assert.Equal(t, "MATCH (src:`Person`)-[r:`KNOWS`]->(dst) WHERE elementId(src) = $p0 AND r.`since` = $p1", match)

	assert.False(t, RelRef{Type: "KNOWS"}.HasPredicate(), "a type alone must not select relationships for mutation")
	assert.True(t, RelRef{To: &graphutil.NodeRef{ID: "1"}}.HasPredicate())
}

func TestBuildFindUpdateDeleteRelationships(t *testing.T) {
	q, err := buildFindRelationships(&FindRelationshipsInput{RelRef: RelRef{Type: "KNOWS"}, EndpointProperties: []string{"name"}})
	require.NoError(t, err)
	assert.False(t, q.Write)
	assert.Contains(t, q.Cypher, "AS fromNode")
	assert.Contains(t, q.Cypher, "properties: src{.`name`}")

	_, err = buildUpdateRelationship(&UpdateRelationshipInput{RelRef: RelRef{Type: "KNOWS"}, Set: map[string]any{"a": 1}})
	assert.ErrorContains(t, err, "a type alone would select every relationship")
	_, err = buildUpdateRelationship(&UpdateRelationshipInput{RelRef: RelRef{ID: "1"}})
	assert.ErrorContains(t, err, "nothing to update")
	q, err = buildUpdateRelationship(&UpdateRelationshipInput{RelRef: RelRef{ID: "1"}, Set: map[string]any{"w": 1}, Remove: []string{"x"}})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "SET r += $p")
	assert.Contains(t, q.Cypher, "REMOVE r.`x`")

	_, err = buildDeleteRelationship(&DeleteRelationshipInput{RelRef: RelRef{Type: "KNOWS"}})
	assert.ErrorContains(t, err, "a type alone would select every relationship")
	q, err = buildDeleteRelationship(&DeleteRelationshipInput{RelRef: RelRef{ID: "1"}})
	require.NoError(t, err)
	assert.Contains(t, q.Cypher, "DELETE r")
	assert.True(t, q.Write)
}

func TestSpecAnnotations(t *testing.T) {
	assert.True(t, FindNodesSpec().Annotations.ReadOnlyHint)
	assert.False(t, CreateNodeSpec().Annotations.ReadOnlyHint)
	require.NotNil(t, DeleteNodeSpec().Annotations.DestructiveHint)
	assert.True(t, *DeleteNodeSpec().Annotations.DestructiveHint)
	assert.False(t, DeleteRelationshipSpec().Annotations.ReadOnlyHint)
}
