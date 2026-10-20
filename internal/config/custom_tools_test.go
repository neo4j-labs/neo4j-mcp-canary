// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCustomTool() CustomTool {
	return CustomTool{
		Name:        "find-customer",
		Description: "Find a customer by email. Use when the user names a customer.",
		Category:    "crm",
		Access:      CustomToolAccessRead,
		Statement:   "MATCH (c:Customer {email: $email}) RETURN c",
		Params:      []CustomToolParam{{Name: "email", Type: CustomParamString, Description: "Customer email", Required: true}},
	}
}

func TestValidateCustomTools(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CustomTool)
		wantErr string
	}{
		{"valid", func(*CustomTool) {}, ""},
		{"bad name", func(c *CustomTool) { c.Name = "Find_Customer" }, "kebab-case"},
		{"missing description", func(c *CustomTool) { c.Description = " " }, "description is required"},
		{"missing category", func(c *CustomTool) { c.Category = "" }, "category is required"},
		{"bad access", func(c *CustomTool) { c.Access = "write" }, "access must be"},
		{"missing statement", func(c *CustomTool) { c.Statement = "" }, "statement is required"},
		{"bad param type", func(c *CustomTool) { c.Params[0].Type = "array" }, "type must be one of"},
		{"param without description", func(c *CustomTool) { c.Params[0].Description = "" }, "description is required"},
		{"required with default", func(c *CustomTool) { c.Params[0].Default = "x" }, ""},
		{"default wrong type", func(c *CustomTool) { c.Params[0].Required = false; c.Params[0].Default = 5.0 }, "default"},
		{"undeclared reference", func(c *CustomTool) { c.Statement += " LIMIT $limit" }, "$limit but no parameter"},
		{"unused parameter", func(c *CustomTool) { c.Statement = "MATCH (c:Customer) RETURN c" }, "never referenced"},
		{"duplicate parameter", func(c *CustomTool) { c.Params = append(c.Params, c.Params[0]) }, "duplicate parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := validCustomTool()
			tool.Params = append([]CustomToolParam(nil), tool.Params...)
			tt.mutate(&tool)
			err := ValidateCustomTools([]CustomTool{tool})
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("duplicate tool name", func(t *testing.T) {
		err := ValidateCustomTools([]CustomTool{validCustomTool(), validCustomTool()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate tool name")
	})
}

func TestCoerceCustomParam(t *testing.T) {
	v, err := CoerceCustomParam(CustomParamInteger, 3.0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), v)

	_, err = CoerceCustomParam(CustomParamInteger, 3.5)
	assert.Error(t, err)
	_, err = CoerceCustomParam(CustomParamString, 3.0)
	assert.Error(t, err)

	v, err = CoerceCustomParam(CustomParamNumber, 2)
	require.NoError(t, err)
	assert.Equal(t, 2.0, v)
}

func TestValidateCustomTools_ReportsEveryProblemOfATool(t *testing.T) {
	tool := validCustomTool()
	tool.Name = "BAD"
	tool.Access = "write"
	tool.Statement = "RETURN $x"
	err := ValidateCustomTools([]CustomTool{tool})
	require.Error(t, err)
	for _, want := range []string{"kebab-case", "access must be", "references $x", `parameter "email" is declared but never`} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestLoadCustomTools_FromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(`
neo4j_uri: bolt://localhost:7687
neo4j_username: neo4j
neo4j_password: secret
custom_tools:
  - name: find-customer
    description: Find a customer by email.
    category: crm
    access: read
    statement: |
      MATCH (c:Customer {email: $email}) RETURN c
    parameters:
      - name: email
        type: string
        description: Customer email
        required: true
`)), 0o600))

	cfg, err := LoadConfig(CLIOverrides{configFileOverrideKey: path})
	require.NoError(t, err)
	require.Len(t, cfg.CustomTools, 1)
	assert.Equal(t, "find-customer", cfg.CustomTools[0].Name)
	assert.Equal(t, CustomToolAccessRead, cfg.CustomTools[0].Access)
	assert.True(t, cfg.CustomTools[0].Params[0].Required)
}

func TestLoadCustomTools_RejectsInvalidAndUnknownKeys(t *testing.T) {
	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("neo4j_uri: bolt://localhost:7687\nneo4j_username: neo4j\nneo4j_password: secret\n"+body), 0o600))
		return path
	}

	_, err := LoadConfig(CLIOverrides{configFileOverrideKey: write("custom_tools:\n  - name: x\n    cypher: RETURN 1\n")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")

	_, err = LoadConfig(CLIOverrides{configFileOverrideKey: write("custom_tools:\n  - name: x\n")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom_tools[0]")
}

func TestLoadConfigAll_CollectsEveryProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(`
neo4j_transport_mode: http
neo4j_mcp_http_tls_enabled: true
neo4j_username: neo4j
custom_tools:
  - name: BAD
  - name: also-bad
`)), 0o600))

	cfg, errs := LoadConfigAll(CLIOverrides{configFileOverrideKey: path})
	require.NotNil(t, cfg)
	var all []string
	for _, e := range errs {
		all = append(all, e.Error())
	}
	joined := strings.Join(all, "\n")
	assert.Contains(t, joined, "custom_tools[0] (BAD)")
	assert.Contains(t, joined, "custom_tools[1] (also-bad)")
	assert.Contains(t, joined, "Neo4j URI is required")
	assert.Contains(t, joined, "should not be set for HTTP transport mode")
	assert.Contains(t, joined, "TLS certificate file is required")

	_, err := LoadConfig(CLIOverrides{configFileOverrideKey: path})
	require.Error(t, err)
	assert.Equal(t, errs[0].Error(), err.Error(), "LoadConfig reports the first of LoadConfigAll's errors")
}

func TestUnknownFileKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("neo4j_uri: x\nNEO4J_READ_ONLY: true\nneo4j_read_onyl: true\ncustom_tools: []\nneo4j_instances: []\n"), 0o600))
	keys, err := UnknownFileKeys(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"neo4j_read_onyl"}, keys)
}
