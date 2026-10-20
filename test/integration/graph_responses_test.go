// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graph"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/traversal"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

func TestGraphTools_WriteThatMatchesNothingIsNotReportedAsPerformed(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())
	person := tc.GetUniqueLabel("Person").String()

	res := tc.CallTool(graph.CreateNodeHandler(tc.Deps), map[string]any{"labels": []any{person}, "properties": map[string]any{"name": "Alice"}})
	nodeID := rowMap(t, tc.ParseCypherRecords(res)[0], "node")["id"].(string)
	missing := "4:00000000-0000-0000-0000-000000000000:99999"

	for _, c := range []struct {
		name    string
		handler mcpsdk.ToolHandlerFunc
		args    map[string]any
	}{
		{"update-node", graph.UpdateNodeHandler(tc.Deps), map[string]any{"id": missing, "set": map[string]any{"a": 1}}},
		{"delete-node", graph.DeleteNodeHandler(tc.Deps), map[string]any{"id": missing}},
		{"update-relationship", graph.UpdateRelationshipHandler(tc.Deps), map[string]any{"id": nodeID, "set": map[string]any{"a": 1}}},
		{"delete-relationship", graph.DeleteRelationshipHandler(tc.Deps), map[string]any{"id": nodeID}},
	} {
		rows := tc.ParseCypherRecords(tc.CallTool(c.handler, c.args))
		if len(rows) == 0 {
			t.Fatalf("%s returned no rows", c.name)
		}
		row := rows[0]
		msg, _ := row["message"].(string)
		if row["performed"] != false || row["matched"] != float64(0) || !strings.Contains(msg, "no ") || !strings.Contains(msg, "matched") {
			t.Errorf("%s with an id that matches nothing: expected performed=false, matched=0 and an explanation, got %v", c.name, row)
		}

		dry := map[string]any{}
		for k, v := range c.args {
			dry[k] = v
		}
		dry["dryRun"] = true
		dryRows := tc.ParseCypherRecords(tc.CallTool(c.handler, dry))
		if len(dryRows) == 0 {
			t.Fatalf("%s dryRun returned no rows", c.name)
		}
		row = dryRows[0]
		if msg, _ := row["message"].(string); row["matched"] != float64(0) || !strings.Contains(msg, "nothing matched") {
			t.Errorf("%s dryRun with nothing matched: expected an explanatory message, got %v", c.name, row)
		}
	}

	tc.VerifyNodeInDB(tc.GetUniqueLabel("Person"), map[string]any{"name": "Alice"})
}

func TestGraphAndTraversalTools_RejectUnknownArguments(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())

	msg := tc.GetToolError(traversal.ExpandNeighborsHandler(tc.Deps), map[string]any{
		"start":              map[string]any{"id": "4:x:1"},
		"neighborProperties": []any{"name"},
	})
	if !strings.Contains(msg, `unknown argument "neighborProperties"`) || !strings.Contains(msg, "nodeProperties") {
		t.Fatalf("expected an unknown-argument error listing the valid names, got: %s", msg)
	}

	msg = tc.GetToolError(graph.FindNodesHandler(tc.Deps), map[string]any{"label": []any{"Person"}})
	if !strings.Contains(msg, `unknown argument "label"`) || !strings.Contains(msg, "labels") {
		t.Fatalf("expected an unknown-argument error, got: %s", msg)
	}
}
