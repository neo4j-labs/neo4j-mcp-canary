// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/configcheck"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

func TestValidateConfig_CustomToolsAgainstRealNeo4j(t *testing.T) {
	t.Parallel()
	tc := helpers.NewTestContext(t, dbs.GetDriver())

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `neo4j_uri: bolt://localhost:7687
neo4j_username: neo4j
neo4j_password: secret
neo4j_mcp_enabled_tool_categories: crm,search
custom_tools:
  - name: good
    description: d
    category: crm
    access: read
    statement: |
      MATCH (c:Customer {email: $email}) RETURN c.name AS name
    parameters: [{name: email, type: string, description: email, required: true}]
  - name: writes-as-read
    description: d
    category: crm
    access: read
    statement: |
      MERGE (c:Customer {email: $email}) RETURN c
    parameters: [{name: email, type: string, description: email, required: true}]
  - name: bad-syntax
    description: d
    category: crm
    access: read
    statement: |
      MATCH (c:Customer RETURN c
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	connect := func(context.Context, *config.Config) (database.Service, func(), error) {
		return tc.Deps.DBService, func() {}, nil
	}
	var out bytes.Buffer
	code := configcheck.Run(context.Background(), &out, config.CLIOverrides{"ConfigFile": path}, connect)
	report := out.String()

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d:\n%s", code, report)
	}
	for _, want := range []string{
		`custom tool "writes-as-read" is declared access: read but its statement is not read-only`,
		`custom tool "bad-syntax": Neo4j rejected the statement`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, `"good"`) {
		t.Errorf("valid tool must not be reported:\n%s", report)
	}
}
