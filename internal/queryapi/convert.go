// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"math"
	"time"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/db"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/dbtype"
)

// convertRecord adapts a *query.Record from the Query API SDK into a
// neo4j.Record (a type alias for db.Record — see neo4j/aliases.go in the
// driver), so downstream code that only knows about database.Service
// (RecordFormatter, the MCP tool handlers, the analytics event builder in
// internal/server/server.go) can consume Query API results exactly as it
// consumes Bolt driver results, with no changes on their side.
func convertRecord(rec *query.Record) *neo4j.Record {
	keys := rec.Keys()
	values := rec.Values()
	converted := make([]any, len(values))
	for i, v := range values {
		converted[i] = convertValue(v)
	}
	return &db.Record{Keys: keys, Values: converted}
}

// convertValue recursively converts one decoded Query API value into the
// equivalent neo4j driver value. Scalars that already match the driver's
// RecordValue set (bool, int64, float64, string, []byte, nil) pass through
// unchanged — see the table in the queryType conversion doc comment on
// convertRecord's caller for the full type-by-type mapping.
//
// KNOWN FIDELITY GAP: the Query API SDK's decoder collapses Neo4j's
// Date/Time/LocalTime/LocalDateTime/OffsetDateTime Cypher types all down to
// plain time.Time (see query-go-sdk internal/decode/types.go), discarding
// which Cypher type a given value originally was. convertTime below can only
// approximate the original dbtype.* variant from the decoded time.Time's
// shape. This is documented as a launch caveat rather than blocking this
// change — see the project plan for the fast-follow to decode the wire
// $type tag directly if exact parity turns out to matter.
func convertValue(v any) any {
	switch typed := v.(type) {
	case *query.Node:
		return dbtype.Node{
			ElementId: typed.ElementID,
			Labels:    typed.Labels,
			Props:     convertPropertyMap(typed.Properties),
		}
	case *query.Relationship:
		return dbtype.Relationship{
			ElementId:      typed.ElementID,
			StartElementId: typed.StartNodeElementID,
			EndElementId:   typed.EndNodeElementID,
			Type:           typed.Type,
			Props:          convertPropertyMap(typed.Properties),
		}
	case query.Path:
		nodes := make([]dbtype.Node, len(typed.Nodes))
		for i, n := range typed.Nodes {
			nodes[i] = convertValue(n).(dbtype.Node)
		}
		rels := make([]dbtype.Relationship, len(typed.Relationships))
		for i, r := range typed.Relationships {
			rels[i] = convertValue(r).(dbtype.Relationship)
		}
		return dbtype.Path{Nodes: nodes, Relationships: rels}
	case query.Duration:
		return dbtype.Duration{
			Months:  typed.Months,
			Days:    typed.Days,
			Seconds: typed.Seconds,
			Nanos:   int(typed.Nanos),
		}
	case query.Point:
		if typed.Is3D {
			return dbtype.Point3D{X: typed.X, Y: typed.Y, Z: typed.Z, SpatialRefId: spatialRefID(typed.SRID)}
		}
		return dbtype.Point2D{X: typed.X, Y: typed.Y, SpatialRefId: spatialRefID(typed.SRID)}
	case time.Time:
		return convertTime(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = convertValue(item)
		}
		return out
	case map[string]any:
		return convertPropertyMap(typed)
	default:
		// bool, int64, float64, string, []byte, nil, query.Vector,
		// query.Unsupported all pass through unchanged: either they already
		// match a driver RecordValue type, or (Vector/Unsupported) they have
		// no driver equivalent and are surfaced as-is rather than dropped.
		return v
	}
}

// convertPropertyMap recursively converts every value in a properties map
// (node/relationship properties, or a Cypher map value). Returns nil for a
// nil input rather than an allocated empty map, mirroring
// database.convertMapToTagged's same nil-preserving behavior downstream.
func convertPropertyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = convertValue(v)
	}
	return out
}

// convertTime approximates which of Neo4j's four zone-less/zoned temporal
// types a decoded time.Time originally was. See the fidelity-gap note on
// convertValue: the original Cypher type has already been lost by the SDK's
// decoder by the time we see this value, so this is a best-effort heuristic,
// not a lossless reconstruction:
//
//   - A value at exact midnight UTC with no sub-second component is treated
//     as a Date (Neo4j's Date values decode via layoutDate, "2006-01-02",
//     which time.Parse anchors at midnight UTC).
//   - A value whose location is a fixed, named, or non-UTC offset is treated
//     as a zoned Time (Neo4j Time/OffsetDateTime values carry real offset
//     information; a bare LocalTime/LocalDateTime does not).
//   - Everything else defaults to LocalDateTime, the most general zone-less
//     case.
func convertTime(t time.Time) any {
	if isMidnightUTC(t) {
		return dbtype.Date(t)
	}
	if _, offset := t.Zone(); offset != 0 || t.Location() != time.UTC {
		return dbtype.Time(t)
	}
	return dbtype.LocalDateTime(t)
}

func isMidnightUTC(t time.Time) bool {
	return t.Location() == time.UTC &&
		t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0
}

// spatialRefID converts a decoded SRID (a plain int on query.Point) to the
// uint32 dbtype.Point2D/3D expect. Real SRIDs are always small non-negative
// values (4326, 4979, 7203, 9157, ...); this clamps out-of-range input to 0
// rather than silently wrapping, on the same principle as
// database.ExtractEstimatedRows clamping a negative value to 0 — a
// protocol-level bug should surface as an obviously-wrong 0, not a wrapped
// garbage SRID.
func spatialRefID(srid int) uint32 {
	if srid < 0 || srid > math.MaxUint32 {
		return 0
	}
	return uint32(srid)
}
