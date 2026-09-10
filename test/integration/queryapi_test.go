// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

// requireQueryAPI skips the calling test when no Query API base URL is
// available — external-DB mode (USE_CONTAINER=false) has no way to know
// whether the configured server even supports the Query API, so tests skip
// rather than guess.
func requireQueryAPI(t *testing.T) string {
	t.Helper()
	baseURL := dbs.GetQueryAPIBaseURL()
	if baseURL == "" {
		t.Skip("Query API base URL not available (USE_CONTAINER=false?)")
	}
	return baseURL
}

// TestQueryAPIVersionGate proves the shared container's image actually
// clears queryapi.CheckMinimumVersion's floor — this is the direct
// regression check for the version-gate bug fix, run against a real server
// rather than a mocked discovery response.
func TestQueryAPIVersionGate(t *testing.T) {
	t.Parallel()
	baseURL := requireQueryAPI(t)

	if err := queryapi.EnsureMinimumVersion(context.Background(), http.DefaultClient, baseURL); err != nil {
		t.Fatalf("expected the shared container's Neo4j version to clear the Query API floor, got: %v", err)
	}
}

// TestQueryAPIReadCypher mirrors TestReadCypher's happy path, but talks to
// Neo4j over the HTTP-based Query API instead of Bolt.
func TestQueryAPIReadCypher(t *testing.T) {
	t.Parallel()
	baseURL := requireQueryAPI(t)
	conf := dbs.GetDriverConf()
	tc := helpers.NewQueryAPITestContext(t, baseURL, conf.Username, conf.Password, "neo4j")

	personLabel, err := tc.SeedNode("Person", map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}

	read := cypher.ReadCypherHandler(tc.Deps)
	res := tc.CallTool(read, map[string]any{
		"query":  "MATCH (p:" + personLabel + " {name: $name}) RETURN p",
		"params": map[string]any{"name": "Alice"},
	})

	records := tc.ParseCypherRecords(res)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	pNode, ok := records[0]["p"].(map[string]any)
	if !ok {
		t.Fatalf("expected p to be map[string]any, got %T", records[0]["p"])
	}
	tc.AssertNodeProperties(pNode, map[string]any{"name": "Alice"})
	tc.AssertNodeHasLabel(pNode, personLabel)
}

// TestQueryAPIWriteCypher mirrors TestWriteCypher over the Query API, and
// TestQueryAPIReadCypherRejectsWrites confirms read-cypher's write-rejection
// (backed by queryapi.Service.GetQueryType's EXPLAIN classification, wired
// through the same wrapQueryAPIError path as the Bolt driver) also works
// against a real server, not just the httptest.Server fakes in
// internal/queryapi's own unit tests.
func TestQueryAPIWriteCypher(t *testing.T) {
	t.Parallel()
	baseURL := requireQueryAPI(t)
	conf := dbs.GetDriverConf()
	tc := helpers.NewQueryAPITestContext(t, baseURL, conf.Username, conf.Password, "neo4j")

	personLabel := tc.GetUniqueLabel("Person")

	write := cypher.WriteCypherHandler(tc.Deps)
	tc.CallTool(write, map[string]any{
		"query":  "CREATE (p:" + personLabel + " {name: $name}) RETURN p",
		"params": map[string]any{"name": "Alice"},
	})

	tc.VerifyNodeInDB(personLabel, map[string]any{"name": "Alice"})
}

func TestQueryAPIReadCypherRejectsWrites(t *testing.T) {
	t.Parallel()
	baseURL := requireQueryAPI(t)
	conf := dbs.GetDriverConf()
	tc := helpers.NewQueryAPITestContext(t, baseURL, conf.Username, conf.Password, "neo4j")

	personLabel := tc.GetUniqueLabel("Person")

	read := cypher.ReadCypherHandler(tc.Deps)
	textError := tc.GetToolError(read, map[string]any{
		"query":  "CREATE (p:" + personLabel + ") SET p.name = $name RETURN p",
		"params": map[string]any{"name": "Alice"},
	})

	if !strings.Contains(textError, "read-cypher can only run read-only Cypher statements.") {
		t.Fatalf("expected read-cypher to reject the CREATE query, got: %s", textError)
	}
}
