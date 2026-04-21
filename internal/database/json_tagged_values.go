// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"fmt"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j/dbtype"
)

// This file defines the JSON-tagged wrapper types and the recursive conversion
// function used by QueryResultToJSON and Neo4jRecordsToJSON to render Neo4j
// driver values in a client-friendly shape.
//
// The driver's built-in types (dbtype.Node, dbtype.Date, etc.) carry neither
// json.Marshaler implementations nor JSON struct tags. json.Marshal therefore
// falls back to reflection, which either emits PascalCase Go field names
// (Node, Relationship, Path, Point2D, Point3D, Duration) or an empty object
// (temporal types whose fields are unexported). Both behaviours are wrong for
// an MCP client: they diverge from the Python Neo4j MCP server, diverge from
// Cypher and Bolt conventions, and in the temporal case silently drop data.
//
// convertToTagged(v) is the entry point: it walks v recursively, replacing
// every recognised driver type with either a tagged wrapper (for structured
// types) or a formatted string (for temporal types, which are most naturally
// consumed as ISO 8601). Unrecognised values pass through unchanged, so
// adding support for a new driver type is purely additive — a single switch
// arm plus a test.

// taggedNode replaces dbtype.Node in the serialised output. Fields are
// camelCase to match the Python Neo4j MCP server's shape and the driver's
// Bolt-protocol naming. The deprecated numeric Id is deliberately omitted:
// elementId is the only supported stable identifier, and carrying both
// invites callers to depend on the wrong one.
type taggedNode struct {
	ElementId  string         `json:"elementId"`
	Labels     []string       `json:"labels"`
	Properties map[string]any `json:"properties"`
}

// taggedRelationship replaces dbtype.Relationship. Same treatment as
// taggedNode for camelCase and elementId-only. The deprecated numeric
// StartId / EndId are omitted; startElementId / endElementId are the
// supported stable identifiers for the endpoints.
type taggedRelationship struct {
	ElementId      string         `json:"elementId"`
	StartElementId string         `json:"startElementId"`
	EndElementId   string         `json:"endElementId"`
	Type           string         `json:"type"`
	Properties     map[string]any `json:"properties"`
}

// taggedPath replaces dbtype.Path. A path is a recursive structure over nodes
// and relationships; we delegate the wrapping of each element to the
// top-level convertToTagged rather than inlining it here, so the wrapper
// types stay single-purpose.
type taggedPath struct {
	Nodes         []taggedNode         `json:"nodes"`
	Relationships []taggedRelationship `json:"relationships"`
}

// taggedPoint2D replaces dbtype.Point2D. The JSON key for the spatial
// reference identifier is `srid`, matching the key Cypher's point() function
// uses — see https://neo4j.com/docs/cypher-manual/current/values-and-types/spatial/
// — rather than the driver's Go field name SpatialRefId.
type taggedPoint2D struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Srid uint32  `json:"srid"`
}

// taggedPoint3D mirrors taggedPoint2D with an additional z coordinate.
type taggedPoint3D struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
	Srid uint32  `json:"srid"`
}

// Temporal formatting constants. These are fixed-precision Go time layouts
// that retain nanosecond resolution when present and omit it cleanly when
// not — the "999999999" verb trims trailing zeros, producing "14:30:00"
// rather than "14:30:00.000000000" for whole-second values.
//
// These layouts are intentionally NOT RFC 3339: RFC 3339 mandates a timezone
// suffix on date-times, but Neo4j's LocalDateTime and LocalTime are explicitly
// zone-less. Using a layout that preserves the zone-less form prevents
// round-tripping through JSON from silently inventing a timezone.
const (
	dateFormat          = "2006-01-02"
	localTimeFormat     = "15:04:05.999999999"
	localDateTimeFormat = "2006-01-02T15:04:05.999999999"
	offsetTimeFormat    = "15:04:05.999999999Z07:00"
)

// convertToTagged walks v and returns a shape suitable for json.Marshal.
// The switch is exhaustive over driver types; recursion into maps, slices,
// and the Properties fields of the wrappers themselves ensures that a
// Node nested inside a list nested inside a map still gets wrapped.
//
// Anything not recognised is returned as-is. Primitives (int, float, string,
// bool, nil) fall through naturally, and time.Time already marshals to
// RFC 3339 without assistance — which is why there's no arm for it here.
//
// The order of arms is not semantically significant (driver types don't
// shadow each other) but the structured types come first as the common case.
func convertToTagged(v any) any {
	switch typed := v.(type) {
	case dbtype.Node:
		return taggedNode{
			ElementId:  typed.ElementId,
			Labels:     typed.Labels,
			Properties: convertMapToTagged(typed.Props),
		}
	case dbtype.Relationship:
		return taggedRelationship{
			ElementId:      typed.ElementId,
			StartElementId: typed.StartElementId,
			EndElementId:   typed.EndElementId,
			Type:           typed.Type,
			Properties:     convertMapToTagged(typed.Props),
		}
	case dbtype.Path:
		// Pre-allocate with len() because Nodes and Relationships slices have
		// a stable length over the life of a path — nothing is appended after
		// driver-side construction. The type assertions on the recursive
		// convertToTagged return are safe because the arms above are
		// guaranteed to produce the matching wrapper type for these inputs.
		nodes := make([]taggedNode, len(typed.Nodes))
		for i, n := range typed.Nodes {
			nodes[i] = convertToTagged(n).(taggedNode)
		}
		rels := make([]taggedRelationship, len(typed.Relationships))
		for i, r := range typed.Relationships {
			rels[i] = convertToTagged(r).(taggedRelationship)
		}
		return taggedPath{Nodes: nodes, Relationships: rels}
	case dbtype.Point2D:
		return taggedPoint2D{X: typed.X, Y: typed.Y, Srid: typed.SpatialRefId}
	case dbtype.Point3D:
		return taggedPoint3D{X: typed.X, Y: typed.Y, Z: typed.Z, Srid: typed.SpatialRefId}
	case dbtype.Date:
		return typed.Time().Format(dateFormat)
	case dbtype.LocalTime:
		return typed.Time().Format(localTimeFormat)
	case dbtype.LocalDateTime:
		return typed.Time().Format(localDateTimeFormat)
	case dbtype.Time:
		return typed.Time().Format(offsetTimeFormat)
	case dbtype.Duration:
		return formatDurationISO(typed)
	case map[string]any:
		return convertMapToTagged(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = convertToTagged(item)
		}
		return out
	}
	return v
}

// convertMapToTagged recursively wraps every value in m. Returns nil for a
// nil input map rather than an allocated empty map, so the JSON distinction
// between an absent properties field and an empty object is preserved when
// it matters. Callers that need a guaranteed non-nil map should do that
// coercion at the call site.
func convertMapToTagged(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = convertToTagged(val)
	}
	return out
}

// formatDurationISO renders a dbtype.Duration as an ISO 8601 duration string
// such as "P1Y2M3DT4H5M6.789S". The driver's Duration decomposes into Months,
// Days, Seconds (int64), and Nanos (int); we split Months into years/months
// and Seconds into hours/minutes/seconds to reach the canonical ISO shape.
//
// Zero-valued components are omitted so the common cases stay compact — a
// one-day duration renders as "P1D", not "P0Y0M1DT0H0M0S". An empty duration
// returns the canonical zero form "PT0S" rather than the ambiguous bare "P".
//
// We don't delegate to dbtype.Duration.String() because its output format is
// not contractually stable across driver versions and has historically varied
// in minor details (space handling, fractional second representation). Doing
// the formatting here pins the MCP output shape to a single well-known form
// regardless of which driver version is in use.
func formatDurationISO(d dbtype.Duration) string {
	years := d.Months / 12
	months := d.Months % 12
	hours := d.Seconds / 3600
	minutes := (d.Seconds % 3600) / 60
	seconds := d.Seconds % 60

	var b strings.Builder
	b.WriteString("P")
	if years != 0 {
		fmt.Fprintf(&b, "%dY", years)
	}
	if months != 0 {
		fmt.Fprintf(&b, "%dM", months)
	}
	if d.Days != 0 {
		fmt.Fprintf(&b, "%dD", d.Days)
	}

	hasTimeComponent := hours != 0 || minutes != 0 || seconds != 0 || d.Nanos != 0
	if hasTimeComponent {
		b.WriteString("T")
		if hours != 0 {
			fmt.Fprintf(&b, "%dH", hours)
		}
		if minutes != 0 {
			fmt.Fprintf(&b, "%dM", minutes)
		}
		if seconds != 0 || d.Nanos != 0 {
			if d.Nanos != 0 {
				// Trim trailing zeros from the nanosecond fraction so "1.500000000S"
				// renders as "1.5S". Nanos is in [0, 999_999_999], so the nine-digit
				// zero-padded representation fits cleanly.
				fracStr := strings.TrimRight(fmt.Sprintf("%09d", d.Nanos), "0")
				fmt.Fprintf(&b, "%d.%sS", seconds, fracStr)
			} else {
				fmt.Fprintf(&b, "%dS", seconds)
			}
		}
	}

	// b.Len() == 1 means only the "P" was appended — the empty-duration case.
	// Return the canonical zero form so downstream consumers don't have to
	// special-case "P".
	if b.Len() == 1 {
		return "PT0S"
	}
	return b.String()
}
