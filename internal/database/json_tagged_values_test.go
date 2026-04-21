// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j/dbtype"
)

// TestConvertToTagged_Node pins the Node wrapping, including the camelCase
// key names and the deliberate absence of the deprecated numeric id.
func TestConvertToTagged_Node(t *testing.T) {
	node := dbtype.Node{
		Id:        42, // deprecated numeric id — must not appear in output
		ElementId: "4:abc:42",
		Labels:    []string{"Person", "Employee"},
		Props:     map[string]any{"name": "Alice", "age": int64(30)},
	}

	got, err := marshalTagged(node)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Pin the exact JSON shape: keys present, ordering stable (Go's json
	// package emits struct fields in declaration order), deprecated id
	// absent. If the wrapper type's fields are reordered or renamed, this
	// string comparison fails with a precise diff.
	want := `{"elementId":"4:abc:42","labels":["Person","Employee"],"properties":{"age":30,"name":"Alice"}}`
	if got != want {
		t.Errorf("node JSON mismatch\n got:  %s\n want: %s", got, want)
	}
}

// TestConvertToTagged_Relationship pins the Relationship wrapping. Same
// camelCase-and-no-numeric-id contract as Node, extended over the three
// identifier fields (element, startElement, endElement).
func TestConvertToTagged_Relationship(t *testing.T) {
	rel := dbtype.Relationship{
		Id:             100,                 // deprecated
		ElementId:      "5:abc:100",
		StartId:        1,                   // deprecated
		StartElementId: "4:abc:1",
		EndId:          2,                   // deprecated
		EndElementId:   "4:abc:2",
		Type:           "KNOWS",
		Props:          map[string]any{"since": int64(2020)},
	}

	got, err := marshalTagged(rel)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	want := `{"elementId":"5:abc:100","startElementId":"4:abc:1","endElementId":"4:abc:2","type":"KNOWS","properties":{"since":2020}}`
	if got != want {
		t.Errorf("relationship JSON mismatch\n got:  %s\n want: %s", got, want)
	}
}

// TestConvertToTagged_Path pins Path wrapping and, crucially, that Nodes and
// Relationships inside a Path are themselves wrapped (not passed through as
// raw dbtype values). Without the recursive step the elements would still
// emit PascalCase field names.
func TestConvertToTagged_Path(t *testing.T) {
	path := dbtype.Path{
		Nodes: []dbtype.Node{
			{ElementId: "4:abc:1", Labels: []string{"Person"}, Props: map[string]any{"name": "Alice"}},
			{ElementId: "4:abc:2", Labels: []string{"Person"}, Props: map[string]any{"name": "Bob"}},
		},
		Relationships: []dbtype.Relationship{
			{ElementId: "5:abc:1", StartElementId: "4:abc:1", EndElementId: "4:abc:2", Type: "KNOWS", Props: map[string]any{}},
		},
	}

	got, err := marshalTagged(path)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	want := `{"nodes":[{"elementId":"4:abc:1","labels":["Person"],"properties":{"name":"Alice"}},{"elementId":"4:abc:2","labels":["Person"],"properties":{"name":"Bob"}}],"relationships":[{"elementId":"5:abc:1","startElementId":"4:abc:1","endElementId":"4:abc:2","type":"KNOWS","properties":{}}]}`
	if got != want {
		t.Errorf("path JSON mismatch\n got:  %s\n want: %s", got, want)
	}
}

// TestConvertToTagged_Points verifies both 2D and 3D point wrapping, with
// particular attention to the `srid` key (Cypher's convention) rather than
// the driver's Go field name SpatialRefId.
func TestConvertToTagged_Points(t *testing.T) {
	t.Run("Point2D", func(t *testing.T) {
		p := dbtype.Point2D{X: 1.5, Y: 2.5, SpatialRefId: 4326}
		got, err := marshalTagged(p)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		want := `{"x":1.5,"y":2.5,"srid":4326}`
		if got != want {
			t.Errorf("Point2D JSON mismatch\n got:  %s\n want: %s", got, want)
		}
	})

	t.Run("Point3D", func(t *testing.T) {
		p := dbtype.Point3D{X: 1.5, Y: 2.5, Z: 3.5, SpatialRefId: 4979}
		got, err := marshalTagged(p)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		want := `{"x":1.5,"y":2.5,"z":3.5,"srid":4979}`
		if got != want {
			t.Errorf("Point3D JSON mismatch\n got:  %s\n want: %s", got, want)
		}
	})
}

// TestConvertToTagged_Temporal pins the ISO-8601 string output for every
// zone-variant temporal type the driver produces. Each sub-test contrasts
// with the broken baseline (dbtype.Date serialised as "{}") so the fix is
// self-documenting.
func TestConvertToTagged_Temporal(t *testing.T) {
	// Fixed reference instant with sub-second resolution so the trim-trailing-
	// zeros behaviour of the "999999999" layout verb is exercised in at
	// least one case. Using a single reference across sub-tests also makes
	// the expected strings easy to eyeball-verify against one another.
	ref := time.Date(2024, 6, 15, 14, 30, 45, 123000000, time.FixedZone("CEST", 2*3600))

	t.Run("Date", func(t *testing.T) {
		d := dbtype.Date(ref)
		got := convertToTagged(d).(string)
		if got != "2024-06-15" {
			t.Errorf("Date format = %q, want %q", got, "2024-06-15")
		}
	})

	t.Run("LocalTime", func(t *testing.T) {
		lt := dbtype.LocalTime(ref)
		got := convertToTagged(lt).(string)
		if got != "14:30:45.123" {
			t.Errorf("LocalTime format = %q, want %q", got, "14:30:45.123")
		}
	})

	t.Run("LocalDateTime", func(t *testing.T) {
		ldt := dbtype.LocalDateTime(ref)
		got := convertToTagged(ldt).(string)
		// Zone-less by contract — no "+02:00" or "Z" suffix.
		if got != "2024-06-15T14:30:45.123" {
			t.Errorf("LocalDateTime format = %q, want %q", got, "2024-06-15T14:30:45.123")
		}
	})

	t.Run("Time with offset", func(t *testing.T) {
		ot := dbtype.Time(ref)
		got := convertToTagged(ot).(string)
		// The offset must be preserved; this is the distinguishing trait
		// of dbtype.Time versus dbtype.LocalTime.
		if got != "14:30:45.123+02:00" {
			t.Errorf("Time format = %q, want %q", got, "14:30:45.123+02:00")
		}
	})

	t.Run("whole-second values have no fractional suffix", func(t *testing.T) {
		// Regression guard on the "999999999" verb: it must TRIM trailing
		// zeros, so a whole-second value yields "14:30:45" not "14:30:45.000".
		whole := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
		got := convertToTagged(dbtype.LocalTime(whole)).(string)
		if got != "14:30:45" {
			t.Errorf("LocalTime (whole second) format = %q, want %q", got, "14:30:45")
		}
	})
}

// TestConvertToTagged_Duration pins the ISO 8601 duration output. The cases
// are chosen to exercise each zero-omission branch and the nanosecond-fraction
// trimming — miss any one and the canonical shape drifts.
func TestConvertToTagged_Duration(t *testing.T) {
	tests := []struct {
		name     string
		duration dbtype.Duration
		want     string
	}{
		{
			name:     "zero duration is PT0S",
			duration: dbtype.Duration{},
			want:     "PT0S",
		},
		{
			name:     "days only",
			duration: dbtype.Duration{Days: 5},
			want:     "P5D",
		},
		{
			name:     "months decompose into years and months",
			duration: dbtype.Duration{Months: 14}, // 1 year 2 months
			want:     "P1Y2M",
		},
		{
			name:     "seconds decompose into hours minutes seconds",
			duration: dbtype.Duration{Seconds: 3661}, // 1h 1m 1s
			want:     "PT1H1M1S",
		},
		{
			name:     "all fields populated",
			duration: dbtype.Duration{Months: 14, Days: 5, Seconds: 3661},
			want:     "P1Y2M5DT1H1M1S",
		},
		{
			name:     "nanoseconds render as fractional seconds",
			duration: dbtype.Duration{Seconds: 1, Nanos: 500000000},
			want:     "PT1.5S",
		},
		{
			name:     "nanosecond precision is preserved and trailing zeros trimmed",
			duration: dbtype.Duration{Seconds: 1, Nanos: 123456789},
			want:     "PT1.123456789S",
		},
		{
			name:     "zero seconds with nanoseconds",
			duration: dbtype.Duration{Nanos: 1},
			want:     "PT0.000000001S",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertToTagged(tc.duration).(string)
			if got != tc.want {
				t.Errorf("Duration format = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConvertToTagged_Nested verifies the recursion path: a Node wrapped in
// a list wrapped in a map all the way down, with a Path that itself contains
// Nodes and Relationships. Without recursion any of the inner values would
// bypass wrapping and leak raw driver types into the JSON output.
func TestConvertToTagged_Nested(t *testing.T) {
	input := map[string]any{
		"solo": dbtype.Node{ElementId: "4:x:1", Labels: []string{"A"}, Props: map[string]any{}},
		"list": []any{
			dbtype.Node{ElementId: "4:x:2", Labels: []string{"B"}, Props: map[string]any{}},
			dbtype.Node{ElementId: "4:x:3", Labels: []string{"C"}, Props: map[string]any{}},
		},
		"point": dbtype.Point2D{X: 0, Y: 0, SpatialRefId: 4326},
		"when":  dbtype.Date(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	got, err := marshalTagged(input)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Map key ordering is lexicographic in Go's JSON marshaller (since 1.12
	// for map[string]any with string keys), so we can pin the exact output.
	// If the ordering contract ever changes this test will fail with a clear
	// signal to either update the assertion or switch to semantic comparison.
	want := `{"list":[{"elementId":"4:x:2","labels":["B"],"properties":{}},{"elementId":"4:x:3","labels":["C"],"properties":{}}],"point":{"x":0,"y":0,"srid":4326},"solo":{"elementId":"4:x:1","labels":["A"],"properties":{}},"when":"2024-01-01"}`
	if got != want {
		t.Errorf("nested JSON mismatch\n got:  %s\n want: %s", got, want)
	}
}

// TestConvertToTagged_Passthrough pins that values outside the recognised
// driver-type set are returned unchanged. This is the safety contract that
// lets convertToTagged be applied unconditionally to arbitrary result maps
// without fear of corrupting primitive values.
func TestConvertToTagged_Passthrough(t *testing.T) {
	cases := []any{
		nil,
		"a string",
		int64(42),
		3.14,
		true,
		false,
	}
	for _, v := range cases {
		got := convertToTagged(v)
		if got != v {
			t.Errorf("passthrough failed for %v (%T): got %v", v, v, got)
		}
	}
}

// TestConvertMapToTagged_NilMap verifies that a nil map stays nil rather than
// being allocated into an empty map. The distinction matters for JSON output
// shape: nil properties serialise as `null`, empty properties as `{}`. The
// driver emits Props as non-nil empty map so the common case always shows
// `{}`, but we preserve the nil handling for defensive completeness.
func TestConvertMapToTagged_NilMap(t *testing.T) {
	var nilMap map[string]any
	if got := convertMapToTagged(nilMap); got != nil {
		t.Errorf("expected nil for nil input, got: %v", got)
	}
}

// marshalTagged is a test helper that runs convertToTagged and json.Marshal
// in sequence, so assertions can compare the final on-wire bytes rather
// than the intermediate tagged-struct representation. Using json.Marshal
// (not MarshalIndent) gives compact output that's easy to compare via
// string equality.
func marshalTagged(v any) (string, error) {
	converted := convertToTagged(v)
	b, err := json.Marshal(converted)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
