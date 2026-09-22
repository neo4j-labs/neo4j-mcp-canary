// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

// profileCypherResponse mirrors cypher.ProfileCypherResponse's JSON shape
// for parsing in tests — the exported type embeds database.CypherResponse,
// which flattens the same fields ParseCypherEnvelope already knows, plus a
// nested "profile" object.
type profileCypherResponse struct {
	Rows      []map[string]any   `json:"rows"`
	RowCount  int                `json:"rowCount"`
	Truncated bool               `json:"truncated"`
	Profile   cypher.ProfileNode `json:"profile"`
}

func TestProfileCypher(t *testing.T) {
	t.Parallel()

	t.Run("profiles a query touching real data and returns runtime stats", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())

		personLabel, err := tc.SeedNode("Person", map[string]any{"name": "Alice"})
		if err != nil {
			t.Fatalf("failed to seed data: %v", err)
		}

		profile := cypher.ProfileCypherHandler(tc.Deps)
		res := tc.CallTool(profile, map[string]any{
			"query": "MATCH (p:" + personLabel.String() + " {name: 'Alice'}) RETURN p",
		})

		var parsed profileCypherResponse
		tc.ParseJSONResponse(res, &parsed)

		if parsed.RowCount != 1 {
			t.Fatalf("expected 1 row, got %d", parsed.RowCount)
		}
		if parsed.Profile.Operator == "" {
			t.Fatalf("expected a non-empty root operator in the profile tree, got: %+v", parsed.Profile)
		}
		// At least one operator in the tree should report a reported DbHits
		// value — PROFILE genuinely executed and instrumented the plan.
		if !anyOperatorReportsDbHits(parsed.Profile) {
			t.Fatalf("expected at least one operator to report dbHits, got: %+v", parsed.Profile)
		}
	})

	t.Run("PROFILE-prefixed query rejected with own message, not the driver's", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		profile := cypher.ProfileCypherHandler(tc.Deps)
		textError := tc.GetToolError(profile, map[string]any{
			"query": "PROFILE MATCH (p:" + personLabel.String() + ") RETURN p",
		})
		if !strings.Contains(textError, "already prepends PROFILE") {
			t.Fatalf("expected profile-cypher's own double-prefix message, got: %s", textError)
		}
	})

	t.Run("EXPLAIN-prefixed query rejected", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		profile := cypher.ProfileCypherHandler(tc.Deps)
		textError := tc.GetToolError(profile, map[string]any{
			"query": "EXPLAIN MATCH (p:" + personLabel.String() + ") RETURN p",
		})
		if !strings.Contains(textError, "explain-cypher") {
			t.Fatalf("expected pointer to explain-cypher, got: %s", textError)
		}
	})
}

func anyOperatorReportsDbHits(node cypher.ProfileNode) bool {
	if node.DBHits != nil {
		return true
	}
	for _, child := range node.Children {
		if anyOperatorReportsDbHits(child) {
			return true
		}
	}
	return false
}
