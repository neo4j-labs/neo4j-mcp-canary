// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/admin"
)

func TestApplyEditsTo_OnlyTouchesEditableFields(t *testing.T) {
	base := baseConfig()
	base.Username = "should-not-change"
	base.AdminToken = "should-not-change"
	base.LogLevel = "debug"

	edits := toEditableConfig(base)
	edits.ReadOnly = true
	edits.HTTPPort = "9999"

	got := applyEditsTo(base, edits)

	if !got.ReadOnly {
		t.Error("ReadOnly edit was not applied")
	}
	if got.HTTPPort != "9999" {
		t.Errorf("HTTPPort = %q, want 9999", got.HTTPPort)
	}
	// Fields EditableConfig doesn't expose must come through untouched.
	if got.Username != "should-not-change" {
		t.Errorf("Username = %q, want unchanged", got.Username)
	}
	if got.AdminToken != "should-not-change" {
		t.Errorf("AdminToken = %q, want unchanged (not editable via the dashboard)", got.AdminToken)
	}
	if got.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want unchanged", got.LogLevel)
	}
}

func TestToEditableConfig_RoundTripsWithApplyEditsTo(t *testing.T) {
	base := baseConfig()
	edits := toEditableConfig(base)

	got := applyEditsTo(base, edits)
	if *got != *base {
		t.Errorf("applyEditsTo(base, toEditableConfig(base)) = %+v, want unchanged %+v", got, base)
	}
}

func TestDiffEditableFields(t *testing.T) {
	old := admin.EditableConfig{ReadOnly: false, HTTPPort: "80", URI: "bolt://a"}
	next := admin.EditableConfig{ReadOnly: true, HTTPPort: "80", URI: "bolt://b"}

	changes := diffEditableFields(old, next)

	fields := make(map[string]admin.FieldDiff, len(changes))
	for _, c := range changes {
		fields[c.Field] = c
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want exactly ReadOnly and URI", changes)
	}
	if _, ok := fields["ReadOnly"]; !ok {
		t.Error("expected a ReadOnly diff entry")
	}
	if _, ok := fields["URI"]; !ok {
		t.Error("expected a URI diff entry")
	}
	if _, ok := fields["HTTPPort"]; ok {
		t.Error("HTTPPort didn't change and shouldn't appear in the diff")
	}
}
