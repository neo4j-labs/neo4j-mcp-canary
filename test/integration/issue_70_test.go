// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"

	"github.com/mark3labs/mcp-go/mcp"
)

// https://github.com/neo4j-labs/neo4j-mcp-canary/issues/70
func TestIssue70(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler func(deps *tools.ToolDependencies) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{
			name:    "read-cypher",
			handler: cypher.ReadCypherHandler,
		},
		{
			name:    "write-cypher",
			handler: cypher.WriteCypherHandler,
		},
	}

	for _, tt := range tests {

		t.Run(strings.Join([]string{tt.name, "should accept float parameter"}, " "), func(t *testing.T) {

			tc := helpers.NewTestContext(t, dbs.GetDriver())

			companyLabel := tc.GetUniqueLabel("Company")

			_, err := tc.SeedNode("Company", map[string]any{"prop": 1.2})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}
			_, err = tc.SeedNode("Company", map[string]any{"prop": 3.2})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}
			_, err = tc.SeedNode("Company", map[string]any{"prop": 4.2})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}

			handler := tt.handler(tc.Deps)
			handlerQuery := strings.Join(
				[]string{
					"MATCH (n:", companyLabel.String(), ")\n",
					"WHERE n.prop < $param1\n",
					"RETURN n\n",
				}, "")
			res := tc.CallTool(handler, map[string]any{
				"query": handlerQuery,
				"params": map[string]any{
					"param1": 3.5,
				},
			})

			records := tc.ParseCypherRecords(res)

			if len(records) != 2 {
				t.Fatalf("expected 2 company, got %d", len(records))
			}
		})
		t.Run(strings.Join([]string{tt.name, "should accept integer parameter"}, " "), func(t *testing.T) {
			tc := helpers.NewTestContext(t, dbs.GetDriver())

			companyLabel := tc.GetUniqueLabel("Company")

			_, err := tc.SeedNode("Company", map[string]any{})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}
			_, err = tc.SeedNode("Company", map[string]any{})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}
			_, err = tc.SeedNode("Company", map[string]any{})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}
			_, err = tc.SeedNode("Company", map[string]any{})
			if err != nil {
				t.Fatalf("failed to seed Company node: %v", err)
			}

			handler := tt.handler(tc.Deps)
			handlerQuery := strings.Join(
				[]string{
					"MATCH (n:", companyLabel.String(), ") RETURN n LIMIT $param1",
				}, "")
			res := tc.CallTool(handler, map[string]any{
				"query": handlerQuery,
				"params": map[string]int{
					"param1": 1,
				},
			})

			records := tc.ParseCypherRecords(res)

			if len(records) != 1 {
				t.Fatalf("expected 1 company, got %d", len(records))
			}

			company := records[0]["n"].(map[string]any)
			tc.AssertNodeHasLabel(company, companyLabel)
		})
	}
}
