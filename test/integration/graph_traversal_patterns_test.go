// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/patterns"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/traversal"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

type toolHandler = func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)

// seedDependencyGraph builds a -> b -> c -> d, a -> c, d -> b:
// a has out-degree 2; b and c have in-degree 2; b -> c -> d -> b is a
// 3-cycle; {a,b,c} and {b,c,d} are the two triangles.
func seedDependencyGraph(t *testing.T, tc *helpers.TestContext) (label string, ids map[string]string) {
	t.Helper()
	label = tc.GetUniqueLabel("Svc").String()
	query := fmt.Sprintf(`CREATE (a:%[1]s {name:'a'}), (b:%[1]s {name:'b'}), (c:%[1]s {name:'c'}), (d:%[1]s {name:'d'}),
		(a)-[:DEPENDS_ON]->(b), (b)-[:DEPENDS_ON]->(c), (c)-[:DEPENDS_ON]->(d), (a)-[:DEPENDS_ON]->(c), (d)-[:DEPENDS_ON]->(b)
		RETURN elementId(a) AS a, elementId(b) AS b, elementId(c) AS c, elementId(d) AS d`, label)
	records, err := tc.Service.ExecuteWriteQuery(context.Background(), query, map[string]any{})
	if err != nil {
		t.Fatalf("failed to seed graph: %v", err)
	}
	ids = map[string]string{}
	for i, k := range records[0].Keys {
		ids[k] = records[0].Values[i].(string)
	}
	return label, ids
}

func names(t *testing.T, rows []map[string]any, extract func(map[string]any) string) []string {
	t.Helper()
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = extract(r)
	}
	return out
}

func pathNames(path map[string]any) string {
	var parts []string
	for _, n := range path["nodes"].([]any) {
		parts = append(parts, n.(map[string]any)["properties"].(map[string]any)["name"].(string))
	}
	return strings.Join(parts, ">")
}

func TestGraphTools_TraversalAndPatterns(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())
	label, id := seedDependencyGraph(t, tc)
	start := func(n string) map[string]any { return map[string]any{"id": id[n]} }
	call := func(h toolHandler, args map[string]any) []map[string]any {
		t.Helper()
		return tc.ParseCypherRecords(tc.CallTool(h, args))
	}
	depends := []any{"DEPENDS_ON"}
	nameProp := []any{"name"}

	t.Run("traverse nodes returns each reachable node once at its shortest depth", func(t *testing.T) {
		rows := call(traversal.TraverseHandler(tc.Deps), map[string]any{"start": start("a"), "relationshipTypes": depends, "nodeProperties": nameProp})
		got := map[string]float64{}
		for _, r := range rows {
			got[r["node"].(map[string]any)["properties"].(map[string]any)["name"].(string)] = r["depth"].(float64)
		}
		want := map[string]float64{"b": 1, "c": 1, "d": 2}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("want %v got %v", want, got)
		}
	})

	t.Run("traverse paths and target filter", func(t *testing.T) {
		rows := call(traversal.TraverseHandler(tc.Deps), map[string]any{"start": start("a"), "relationshipTypes": depends, "returnMode": "paths", "maxDepth": 2, "nodeProperties": nameProp})
		if len(rows) != 4 {
			t.Fatalf("expected 4 paths within 2 hops, got %d: %v", len(rows), rows)
		}
		rows = call(traversal.TraverseHandler(tc.Deps), map[string]any{"start": start("a"), "relationshipTypes": depends, "target": map[string]any{"match": map[string]any{"name": "d"}}})
		if len(rows) != 1 {
			t.Fatalf("expected only d, got %v", rows)
		}
	})

	t.Run("expand-neighbors reports direction relative to the start", func(t *testing.T) {
		rows := call(traversal.ExpandNeighborsHandler(tc.Deps), map[string]any{"start": start("b"), "nodeProperties": nameProp})
		got := map[string]string{}
		for _, r := range rows {
			got[r["neighbor"].(map[string]any)["properties"].(map[string]any)["name"].(string)] = r["relationship"].(map[string]any)["direction"].(string)
		}
		if fmt.Sprint(got) != fmt.Sprint(map[string]string{"a": "INCOMING", "c": "OUTGOING", "d": "INCOMING"}) {
			t.Fatalf("unexpected neighbors: %v", got)
		}
	})

	t.Run("shortest and all paths", func(t *testing.T) {
		rows := call(traversal.FindShortestPathHandler(tc.Deps), map[string]any{"from": start("a"), "to": start("d"), "direction": "OUTGOING", "nodeProperties": nameProp})
		if len(rows) != 1 || pathNames(rows[0]["path"].(map[string]any)) != "a>c>d" {
			t.Fatalf("expected a>c>d, got %v", rows)
		}
		rows = call(traversal.FindAllPathsHandler(tc.Deps), map[string]any{"from": start("a"), "to": start("d"), "direction": "OUTGOING", "nodeProperties": nameProp})
		got := names(t, rows, func(r map[string]any) string { return pathNames(r["path"].(map[string]any)) })
		if len(got) != 2 || got[0] != "a>c>d" || got[1] != "a>b>c>d" {
			t.Fatalf("expected [a>c>d a>b>c>d], got %v", got)
		}
	})

	t.Run("match-pattern rows, count and exists", func(t *testing.T) {
		nodes := []any{
			map[string]any{"alias": "x", "labels": []any{label}, "match": map[string]any{"name": "a"}},
			map[string]any{"alias": "y", "labels": []any{label}},
			map[string]any{"alias": "z", "labels": []any{label}},
		}
		rels := []any{
			map[string]any{"from": "x", "to": "y", "types": depends},
			map[string]any{"from": "y", "to": "z", "types": depends},
		}
		rows := call(patterns.MatchPatternHandler(tc.Deps), map[string]any{"nodes": nodes, "relationships": rels, "returnProperties": nameProp})
		if len(rows) != 2 {
			t.Fatalf("expected a>b>c and a>c>d, got %v", rows)
		}
		if got := call(patterns.MatchPatternHandler(tc.Deps), map[string]any{"nodes": nodes, "relationships": rels, "mode": "count"})[0]["count"]; got != float64(2) {
			t.Fatalf("expected count 2, got %v", got)
		}
		if got := call(patterns.MatchPatternHandler(tc.Deps), map[string]any{"nodes": nodes, "relationships": rels, "mode": "exists"})[0]["exists"]; got != true {
			t.Fatalf("expected exists true, got %v", got)
		}
		msg := tc.GetToolError(patterns.MatchPatternHandler(tc.Deps), map[string]any{"nodes": nodes, "relationships": rels[:1]})
		if !strings.Contains(msg, "not connected") {
			t.Fatalf("expected a not-connected error, got: %s", msg)
		}
	})

	t.Run("find-common-connections", func(t *testing.T) {
		rows := call(patterns.FindCommonConnectionsHandler(tc.Deps), map[string]any{"nodes": []any{start("a"), start("b")}, "direction": "OUTGOING", "nodeProperties": nameProp})
		if len(rows) != 1 || rows[0]["connection"].(map[string]any)["properties"].(map[string]any)["name"] != "c" {
			t.Fatalf("expected c as the only shared target, got %v", rows)
		}
	})

	t.Run("find-motif", func(t *testing.T) {
		motif := patterns.FindMotifHandler(tc.Deps)
		hubName := func(r map[string]any) string {
			return r["hub"].(map[string]any)["properties"].(map[string]any)["name"].(string)
		}
		base := func(extra map[string]any) map[string]any {
			a := map[string]any{"relationshipTypes": depends, "nodeLabels": []any{label}, "nodeProperties": nameProp}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
		if got := names(t, call(motif, base(map[string]any{"motif": "fan-out", "minDegree": 2})), hubName); len(got) != 1 || got[0] != "a" {
			t.Fatalf("expected fan-out hub a, got %v", got)
		}
		if got := names(t, call(motif, base(map[string]any{"motif": "fan-in", "minDegree": 2})), hubName); len(got) != 2 {
			t.Fatalf("expected fan-in hubs b and c, got %v", got)
		}
		if got := call(motif, base(map[string]any{"motif": "triangle"})); len(got) != 2 {
			t.Fatalf("expected 2 triangles, got %v", got)
		}
		cycles := call(motif, base(map[string]any{"motif": "cycle", "cycleLength": 3}))
		if len(cycles) != 1 {
			t.Fatalf("expected exactly one 3-cycle, got %v", cycles)
		}
	})

	t.Run("find-relationship-chain", func(t *testing.T) {
		rows := call(patterns.FindRelationshipChainHandler(tc.Deps), map[string]any{
			"start": start("a"), "nodeProperties": nameProp,
			"steps": []any{map[string]any{"type": "DEPENDS_ON"}, map[string]any{"type": "DEPENDS_ON", "node": map[string]any{"match": map[string]any{"name": "d"}}}},
		})
		if len(rows) != 1 || pathNames(rows[0]["path"].(map[string]any)) != "a>c>d" {
			t.Fatalf("expected a>c>d, got %v", rows)
		}
	})

	t.Run("find-subgraph", func(t *testing.T) {
		rows := call(patterns.FindSubgraphHandler(tc.Deps), map[string]any{"start": start("a"), "maxDepth": 1, "relationshipTypes": depends})
		if len(rows) != 1 {
			t.Fatalf("expected one subgraph row, got %v", rows)
		}
		if len(rows[0]["nodes"].([]any)) != 3 || len(rows[0]["relationships"].([]any)) != 3 || rows[0]["truncated"] != false {
			t.Fatalf("expected 3 nodes (a,b,c) and 3 relationships, got %v", rows[0])
		}
		rows = call(patterns.FindSubgraphHandler(tc.Deps), map[string]any{"start": start("a"), "maxNodes": 2})
		if rows[0]["truncated"] != true || len(rows[0]["nodes"].([]any)) != 2 {
			t.Fatalf("expected truncation at maxNodes, got %v", rows[0])
		}
	})
}
