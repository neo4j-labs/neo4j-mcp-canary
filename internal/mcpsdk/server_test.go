// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"context"
	"testing"
)

func noopHandler(context.Context, *CallToolRequest) (*CallToolResult, error) {
	return NewToolResultText("ok"), nil
}

func toolNames(tools []Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Name] = true
	}
	return names
}

func TestServer_RemoveTools(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.AddTools(
		ServerTool{Tool: NewTool("a"), Handler: noopHandler},
		ServerTool{Tool: NewTool("b"), Handler: noopHandler},
		ServerTool{Tool: NewTool("c"), Handler: noopHandler},
	)

	s.RemoveTools("b")

	got := toolNames(s.ListTools())
	if len(got) != 2 || !got["a"] || !got["c"] || got["b"] {
		t.Fatalf("ListTools() = %v, want {a, c}", got)
	}
}

func TestServer_RemoveTools_UnknownNameIsNotAnError(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.AddTool(NewTool("a"), noopHandler)

	s.RemoveTools("does-not-exist")

	got := toolNames(s.ListTools())
	if len(got) != 1 || !got["a"] {
		t.Fatalf("ListTools() = %v, want {a} unchanged", got)
	}
}

func TestServer_RemoveTools_ThenReAddWorks(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.AddTool(NewTool("a"), noopHandler)
	s.RemoveTools("a")

	if got := toolNames(s.ListTools()); len(got) != 0 {
		t.Fatalf("ListTools() = %v, want empty after removal", got)
	}

	s.AddTool(NewTool("a"), noopHandler)
	if got := toolNames(s.ListTools()); len(got) != 1 || !got["a"] {
		t.Fatalf("ListTools() = %v, want {a} after re-adding", got)
	}
}
