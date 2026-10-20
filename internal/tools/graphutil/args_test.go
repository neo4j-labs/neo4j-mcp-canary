// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"reflect"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

func TestCheckArguments(t *testing.T) {
	type input struct {
		NodeRef
		Limit int `json:"limit,omitempty"`
	}
	req := func(args map[string]any) *mcpsdk.CallToolRequest {
		return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Arguments: args}}
	}
	typ := reflect.TypeOf(input{})

	if err := checkArguments("t", req(map[string]any{"labels": []any{"A"}, "limit": 1}), typ); err != nil {
		t.Errorf("declared arguments rejected (including embedded NodeRef fields): %v", err)
	}
	if err := checkArguments("t", req(nil), typ); err != nil {
		t.Errorf("no arguments rejected: %v", err)
	}
	err := checkArguments("t", req(map[string]any{"limt": 1}), typ)
	if err == nil || !strings.Contains(err.Error(), `unknown argument "limt"`) || !strings.Contains(err.Error(), "limit") {
		t.Errorf("expected an unknown-argument error listing valid names, got %v", err)
	}
}
