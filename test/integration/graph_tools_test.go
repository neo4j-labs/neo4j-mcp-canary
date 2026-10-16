// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graph"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

func rowMap(t *testing.T, row map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := row[key].(map[string]any)
	if !ok {
		t.Fatalf("expected %q to be an object in row %v", key, row)
	}
	return m
}

func TestGraphTools_NodeAndRelationshipLifecycle(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())
	person := tc.GetUniqueLabel("Person").String()

	create := graph.CreateNodeHandler(tc.Deps)
	find := graph.FindNodesHandler(tc.Deps)
	get := graph.GetNodeHandler(tc.Deps)
	count := graph.CountNodesHandler(tc.Deps)
	update := graph.UpdateNodeHandler(tc.Deps)
	del := graph.DeleteNodeHandler(tc.Deps)
	createRel := graph.CreateRelationshipHandler(tc.Deps)
	findRel := graph.FindRelationshipsHandler(tc.Deps)
	updateRel := graph.UpdateRelationshipHandler(tc.Deps)
	delRel := graph.DeleteRelationshipHandler(tc.Deps)

	// create-node, including integer preservation and mergeOn idempotence
	res := tc.CallTool(create, map[string]any{"labels": []any{person}, "properties": map[string]any{"name": "Alice", "age": 30}, "mergeOn": []any{"name"}})
	aliceNode := rowMap(t, tc.ParseCypherRecords(res)[0], "node")
	aliceID := aliceNode["id"].(string)
	tc.CallTool(create, map[string]any{"labels": []any{person}, "properties": map[string]any{"name": "Alice", "age": 31}, "mergeOn": []any{"name"}})
	res = tc.CallTool(create, map[string]any{"labels": []any{person}, "properties": map[string]any{"name": "Bob", "age": 40}})
	bobID := rowMap(t, tc.ParseCypherRecords(res)[0], "node")["id"].(string)
	tc.VerifyNodeInDB(tc.GetUniqueLabel("Person"), map[string]any{"name": "Alice", "age": int64(31)})

	res = tc.CallTool(count, map[string]any{"labels": []any{person}})
	if got := tc.ParseCypherRecords(res)[0]["count"]; got != float64(2) {
		t.Fatalf("expected mergeOn to leave exactly 2 nodes, count=%v", got)
	}

	// find-nodes with filter + projection + ordering
	res = tc.CallTool(find, map[string]any{
		"labels": []any{person}, "filters": []any{map[string]any{"property": "age", "operator": ">=", "value": 35}},
		"returnProperties": []any{"name"}, "orderBy": "name",
	})
	rows := tc.ParseCypherRecords(res)
	if len(rows) != 1 || rowMap(t, rowMap(t, rows[0], "node"), "properties")["name"] != "Bob" {
		t.Fatalf("expected only Bob, got %v", rows)
	}

	// relationships: create is idempotent, ambiguous endpoints are reported
	relArgs := map[string]any{"from": map[string]any{"id": aliceID}, "to": map[string]any{"id": bobID}, "type": "KNOWS", "properties": map[string]any{"since": 2020}}
	res = tc.CallTool(createRel, relArgs)
	row := tc.ParseCypherRecords(res)[0]
	relID := rowMap(t, row, "relationship")["id"].(string)
	tc.CallTool(createRel, relArgs)
	res = tc.CallTool(createRel, map[string]any{"from": map[string]any{"labels": []any{person}, "filters": []any{map[string]any{"property": "age", "operator": ">", "value": 0}}}, "to": map[string]any{"id": bobID}, "type": "KNOWS"})
	row = tc.ParseCypherRecords(res)[0]
	if row["relationship"] != nil || row["fromMatched"] != float64(2) || !strings.Contains(row["message"].(string), "exactly one") {
		t.Fatalf("expected ambiguous from to be reported, got %v", row)
	}

	res = tc.CallTool(findRel, map[string]any{"type": "KNOWS", "from": map[string]any{"id": aliceID}, "endpointProperties": []any{"name"}})
	rows = tc.ParseCypherRecords(res)
	if len(rows) != 1 || rowMap(t, rowMap(t, rows[0], "toNode"), "properties")["name"] != "Bob" {
		t.Fatalf("expected exactly one KNOWS relationship to Bob, got %v", rows)
	}

	// get-node reports relationship counts
	res = tc.CallTool(get, map[string]any{"id": aliceID})
	row = tc.ParseCypherRecords(res)[0]
	counts := row["relationshipCounts"].([]any)
	if len(counts) != 1 || counts[0].(map[string]any)["direction"] != "OUTGOING" {
		t.Fatalf("expected one OUTGOING relationship count, got %v", counts)
	}

	// update-node: guard refuses multi-node updates, dryRun changes nothing
	res = tc.CallTool(update, map[string]any{"labels": []any{person}, "filters": []any{map[string]any{"property": "age", "operator": ">", "value": 0}}, "set": map[string]any{"vip": true}})
	row = tc.ParseCypherRecords(res)[0]
	if row["performed"] != false || row["matched"] != float64(2) {
		t.Fatalf("expected update over 2 nodes to be refused, got %v", row)
	}
	res = tc.CallTool(update, map[string]any{"id": aliceID, "set": map[string]any{"vip": true}, "dryRun": true})
	if row = tc.ParseCypherRecords(res)[0]; row["performed"] != false || row["matched"] != float64(1) {
		t.Fatalf("expected dryRun to report 1 match, got %v", row)
	}
	res = tc.CallTool(update, map[string]any{"id": aliceID, "set": map[string]any{"vip": true}, "addLabels": []any{person + "_Extra"}})
	if row = tc.ParseCypherRecords(res)[0]; row["performed"] != true {
		t.Fatalf("expected update to be performed, got %v", row)
	}

	// update-relationship / delete-relationship
	res = tc.CallTool(updateRel, map[string]any{"id": relID, "set": map[string]any{"since": 2021}})
	updated := rowMap(t, tc.ParseCypherRecords(res)[0]["updated"].([]any)[0].(map[string]any), "properties")
	if updated["since"] != float64(2021) {
		t.Fatalf("expected since=2021, got %v", updated)
	}
	res = tc.CallTool(delRel, map[string]any{"id": relID, "dryRun": true})
	if row = tc.ParseCypherRecords(res)[0]; row["matched"] != float64(1) {
		t.Fatalf("expected dryRun to match 1 relationship, got %v", row)
	}

	// delete-node: refuses while related, works with detach
	errText := tc.GetToolError(del, map[string]any{"id": bobID})
	if !strings.Contains(errText, "detach=true") {
		t.Fatalf("expected an error pointing at detach=true, got: %s", errText)
	}
	res = tc.CallTool(del, map[string]any{"id": bobID, "detach": true})
	if row = tc.ParseCypherRecords(res)[0]; row["deleted"] != float64(1) {
		t.Fatalf("expected 1 deletion, got %v", row)
	}
	res = tc.CallTool(count, map[string]any{"labels": []any{person}})
	if got := tc.ParseCypherRecords(res)[0]["count"]; got != float64(1) {
		t.Fatalf("expected 1 node left, count=%v", got)
	}

	// validation errors are actionable
	if msg := tc.GetToolError(del, map[string]any{"labels": []any{person}}); !strings.Contains(msg, "labels alone") {
		t.Fatalf("expected a labels-alone refusal, got: %s", msg)
	}
}
