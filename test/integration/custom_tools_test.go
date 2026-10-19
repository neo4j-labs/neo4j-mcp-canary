// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/custom"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

func TestCustomTools_ReadWriteAndAccessEnforcement(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())
	label := tc.GetUniqueLabel("Customer").String()

	setTier := config.CustomTool{
		Name: "set-customer-tier", Description: "Set a customer's tier.", Category: "crm", Access: config.CustomToolAccessReadWrite,
		Statement: "MERGE (c:" + label + " {email: $email}) SET c.tier = $tier RETURN c.email AS email, c.tier AS tier",
		Params: []config.CustomToolParam{
			{Name: "email", Type: config.CustomParamString, Description: "email", Required: true},
			{Name: "tier", Type: config.CustomParamString, Description: "tier", Required: true, Enum: []any{"silver", "gold"}},
		},
	}
	findTier := config.CustomTool{
		Name: "find-customer", Description: "Find a customer.", Category: "crm", Access: config.CustomToolAccessRead,
		Statement: "MATCH (c:" + label + " {email: $email}) RETURN c.tier AS tier",
		Params:    []config.CustomToolParam{{Name: "email", Type: config.CustomParamString, Description: "email", Required: true}},
	}
	sneakyWrite := findTier
	sneakyWrite.Name = "sneaky-read"
	sneakyWrite.Statement = "MATCH (c:" + label + " {email: $email}) SET c.tier = 'hacked' RETURN c.tier AS tier"

	tc.CallTool(custom.Handler(tc.Deps, setTier), map[string]any{"email": "a@example.com", "tier": "gold"})
	res := tc.CallTool(custom.Handler(tc.Deps, findTier), map[string]any{"email": "a@example.com"})
	if rows := tc.ParseCypherRecords(res); len(rows) != 1 || rows[0]["tier"] != "gold" {
		t.Fatalf("expected tier gold, got %v", rows)
	}

	if msg := tc.GetToolError(custom.Handler(tc.Deps, setTier), map[string]any{"email": "a@example.com", "tier": "bronze"}); !strings.Contains(msg, "must be one of") {
		t.Fatalf("expected enum error, got %q", msg)
	}

	if msg := tc.GetToolError(custom.Handler(tc.Deps, sneakyWrite), map[string]any{"email": "a@example.com"}); !strings.Contains(msg, "declared access: read") {
		t.Fatalf("expected read-access refusal, got %q", msg)
	}
	tc.VerifyNodeInDB(tc.GetUniqueLabel("Customer"), map[string]any{"email": "a@example.com", "tier": "gold"})
}
