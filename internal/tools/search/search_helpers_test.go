// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"strings"
	"testing"
)

func TestBuildFilterClauses(t *testing.T) {
	t.Run("empty filters produce no clause", func(t *testing.T) {
		clause, params, err := buildFilterClauses("n", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if clause != "" || params != nil {
			t.Errorf("expected empty clause/params, got clause=%q params=%v", clause, params)
		}
	})

	t.Run("single filter", func(t *testing.T) {
		clause, params, err := buildFilterClauses("n", []Filter{{Property: "tenantId", Operator: "=", Value: "acme"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if clause != "n.`tenantId` = $f0" {
			t.Errorf("clause = %q, want %q", clause, "n.`tenantId` = $f0")
		}
		if params["f0"] != "acme" {
			t.Errorf("params[f0] = %v, want %q", params["f0"], "acme")
		}
	})

	t.Run("multiple filters AND-joined", func(t *testing.T) {
		clause, params, err := buildFilterClauses("n", []Filter{
			{Property: "tenantId", Operator: "=", Value: "acme"},
			{Property: "age", Operator: ">", Value: 21},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "n.`tenantId` = $f0 AND n.`age` > $f1"
		if clause != want {
			t.Errorf("clause = %q, want %q", clause, want)
		}
		if params["f0"] != "acme" || params["f1"] != 21 {
			t.Errorf("unexpected params: %v", params)
		}
	})

	t.Run("rejects an unsupported operator", func(t *testing.T) {
		_, _, err := buildFilterClauses("n", []Filter{{Property: "tenantId", Operator: "!=", Value: "acme"}})
		if err == nil {
			t.Fatal("expected an error for unsupported operator")
		}
		if !strings.Contains(err.Error(), "!=") {
			t.Errorf("expected error to mention the bad operator, got: %v", err)
		}
	})

	t.Run("rejects an empty property", func(t *testing.T) {
		_, _, err := buildFilterClauses("n", []Filter{{Property: "", Operator: "=", Value: "acme"}})
		if err == nil {
			t.Fatal("expected an error for empty property")
		}
	})

	t.Run("accepts IN operator", func(t *testing.T) {
		clause, _, err := buildFilterClauses("n", []Filter{{Property: "status", Operator: "IN", Value: []any{"a", "b"}}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if clause != "n.`status` IN $f0" {
			t.Errorf("clause = %q, want %q", clause, "n.`status` IN $f0")
		}
	})
}

func TestResolveSingleLabelOrType(t *testing.T) {
	tests := []struct {
		name             string
		entityType       string
		label            string
		relationshipType string
		want             string
		wantErr          bool
	}{
		{name: "node with label", entityType: "NODE", label: "Document", want: "Document"},
		{name: "relationship with type", entityType: "RELATIONSHIP", relationshipType: "REVIEWED", want: "REVIEWED"},
		{name: "node missing label", entityType: "NODE", wantErr: true},
		{name: "node with both set", entityType: "NODE", label: "Document", relationshipType: "REVIEWED", wantErr: true},
		{name: "relationship missing type", entityType: "RELATIONSHIP", wantErr: true},
		{name: "relationship with both set", entityType: "RELATIONSHIP", label: "Document", relationshipType: "REVIEWED", wantErr: true},
		// The mismatch case that motivated this helper: the field that's set
		// doesn't match the declared entityType, with the other left empty —
		// must be rejected, not silently resolved to an empty string.
		{name: "node with only relationshipType set", entityType: "NODE", relationshipType: "REVIEWED", wantErr: true},
		{name: "relationship with only label set", entityType: "RELATIONSHIP", label: "Document", wantErr: true},
		{name: "unknown entity type", entityType: "EDGE", label: "Document", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSingleLabelOrType(tt.entityType, tt.label, tt.relationshipType)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEntityPatternMulti(t *testing.T) {
	tests := []struct {
		name        string
		entityType  string
		labels      []string
		wantPattern string
		wantVarName string
		wantErr     bool
	}{
		{name: "single node label", entityType: "NODE", labels: []string{"Document"}, wantPattern: "(n:`Document`)", wantVarName: "n"},
		{name: "multi node label", entityType: "NODE", labels: []string{"Document", "Article"}, wantPattern: "(n:`Document`|`Article`)", wantVarName: "n"},
		{name: "single relationship type", entityType: "RELATIONSHIP", labels: []string{"REVIEWED"}, wantPattern: "()-[r:`REVIEWED`]-()", wantVarName: "r"},
		{name: "multi relationship type", entityType: "RELATIONSHIP", labels: []string{"REVIEWED", "COMMENTED"}, wantPattern: "()-[r:`REVIEWED`|`COMMENTED`]-()", wantVarName: "r"},
		{name: "empty labels", entityType: "NODE", labels: nil, wantErr: true},
		{name: "unknown entity type", entityType: "EDGE", labels: []string{"X"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, varName, err := entityPatternMulti(tt.entityType, tt.labels)
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

func TestBracketList(t *testing.T) {
	got := bracketList("n", []string{"title", "body"})
	want := "[n.`title`, n.`body`]"
	if got != want {
		t.Errorf("bracketList() = %q, want %q", got, want)
	}
}

func TestMapProjection(t *testing.T) {
	got := mapProjection("n", []string{"title", "body"})
	want := "n{.`title`, .`body`}"
	if got != want {
		t.Errorf("mapProjection() = %q, want %q", got, want)
	}
}

func TestQuoteIdentifier(t *testing.T) {
	if got := quoteIdentifier("Person"); got != "`Person`" {
		t.Errorf("quoteIdentifier() = %q, want %q", got, "`Person`")
	}
	if got := quoteIdentifier("weird`name"); got != "`weird``name`" {
		t.Errorf("quoteIdentifier() = %q, want %q", got, "`weird``name`")
	}
}

func TestQuoteStringLiteral(t *testing.T) {
	if got := quoteStringLiteral("english"); got != "'english'" {
		t.Errorf("quoteStringLiteral() = %q, want %q", got, "'english'")
	}
	if got := quoteStringLiteral(`o'brien`); got != `'o\'brien'` {
		t.Errorf("quoteStringLiteral() = %q, want %q", got, `'o\'brien'`)
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
}
