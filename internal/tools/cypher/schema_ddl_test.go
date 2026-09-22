// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"strings"
	"testing"
)

func TestEntityPattern(t *testing.T) {
	tests := []struct {
		name             string
		entityType       string
		label            string
		relationshipType string
		wantPattern      string
		wantVarName      string
		wantErr          bool
	}{
		{name: "node", entityType: "NODE", label: "Person", wantPattern: "(n:`Person`)", wantVarName: "n"},
		{name: "relationship", entityType: "RELATIONSHIP", relationshipType: "REVIEWED", wantPattern: "()-[r:`REVIEWED`]-()", wantVarName: "r"},
		{name: "node missing label", entityType: "NODE", wantErr: true},
		{name: "node with relationshipType set", entityType: "NODE", label: "Person", relationshipType: "REVIEWED", wantErr: true},
		{name: "relationship missing type", entityType: "RELATIONSHIP", wantErr: true},
		{name: "relationship with label set", entityType: "RELATIONSHIP", relationshipType: "REVIEWED", label: "Person", wantErr: true},
		{name: "unknown entity type", entityType: "EDGE", label: "Person", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, varName, err := entityPattern(tt.entityType, tt.label, tt.relationshipType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got pattern=%q varName=%q", pattern, varName)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pattern != tt.wantPattern {
				t.Errorf("pattern = %q, want %q", pattern, tt.wantPattern)
			}
			if varName != tt.wantVarName {
				t.Errorf("varName = %q, want %q", varName, tt.wantVarName)
			}
		})
	}
}

func TestParenList(t *testing.T) {
	tests := []struct {
		name       string
		varName    string
		properties []string
		want       string
	}{
		{name: "single property", varName: "n", properties: []string{"email"}, want: "(n.`email`)"},
		{name: "composite properties", varName: "n", properties: []string{"tenantId", "orderId"}, want: "(n.`tenantId`, n.`orderId`)"},
		{name: "relationship var", varName: "r", properties: []string{"rating"}, want: "(r.`rating`)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parenList(tt.varName, tt.properties); got != tt.want {
				t.Errorf("parenList() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: "Person", want: "`Person`"},
		{name: "with space", input: "My Label", want: "`My Label`"},
		{name: "with backtick", input: "weird`name", want: "`weird``name`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quoteIdentifier(tt.input); got != tt.want {
				t.Errorf("quoteIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGeneratedSchemaName(t *testing.T) {
	a := generatedSchemaName()
	b := generatedSchemaName()
	if a == b {
		t.Fatalf("expected two calls to produce distinct names, got %q twice", a)
	}
	if !strings.HasPrefix(a, "mcp_") {
		t.Errorf("generatedSchemaName() = %q, want mcp_ prefix", a)
	}
	if strings.Contains(a, "-") {
		t.Errorf("generatedSchemaName() = %q, want no hyphens (must be a valid unquoted Cypher identifier)", a)
	}
}
