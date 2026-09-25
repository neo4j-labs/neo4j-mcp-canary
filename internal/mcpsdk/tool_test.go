// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"fmt"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// sampleOutput exercises the shapes MustOutputSchemaFor needs to fix: a
// top-level nullable slice (Rows), a nested struct with its own nullable
// slice (Nested.Items), and a slice-of-structs whose element type also has
// a nullable slice (List[i].Tags) — the reflector emits Types:
// ["null","array"] for every one of these, at every nesting depth.
type sampleOutput struct {
	Rows   []map[string]any `json:"rows"`
	Nested struct {
		Items []string `json:"items"`
	} `json:"nested"`
	List []struct {
		Tags []string `json:"tags"`
	} `json:"list"`
}

// assertNoMultiValueTypes fails t if any schema node reachable from s still
// has more than one entry in Types.
func assertNoMultiValueTypes(t *testing.T, s *jsonschema.Schema, path string) {
	t.Helper()
	if len(s.Types) > 1 {
		t.Errorf("%s: Types = %v, want at most one type (nullable types should have been split into anyOf)", path, s.Types)
	}
	for name, child := range s.Properties {
		assertNoMultiValueTypes(t, child, path+".properties."+name)
	}
	if s.Items != nil {
		assertNoMultiValueTypes(t, s.Items, path+".items")
	}
	if s.AdditionalProperties != nil {
		assertNoMultiValueTypes(t, s.AdditionalProperties, path+".additionalProperties")
	}
	for i, branch := range s.AnyOf {
		assertNoMultiValueTypes(t, branch, fmt.Sprintf("%s.anyOf[%d]", path, i))
	}
}

func TestMustOutputSchemaFor_SplitsNullableTypes(t *testing.T) {
	schema := MustOutputSchemaFor[sampleOutput]()

	assertNoMultiValueTypes(t, schema, "$")

	rows := schema.Properties["rows"]
	if rows == nil {
		t.Fatal("expected a 'rows' property")
	}
	if len(rows.Types) != 0 {
		t.Errorf("rows.Types = %v, want empty (cleared in favor of anyOf)", rows.Types)
	}
	if len(rows.AnyOf) != 2 {
		t.Fatalf("rows.AnyOf = %+v, want exactly 2 branches", rows.AnyOf)
	}
	var sawNull, sawArray bool
	for _, branch := range rows.AnyOf {
		switch branch.Type {
		case "null":
			sawNull = true
		case "array":
			sawArray = true
		}
	}
	if !sawNull || !sawArray {
		t.Errorf("rows.AnyOf = %+v, want one {type: null} branch and one {type: array} branch", rows.AnyOf)
	}
	// Sibling keywords (here, the array's "items" schema) stay on the
	// property itself rather than being duplicated into the anyOf branch —
	// see splitNullableTypesEverywhere's doc comment for why that's correct.
	if rows.Items == nil {
		t.Error("expected rows.Items to still be set after splitting Types into anyOf")
	}

	nested := schema.Properties["nested"]
	if nested == nil || nested.Properties["items"] == nil {
		t.Fatal("expected nested.properties.items")
	}
	if len(nested.Properties["items"].Types) != 0 || len(nested.Properties["items"].AnyOf) != 2 {
		t.Errorf("nested.items = %+v, want Types cleared and a 2-branch anyOf", nested.Properties["items"])
	}

	list := schema.Properties["list"]
	if list == nil || list.Items == nil || list.Items.Properties["tags"] == nil {
		t.Fatal("expected list.items.properties.tags")
	}
	if len(list.Items.Properties["tags"].Types) != 0 || len(list.Items.Properties["tags"].AnyOf) != 2 {
		t.Errorf("list.items.tags = %+v, want Types cleared and a 2-branch anyOf", list.Items.Properties["tags"])
	}
}

func TestSplitNullableTypes_LeavesSingleTypeUntouched(t *testing.T) {
	s := &jsonschema.Schema{Type: "string"}
	splitNullableTypes(s)
	if s.Type != "string" || s.AnyOf != nil {
		t.Errorf("single-type schema was modified: %+v", s)
	}

	s2 := &jsonschema.Schema{Types: []string{"string"}}
	splitNullableTypes(s2)
	if len(s2.Types) != 1 || s2.Types[0] != "string" || s2.AnyOf != nil {
		t.Errorf("single-entry Types schema was modified: %+v", s2)
	}
}

func TestSplitNullableTypes_AllNullIsLeftAlone(t *testing.T) {
	// Not a shape jsonschema-go's reflector ever produces, but
	// splitNullableTypes should still leave a Types slice untouched when
	// there's no non-null type to split out (nothing meaningful to do).
	s := &jsonschema.Schema{Types: []string{"null", "null"}}
	splitNullableTypes(s)
	if s.AnyOf != nil {
		t.Errorf("all-null Types schema should be left alone: %+v", s)
	}
}
