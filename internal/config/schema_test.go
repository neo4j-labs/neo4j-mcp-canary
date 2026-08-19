// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"reflect"
	"testing"
)

// TestFields_MatchConfigStruct guards the exact failure mode that motivated
// this refactor: a Config struct field added without a corresponding schema
// entry (or vice versa) previously meant a CLI flag or env var silently did
// nothing. This test fails loudly instead.
func TestFields_MatchConfigStruct(t *testing.T) {
	structFieldNames := map[string]bool{}
	rt := reflect.TypeOf(Config{})
	for i := 0; i < rt.NumField(); i++ {
		structFieldNames[rt.Field(i).Name] = true
	}

	seen := map[string]bool{}
	for _, f := range Fields() {
		if f.Name == "" {
			t.Fatalf("schema field has empty Name: %+v", f)
		}
		if seen[f.Name] {
			t.Errorf("duplicate schema field Name %q", f.Name)
		}
		seen[f.Name] = true

		if !structFieldNames[f.Name] {
			t.Errorf("schema field %q has no matching Config struct field", f.Name)
		}
		if f.EnvVar == "" {
			t.Errorf("schema field %q has no EnvVar", f.Name)
		}
		if f.Setter == nil {
			t.Errorf("schema field %q has a nil Setter", f.Name)
		}
	}

	for name := range structFieldNames {
		if !seen[name] {
			t.Errorf("Config struct field %q has no matching entry in Fields()", name)
		}
	}
}
