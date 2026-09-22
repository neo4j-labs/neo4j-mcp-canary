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

func TestExplainCypher(t *testing.T) {
	t.Parallel()

	t.Run("explains a read query without executing it", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		explain := cypher.ExplainCypherHandler(tc.Deps)
		res := tc.CallTool(explain, map[string]any{
			"query": "MATCH (p:" + personLabel.String() + ") RETURN p",
		})

		var plan cypher.PlanNode
		tc.ParseJSONResponse(res, &plan)
		if plan.Operator == "" {
			t.Fatalf("expected a non-empty root operator, got: %+v", plan)
		}

		// EXPLAIN never executes, so nothing should have been created — a
		// direct read confirms the label genuinely has zero nodes.
		nodes, err := tc.Service.ExecuteReadQuery(context.Background(), "MATCH (p:"+personLabel.String()+") RETURN p", map[string]any{})
		if err != nil {
			t.Fatalf("failed to verify no nodes were created: %v", err)
		}
		if len(nodes) != 0 {
			t.Fatalf("expected EXPLAIN to create nothing, found %d nodes", len(nodes))
		}
	})

	t.Run("explains a write query without executing it", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		explain := cypher.ExplainCypherHandler(tc.Deps)
		res := tc.CallTool(explain, map[string]any{
			"query": "CREATE (p:" + personLabel.String() + " {name: 'Alice'}) RETURN p",
		})

		var plan cypher.PlanNode
		tc.ParseJSONResponse(res, &plan)
		if plan.Operator == "" {
			t.Fatalf("expected a non-empty root operator, got: %+v", plan)
		}

		nodes, err := tc.Service.ExecuteReadQuery(context.Background(), "MATCH (p:"+personLabel.String()+") RETURN p", map[string]any{})
		if err != nil {
			t.Fatalf("failed to verify no nodes were created: %v", err)
		}
		if len(nodes) != 0 {
			t.Fatalf("expected EXPLAIN to create nothing even for a write statement, found %d nodes", len(nodes))
		}
	})

	t.Run("EXPLAIN-prefixed query rejected with own message, not the driver's", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		explain := cypher.ExplainCypherHandler(tc.Deps)
		textError := tc.GetToolError(explain, map[string]any{
			"query": "EXPLAIN MATCH (p:" + personLabel.String() + ") RETURN p",
		})
		if !strings.Contains(textError, "already prepends EXPLAIN") {
			t.Fatalf("expected explain-cypher's own double-prefix message, got: %s", textError)
		}
		if strings.Contains(textError, "conflicting") {
			t.Fatalf("raw driver error leaked through: %s", textError)
		}
	})

	t.Run("PROFILE-prefixed query rejected", func(t *testing.T) {
		tc := helpers.NewTestContext(t, dbs.GetDriver())
		personLabel := tc.GetUniqueLabel("Person")

		explain := cypher.ExplainCypherHandler(tc.Deps)
		textError := tc.GetToolError(explain, map[string]any{
			"query": "PROFILE MATCH (p:" + personLabel.String() + ") RETURN p",
		})
		if !strings.Contains(textError, "profile-cypher") {
			t.Fatalf("expected pointer to profile-cypher, got: %s", textError)
		}
	})
}
