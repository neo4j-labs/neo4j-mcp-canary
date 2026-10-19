// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package configcheck_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/configcheck"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	dbmocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
)

const header = "neo4j_uri: bolt://localhost:7687\nneo4j_username: neo4j\nneo4j_password: secret\n"

func writeConfig(t *testing.T, body string) config.CLIOverrides {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(header+body), 0o600))
	return config.CLIOverrides{"ConfigFile": path}
}

func noConnect(t *testing.T) configcheck.Connector {
	return func(context.Context, *config.Config) (database.Service, func(), error) {
		t.Helper()
		t.Error("connect must not be called")
		return nil, func() {}, errors.New("unexpected")
	}
}

func run(t *testing.T, overrides config.CLIOverrides, connect configcheck.Connector) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := configcheck.Run(context.Background(), &out, overrides, connect)
	return code, out.String()
}

func TestRun_ReportsEveryOfflineProblem(t *testing.T) {
	overrides := writeConfig(t, `
neo4j_read_onyl: true
neo4j_mcp_enabled_tools: nope-tool
custom_tools:
  - name: Bad_Name
    description: d
    category: crm
    access: read
    statement: |
      RETURN 1
  - name: other-tool
    description: d
    category: gds
    access: read
    statement: |
      RETURN 1
`)
	overrides["NEO4J_MCP_ENABLED_TOOL_CATEGORIES"] = "x"
	code, out := run(t, overrides, noConnect(t))

	assert.Equal(t, 1, code)
	assert.Contains(t, out, `WARN  config file`)
	assert.Contains(t, out, `unknown key "neo4j_read_onyl"`)
	assert.Contains(t, out, "ERROR custom_tools[0] (Bad_Name): name must be lower-case kebab-case")
	assert.Contains(t, out, `category "gds", which is a built-in category`)
	assert.Contains(t, out, `unknown tool "nope-tool"`)
	assert.Contains(t, out, "database checks skipped: fix the errors above first")
	assert.Contains(t, out, "Configuration is invalid:")
}

func TestRun_ReportsSeveralConfigErrorsAtOnce(t *testing.T) {
	code, out := run(t, config.CLIOverrides{"TransportMode": "stdio"}, noConnect(t))
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "Neo4j URI is required")
	assert.Contains(t, out, "Neo4j username is required")
	assert.Contains(t, out, "Neo4j password is required")
}

func TestRun_HTTPModeSkipsDatabaseChecks(t *testing.T) {
	overrides := config.CLIOverrides{"URI": "bolt://localhost:7687", "TransportMode": "http"}
	code, out := run(t, overrides, noConnect(t))
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "database checks skipped: in HTTP mode")
}

func TestRun_ConnectionFailureIsAnError(t *testing.T) {
	connect := func(context.Context, *config.Config) (database.Service, func(), error) {
		return nil, func() {}, errors.New("refused")
	}
	code, out := run(t, writeConfig(t, ""), connect)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "cannot connect to Neo4j at bolt://localhost:7687: refused")
}

const customTools = `
custom_tools:
  - name: good-tool
    description: d
    category: crm
    access: read
    statement: |
      MATCH (n) WHERE n.id = $id RETURN n
    parameters: [{name: id, type: integer, description: id, required: true}]
  - name: writes-as-read
    description: d
    category: crm
    access: read
    statement: |
      CREATE (n:X {id: $id}) RETURN n
    parameters: [{name: id, type: integer, description: id, required: true}]
  - name: bad-syntax
    description: d
    category: crm
    access: readwrite
    statement: |
      MATCH (n RETURN n
  - name: read-as-write
    description: d
    category: crm
    access: readwrite
    statement: |
      MATCH (n) RETURN n
`

func TestRun_CustomToolStatementsCheckedAgainstDatabase(t *testing.T) {
	ctrl := gomock.NewController(t)
	db := dbmocks.NewMockService(ctrl)
	// readiness.Checker queries (version, GDS); the exact calls are not the point here.
	db.EXPECT().VerifyConnectivity(gomock.Any()).AnyTimes().Return(nil)
	db.EXPECT().ExecuteReadQuery(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return([]*neo4j.Record{}, nil)
	db.EXPECT().GetQueryType(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q string, params map[string]any) (neo4j.QueryType, error) {
			switch {
			case strings.HasPrefix(q, "MATCH (n) WHERE"):
				assert.Equal(t, map[string]any{"id": int64(1)}, params, "sample values must match the declared type")
			case strings.HasPrefix(q, "CREATE"):
				return neo4j.QueryTypeReadWrite, nil
			case strings.HasPrefix(q, "MATCH (n RETURN"):
				return 0, errors.New("Invalid input 'RETURN'")
			}
			return neo4j.QueryTypeReadOnly, nil
		}).Times(4)

	connect := func(context.Context, *config.Config) (database.Service, func(), error) { return db, func() {}, nil }
	code, out := run(t, writeConfig(t, customTools), connect)

	assert.Equal(t, 1, code)
	assert.Contains(t, out, `ERROR custom tool "writes-as-read" is declared access: read but its statement is not read-only`)
	assert.Contains(t, out, `ERROR custom tool "bad-syntax": Neo4j rejected the statement: Invalid input 'RETURN'`)
	assert.Contains(t, out, `WARN  custom tool "read-as-write" is declared access: readwrite but its statement only reads`)
	assert.NotContains(t, out, "good-tool")
}
