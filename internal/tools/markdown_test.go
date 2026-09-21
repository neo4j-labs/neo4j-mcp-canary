// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

import (
	"encoding/json"
	"testing"
)

func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("failed to decode fixture JSON: %v", err)
	}
	return v
}

func TestEncodeMarkdown_TopLevelUniformArrayIsATable(t *testing.T) {
	v := decodeJSON(t, `[{"name":"gds.pageRank.stream","type":"procedure"},{"name":"gds.louvain.stream","type":"procedure"}]`)

	got := encodeMarkdown(v)
	want := "| name | type |\n" +
		"| --- | --- |\n" +
		"| gds.pageRank.stream | procedure |\n" +
		"| gds.louvain.stream | procedure |"
	if got != want {
		t.Errorf("encodeMarkdown() = %q, want %q", got, want)
	}
}

func TestEncodeMarkdown_MismatchedKeysFallBackToList(t *testing.T) {
	v := decodeJSON(t, `[{"name":"Alice"},{"name":"Bob","age":25}]`)

	got := encodeMarkdown(v)
	// Not a table (key sets differ) — each element renders as its own nested object.
	want := "-\n" +
		"  - **name**: Alice\n" +
		"-\n" +
		"  - **age**: 25\n" +
		"  - **name**: Bob"
	if got != want {
		t.Errorf("encodeMarkdown() = %q, want %q", got, want)
	}
}

func TestEncodeMarkdown_NestedNonFlatValueFallsBackToBullets(t *testing.T) {
	v := decodeJSON(t, `[{"key":"Person","value":{"type":"node","properties":{"name":"STRING"}}}]`)

	got := encodeMarkdown(v)
	want := "-\n" +
		"  - **key**: Person\n" +
		"  - **value**:\n" +
		"    - **properties**:\n" +
		"      - **name**: STRING\n" +
		"    - **type**: node"
	if got != want {
		t.Errorf("encodeMarkdown() = %q, want %q", got, want)
	}
}

func TestEncodeMarkdown_ScalarsAndEmptyCollections(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty array", `[]`, "(empty)"},
		{"empty object", `{}`, "(empty)"},
		{"bare string", `"hello"`, "hello"},
		{"bare number", `42`, "42"},
		{"bare float", `3.5`, "3.5"},
		{"bare bool", `true`, "true"},
		{"null", `null`, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := decodeJSON(t, tt.in)
			got := encodeMarkdown(v)
			if got != tt.want {
				t.Errorf("encodeMarkdown(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEncodeMarkdown_TableCellEscaping(t *testing.T) {
	v := decodeJSON(t, `[{"note":"a | b\nc"}]`)

	got := encodeMarkdown(v)
	want := "| note |\n| --- |\n| a \\| b<br>c |"
	if got != want {
		t.Errorf("encodeMarkdown() = %q, want %q", got, want)
	}
}
