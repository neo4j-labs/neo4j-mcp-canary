// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// White-box tests for tools_register.go's internal metadata (Category,
// Label) and selection filter, kept separate from the black-box
// registration behaviour tests in tool_register_test.go.
package server

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

// serverToolNamed builds a minimal mcpsdk.ServerTool for filterBySelection
// tests, where only the tool's Name matters.
func serverToolNamed(name string) mcpsdk.ServerTool {
	return mcpsdk.ServerTool{Tool: mcpsdk.NewTool(name)}
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
