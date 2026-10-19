// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// White-box tests for tools_register.go's internal metadata (Category,
// Label) and selection filter, kept separate from the black-box
// registration behaviour tests in tool_register_test.go.
package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/readiness"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

// serverToolNamed builds a minimal mcpsdk.ServerTool for filterBySelection
// tests, where only the tool's Name matters.
func serverToolNamed(name string) mcpsdk.ServerTool {
	return mcpsdk.ServerTool{Tool: mcpsdk.NewTool(name)}
}

// TestBuildToolDependencies_HalvesCypherMaxBytes locks in the byte-budget
// halving: read-cypher/write-cypher now always attach the canonical JSON as
// structuredContent alongside the text block, so the same payload goes out
// twice — the effective per-call cap enforced during streaming must be half
// of the configured NEO4J_CYPHER_MAX_BYTES, or a response that used to just
// fit under the ~1MB MCP transport ceiling would now double it.
func TestBuildToolDependencies_HalvesCypherMaxBytes(t *testing.T) {
	s := &Neo4jMCPServer{config: &config.Config{CypherMaxBytes: 900_000}}

	deps := s.buildToolDependencies()

	if deps.CypherMaxBytes != 450_000 {
		t.Errorf("CypherMaxBytes = %d, want 450000 (half of configured 900000)", deps.CypherMaxBytes)
	}
}

// TestGetAllToolsDefs_EveryToolHasCategoryAndLabel enforces the "every tool
// must belong to a category and carry a label" requirement as a test rather
// than a convention, since getAllToolsDefs is a hand-maintained literal
// slice.
func TestGetAllToolsDefs_EveryToolHasCategoryAndLabel(t *testing.T) {
	s := &Neo4jMCPServer{config: &config.Config{}}
	deps := s.buildToolDependencies()

	for _, d := range s.getAllToolsDefs(deps) {
		name := d.definition.Tool.Name
		if d.Category == "" {
			t.Errorf("tool %q has no Category", name)
		}
		if d.Label() == "" {
			t.Errorf("tool %q has no Label", name)
		}
	}
}

// TestGetAllToolsDefs_OutputSchemaIsTopLevelObject enforces MCP's requirement
// that structuredContent (and the outputSchema describing it) be a JSON
// object at the top level — a top-level "array" or a bare "$ref" (present
// but with no explicit "type") fails Claude Desktop's tools/list validation
// outright and prevents every tool from registering, not just the
// offending one. See get-schema/list-gds-procedures (used to declare a
// top-level array) and explain-cypher (used to declare a top-level bare
// $ref) for the bug this guards against.
func TestGetAllToolsDefs_OutputSchemaIsTopLevelObject(t *testing.T) {
	s := &Neo4jMCPServer{config: &config.Config{}}
	deps := s.buildToolDependencies()

	for _, d := range s.getAllToolsDefs(deps) {
		raw := d.definition.Tool.OutputSchema
		if raw == nil {
			continue
		}
		schema, ok := raw.(*jsonschema.Schema)
		if !ok {
			t.Errorf("tool %q has an OutputSchema of unexpected type %T", d.definition.Tool.Name, raw)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("tool %q OutputSchema.Type = %q, want \"object\"", d.definition.Tool.Name, schema.Type)
		}
	}
}

func TestFilterBySelection(t *testing.T) {
	defs := []ToolDefinition{
		{Category: tools.CategoryCypher, definition: serverToolNamed("read-cypher")},
		{Category: tools.CategoryCypher, definition: serverToolNamed("write-cypher")},
		{Category: tools.CategoryGDS, definition: serverToolNamed("list-gds-procedures")},
		{Category: tools.CategoryFeedback, definition: serverToolNamed("give-feedback")},
	}

	tests := []struct {
		name       string
		names      []string
		categories []string
		want       []string
	}{
		{
			name: "no selection is a no-op",
			want: []string{"read-cypher", "write-cypher", "list-gds-procedures", "give-feedback"},
		},
		{
			name:  "select by name",
			names: []string{"read-cypher"},
			want:  []string{"read-cypher"},
		},
		{
			name:       "select by category",
			categories: []string{"cypher"},
			want:       []string{"read-cypher", "write-cypher"},
		},
		{
			name:       "name and category selection is a union",
			names:      []string{"give-feedback"},
			categories: []string{"gds"},
			want:       []string{"list-gds-procedures", "give-feedback"},
		},
		{
			name:  "unknown name matches nothing",
			names: []string{"does-not-exist"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterBySelection(tt.names, tt.categories)(defs)
			gotNames := make([]string, 0, len(got))
			for _, d := range got {
				gotNames = append(gotNames, d.definition.Tool.Name)
			}
			if len(gotNames) != len(tt.want) {
				t.Fatalf("got %v, want %v", gotNames, tt.want)
			}
			for _, w := range tt.want {
				found := false
				for _, g := range gotNames {
					if g == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in result %v", w, gotNames)
				}
			}
		})
	}
}

func TestCustomTools_RegisteredWithCategoryAndAccess(t *testing.T) {
	s := &Neo4jMCPServer{config: &config.Config{CustomTools: []config.CustomTool{
		{Name: "find-customer", Description: "d", Category: "crm", Access: config.CustomToolAccessRead, Statement: "RETURN 1"},
		{Name: "merge-customer", Description: "d", Category: "crm", Access: config.CustomToolAccessReadWrite, Statement: "RETURN 1"},
	}}}
	require.NoError(t, s.validateCustomTools())

	defs := s.getAllToolsDefs(s.buildToolDependencies())
	byName := map[string]ToolDefinition{}
	for _, d := range defs {
		byName[d.definition.Tool.Name] = d
	}
	assert.Equal(t, tools.Category("crm"), byName["find-customer"].Category)
	assert.True(t, byName["find-customer"].readonly)
	assert.False(t, byName["merge-customer"].readonly)

	readOnly := filterWriteTools(defs)
	names := map[string]bool{}
	for _, d := range readOnly {
		names[d.definition.Tool.Name] = true
	}
	assert.True(t, names["find-customer"])
	assert.False(t, names["merge-customer"], "NEO4J_READ_ONLY must remove readwrite custom tools")

	selected := filterBySelection(nil, []string{"crm"})(defs)
	assert.Len(t, selected, 2)
}

func TestValidateCustomTools_RejectsBuiltInCollisions(t *testing.T) {
	base := config.CustomTool{Name: "my-tool", Description: "d", Category: "crm", Access: config.CustomToolAccessRead, Statement: "RETURN 1"}

	clashName := base
	clashName.Name = "read-cypher"
	err := (&Neo4jMCPServer{config: &config.Config{CustomTools: []config.CustomTool{clashName}}}).validateCustomTools()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in tool")

	clashCategory := base
	clashCategory.Category = "gds"
	err = (&Neo4jMCPServer{config: &config.Config{CustomTools: []config.CustomTool{clashCategory}}}).validateCustomTools()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in category")
}

func TestCheckToolConfig(t *testing.T) {
	messages := func(issues []ToolConfigIssue) string {
		var out string
		for _, i := range issues {
			out += i.Message + "\n"
		}
		return out
	}

	t.Run("clean config has no issues", func(t *testing.T) {
		assert.Empty(t, CheckToolConfig(&config.Config{}, nil))
	})

	t.Run("unknown names and categories warn", func(t *testing.T) {
		issues := CheckToolConfig(&config.Config{EnabledTools: "read-cypher,nope", EnabledToolCategories: "nonsense"}, nil)
		out := messages(issues)
		assert.Contains(t, out, `unknown tool "nope"`)
		assert.Contains(t, out, `unknown category "nonsense"`)
		for _, i := range issues {
			assert.False(t, i.Error)
		}
	})

	t.Run("selected write tool removed by read-only", func(t *testing.T) {
		out := messages(CheckToolConfig(&config.Config{ReadOnly: true, EnabledTools: "read-cypher,write-cypher"}, nil))
		assert.Contains(t, out, `"write-cypher" modifies data and is removed because NEO4J_READ_ONLY`)
	})

	t.Run("readwrite custom tool under read-only warns", func(t *testing.T) {
		cfg := &config.Config{ReadOnly: true, CustomTools: []config.CustomTool{
			{Name: "merge-customer", Description: "d", Category: "crm", Access: config.CustomToolAccessReadWrite, Statement: "RETURN 1"},
		}}
		assert.Contains(t, messages(CheckToolConfig(cfg, nil)), `"merge-customer" modifies data`)
	})

	t.Run("selection leaving no tools warns", func(t *testing.T) {
		out := messages(CheckToolConfig(&config.Config{ReadOnly: true, EnabledTools: "write-cypher"}, nil))
		assert.Contains(t, out, "leave no tools enabled")
	})

	t.Run("capabilities", func(t *testing.T) {
		cfg := &config.Config{EnabledToolCategories: "gds,search,graph"}
		out := messages(CheckToolConfig(cfg, &readiness.Result{}))
		assert.Contains(t, out, "Graph Data Science plugin is not installed")
		assert.Contains(t, out, "too old for the search category")
		assert.Contains(t, out, "does not support Cypher 25")

		assert.Empty(t, CheckToolConfig(cfg, &readiness.Result{GDSInstalled: true, SearchVersionSupported: true, Cypher25Supported: true}))
	})
}
