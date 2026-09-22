// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

func TestWriteCypher(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())

	personLabel := tc.GetUniqueLabel("Person")

	write := cypher.WriteCypherHandler(tc.Deps)
	tc.CallTool(write, map[string]any{
		"query":  "CREATE (p:" + personLabel + " {name: $name}) RETURN p",
		"params": map[string]any{"name": "Alice"},
	})

	tc.VerifyNodeInDB(personLabel, map[string]any{"name": "Alice"})
}

func TestWriteCypher_RejectsProfilePrefix(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())
	personLabel := tc.GetUniqueLabel("Person")

	write := cypher.WriteCypherHandler(tc.Deps)
	textError := tc.GetToolError(write, map[string]any{
		"query": "PROFILE CREATE (p:" + personLabel.String() + ") RETURN p",
	})
	if !strings.Contains(textError, "profile-cypher") {
		t.Fatalf("expected write-cypher to reject the PROFILE prefix and point at profile-cypher, got: %s", textError)
	}

	// Confirm the rejected statement really wasn't executed.
	nodes, err := tc.Service.ExecuteReadQuery(context.Background(), "MATCH (p:"+personLabel.String()+") RETURN p", map[string]any{})
	if err != nil {
		t.Fatalf("failed to verify no node was created: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected rejected PROFILE query to create nothing, found %d nodes", len(nodes))
	}
}
