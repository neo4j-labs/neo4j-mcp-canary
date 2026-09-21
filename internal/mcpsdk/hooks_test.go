// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestToolAccessMiddleware_NoRestriction verifies that when the ToolAccessFunc
// reports restrict=false, both "tools/list" and "tools/call" pass through
// unmodified.
func TestToolAccessMiddleware_NoRestriction(t *testing.T) {
	fn := func(context.Context) (map[string]bool, bool) { return nil, false }
	mw := toolAccessMiddleware(fn)

	t.Run("tools/list is unfiltered", func(t *testing.T) {
		next := func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			return &sdk.ListToolsResult{Tools: []*sdk.Tool{{Name: "a"}, {Name: "b"}}}, nil
		}
		res, err := mw(next)(context.Background(), methodListTools, &sdk.ListToolsRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ltr := res.(*sdk.ListToolsResult)
		if len(ltr.Tools) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(ltr.Tools))
		}
	})

	t.Run("tools/call is allowed", func(t *testing.T) {
		called := false
		next := func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			called = true
			return &sdk.CallToolResult{}, nil
		}
		_, err := mw(next)(context.Background(), methodCallTool, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "a"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called {
			t.Fatal("expected next to be called")
		}
	})
}

// TestToolAccessMiddleware_Restricted verifies that when restrict=true,
// "tools/list" is narrowed to the allowed set and a "tools/call" for a
// disallowed tool is rejected with the same "unknown tool" error shape the
// server returns for a genuinely unregistered tool, without calling next.
func TestToolAccessMiddleware_Restricted(t *testing.T) {
	fn := func(context.Context) (map[string]bool, bool) {
		return map[string]bool{"a": true}, true
	}
	mw := toolAccessMiddleware(fn)

	t.Run("tools/list is narrowed", func(t *testing.T) {
		next := func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			return &sdk.ListToolsResult{Tools: []*sdk.Tool{{Name: "a"}, {Name: "b"}}}, nil
		}
		res, err := mw(next)(context.Background(), methodListTools, &sdk.ListToolsRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ltr := res.(*sdk.ListToolsResult)
		if len(ltr.Tools) != 1 || ltr.Tools[0].Name != "a" {
			t.Fatalf("expected only tool %q, got %v", "a", ltr.Tools)
		}
	})

	t.Run("tools/call for an allowed tool reaches next", func(t *testing.T) {
		called := false
		next := func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			called = true
			return &sdk.CallToolResult{}, nil
		}
		_, err := mw(next)(context.Background(), methodCallTool, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "a"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called {
			t.Fatal("expected next to be called for an allowed tool")
		}
	})

	t.Run("tools/call for a disallowed tool is rejected without calling next", func(t *testing.T) {
		called := false
		next := func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			called = true
			return &sdk.CallToolResult{}, nil
		}
		_, err := mw(next)(context.Background(), methodCallTool, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "b"}})
		if err == nil {
			t.Fatal("expected an error for a disallowed tool")
		}
		if called {
			t.Fatal("next should not be called for a disallowed tool")
		}
	})
}
