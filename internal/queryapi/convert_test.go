// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"testing"
	"time"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/db"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/dbtype"
)

// newQueryRecords builds real *query.Record values the same way the SDK
// does internally (via EagerResultTransformer, which is exported), so tests
// exercise convertRecord/convertValue against the SDK's actual Record type
// rather than a hand-rolled stand-in. query.Record has no exported
// constructor, which is why this indirection is needed.
func newQueryRecords(t *testing.T, fields []string, rows [][]any) []*query.Record {
	t.Helper()
	resp := &query.Response{Fields: fields, Rows: rows}
	eager, err := query.EagerResultTransformer(resp)
	if err != nil {
		t.Fatalf("EagerResultTransformer: %v", err)
	}
	return eager.Records
}

func TestConvertRecord_Scalars(t *testing.T) {
	records := newQueryRecords(t, []string{"s", "i", "f", "b", "n"}, [][]any{
		{"hello", int64(42), 3.14, true, nil},
	})

	got := convertRecord(records[0])
	want := &db.Record{
		Keys:   []string{"s", "i", "f", "b", "n"},
		Values: []any{"hello", int64(42), 3.14, true, nil},
	}

	gotJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{got})
	if err != nil {
		t.Fatalf("FormatRecordsAsJSON(got): %v", err)
	}
	wantJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{want})
	if err != nil {
		t.Fatalf("FormatRecordsAsJSON(want): %v", err)
	}
	if gotJSON != wantJSON {
		t.Errorf("scalar conversion mismatch:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestConvertRecord_NodeAndRelationship(t *testing.T) {
	sdkNode := &query.Node{
		ElementID:  "4:abc:1",
		Labels:     []string{"Person"},
		Properties: map[string]any{"name": "Alice", "age": int64(30)},
	}
	sdkRel := &query.Relationship{
		ElementID:          "5:abc:2",
		StartNodeElementID: "4:abc:1",
		EndNodeElementID:   "4:abc:3",
		Type:               "KNOWS",
		Properties:         map[string]any{"since": int64(2020)},
	}

	records := newQueryRecords(t, []string{"n", "r"}, [][]any{{sdkNode, sdkRel}})
	got := convertRecord(records[0])

	want := &db.Record{
		Keys: []string{"n", "r"},
		Values: []any{
			dbtype.Node{
				ElementId: "4:abc:1",
				Labels:    []string{"Person"},
				Props:     map[string]any{"name": "Alice", "age": int64(30)},
			},
			dbtype.Relationship{
				ElementId:      "5:abc:2",
				StartElementId: "4:abc:1",
				EndElementId:   "4:abc:3",
				Type:           "KNOWS",
				Props:          map[string]any{"since": int64(2020)},
			},
		},
	}

	assertSameJSON(t, got, want)
}

func TestConvertRecord_Path(t *testing.T) {
	sdkPath := query.Path{
		Nodes: []*query.Node{
			{ElementID: "4:abc:1", Labels: []string{"Person"}, Properties: map[string]any{}},
			{ElementID: "4:abc:2", Labels: []string{"Person"}, Properties: map[string]any{}},
		},
		Relationships: []*query.Relationship{
			{ElementID: "5:abc:1", StartNodeElementID: "4:abc:1", EndNodeElementID: "4:abc:2", Type: "KNOWS", Properties: map[string]any{}},
		},
	}

	records := newQueryRecords(t, []string{"p"}, [][]any{{sdkPath}})
	got := convertRecord(records[0])

	want := &db.Record{
		Keys: []string{"p"},
		Values: []any{
			dbtype.Path{
				Nodes: []dbtype.Node{
					{ElementId: "4:abc:1", Labels: []string{"Person"}, Props: map[string]any{}},
					{ElementId: "4:abc:2", Labels: []string{"Person"}, Props: map[string]any{}},
				},
				Relationships: []dbtype.Relationship{
					{ElementId: "5:abc:1", StartElementId: "4:abc:1", EndElementId: "4:abc:2", Type: "KNOWS", Props: map[string]any{}},
				},
			},
		},
	}

	assertSameJSON(t, got, want)
}

func TestConvertRecord_Duration(t *testing.T) {
	sdkDuration := query.Duration{Months: 14, Days: 3, Seconds: 3661, Nanos: 500000000}

	records := newQueryRecords(t, []string{"d"}, [][]any{{sdkDuration}})
	got := convertRecord(records[0])

	want := &db.Record{
		Keys:   []string{"d"},
		Values: []any{dbtype.Duration{Months: 14, Days: 3, Seconds: 3661, Nanos: 500000000}},
	}

	assertSameJSON(t, got, want)
}

func TestConvertRecord_Point(t *testing.T) {
	tests := []struct {
		name  string
		point query.Point
		want  any
	}{
		{
			name:  "2D",
			point: query.Point{SRID: 4326, X: 12.3, Y: 45.6, Is3D: false},
			want:  dbtype.Point2D{X: 12.3, Y: 45.6, SpatialRefId: 4326},
		},
		{
			name:  "3D",
			point: query.Point{SRID: 9157, X: 1, Y: 2, Z: 3, Is3D: true},
			want:  dbtype.Point3D{X: 1, Y: 2, Z: 3, SpatialRefId: 9157},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := newQueryRecords(t, []string{"pt"}, [][]any{{tt.point}})
			got := convertRecord(records[0])
			want := &db.Record{Keys: []string{"pt"}, Values: []any{tt.want}}
			assertSameJSON(t, got, want)
		})
	}
}

func TestConvertRecord_List(t *testing.T) {
	sdkNode := &query.Node{ElementID: "4:abc:1", Labels: []string{"Person"}, Properties: map[string]any{}}

	records := newQueryRecords(t, []string{"list"}, [][]any{
		{[]any{int64(1), "two", sdkNode}},
	})
	got := convertRecord(records[0])

	want := &db.Record{
		Keys: []string{"list"},
		Values: []any{
			[]any{
				int64(1),
				"two",
				dbtype.Node{ElementId: "4:abc:1", Labels: []string{"Person"}, Props: map[string]any{}},
			},
		},
	}

	assertSameJSON(t, got, want)
}

func TestConvertTime_Heuristic(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want any
	}{
		{
			name: "midnight UTC decodes as Date",
			in:   time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			want: dbtype.Date(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)),
		},
		{
			name: "value with a non-UTC offset decodes as Time",
			in:   time.Date(2024, 3, 15, 14, 30, 0, 0, time.FixedZone("+02:00", 2*3600)),
			want: dbtype.Time(time.Date(2024, 3, 15, 14, 30, 0, 0, time.FixedZone("+02:00", 2*3600))),
		},
		{
			name: "zone-less non-midnight value decodes as LocalDateTime",
			in:   time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC),
			want: dbtype.LocalDateTime(time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertValue(tt.in)
			gotJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{{Keys: []string{"t"}, Values: []any{got}}})
			if err != nil {
				t.Fatalf("FormatRecordsAsJSON(got): %v", err)
			}
			wantJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{{Keys: []string{"t"}, Values: []any{tt.want}}})
			if err != nil {
				t.Fatalf("FormatRecordsAsJSON(want): %v", err)
			}
			if gotJSON != wantJSON {
				t.Errorf("convertTime(%v):\ngot:  %s\nwant: %s", tt.in, gotJSON, wantJSON)
			}
		})
	}
}

// assertSameJSON renders got and want through the shared
// database.FormatRecordsAsJSON formatter and fails the test if they differ.
// This is the parity check that matters: it doesn't just compare Go values,
// it confirms the adapted record produces byte-identical MCP-facing JSON to
// a hand-built Bolt-driver equivalent.
func assertSameJSON(t *testing.T, got, want *neo4j.Record) {
	t.Helper()
	gotJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{got})
	if err != nil {
		t.Fatalf("FormatRecordsAsJSON(got): %v", err)
	}
	wantJSON, err := database.FormatRecordsAsJSON([]*neo4j.Record{want})
	if err != nil {
		t.Fatalf("FormatRecordsAsJSON(want): %v", err)
	}
	if gotJSON != wantJSON {
		t.Errorf("conversion mismatch:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}
