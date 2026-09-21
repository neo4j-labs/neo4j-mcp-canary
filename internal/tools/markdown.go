// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// encodeMarkdown renders a decoded JSON value as Markdown. A uniform array
// of flat objects (every element a map with the same key set and only
// scalar values — the common case: Cypher result rows, GDS procedure
// records) renders as a table; everything else renders as a nested bulleted
// key/value list. This mirrors EncodeOutput's TOON path: the caller decodes
// JSON into `any` once, and this function only ever sees that generic value.
//
// Column order in a table is alphabetical rather than the source JSON's
// field order, because encoding/json's decode into map[string]any does not
// preserve key order — there is nothing stable to preserve.
func encodeMarkdown(v any) string {
	var b strings.Builder
	writeMarkdownValue(&b, v, 0)
	return strings.TrimRight(b.String(), "\n")
}

func writeMarkdownValue(b *strings.Builder, v any, depth int) {
	switch val := v.(type) {
	case map[string]any:
		writeMarkdownObject(b, val, depth)
	case []any:
		if rows, cols, ok := flatObjectArray(val); ok {
			writeMarkdownTable(b, rows, cols)
			return
		}
		writeMarkdownList(b, val, depth)
	default:
		b.WriteString(scalarString(val))
		b.WriteString("\n")
	}
}

func writeMarkdownObject(b *strings.Builder, m map[string]any, depth int) {
	if len(m) == 0 {
		b.WriteString("(empty)\n")
		return
	}
	keys := sortedKeys(m)
	indent := strings.Repeat("  ", depth)
	for _, k := range keys {
		v := m[k]
		if isScalar(v) {
			fmt.Fprintf(b, "%s- **%s**: %s\n", indent, k, scalarString(v))
			continue
		}
		fmt.Fprintf(b, "%s- **%s**:\n", indent, k)
		writeMarkdownNested(b, v, depth+1)
	}
}

func writeMarkdownList(b *strings.Builder, arr []any, depth int) {
	if len(arr) == 0 {
		b.WriteString("(empty)\n")
		return
	}
	indent := strings.Repeat("  ", depth)
	for _, item := range arr {
		if isScalar(item) {
			fmt.Fprintf(b, "%s- %s\n", indent, scalarString(item))
			continue
		}
		b.WriteString(indent + "-\n")
		writeMarkdownNested(b, item, depth+1)
	}
}

// writeMarkdownNested renders a non-scalar value that sits under a bullet
// (an object/array key or list item), indenting a nested table's rows to
// match its parent's depth.
func writeMarkdownNested(b *strings.Builder, v any, depth int) {
	switch val := v.(type) {
	case map[string]any:
		writeMarkdownObject(b, val, depth)
	case []any:
		writeMarkdownValue(b, val, depth)
	}
}

// flatObjectArray reports whether arr is a non-empty array of map[string]any
// sharing an identical key set, where every value is itself scalar. Column
// names are returned sorted for a stable header order.
func flatObjectArray(arr []any) ([]map[string]any, []string, bool) {
	if len(arr) == 0 {
		return nil, nil, false
	}
	rows := make([]map[string]any, 0, len(arr))
	var cols []string
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, nil, false
		}
		for _, v := range m {
			if !isScalar(v) {
				return nil, nil, false
			}
		}
		if i == 0 {
			cols = sortedKeys(m)
		} else if !sameKeys(cols, m) {
			return nil, nil, false
		}
		rows = append(rows, m)
	}
	return rows, cols, true
}

func writeMarkdownTable(b *strings.Builder, rows []map[string]any, cols []string) {
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	b.WriteString("| " + strings.Join(repeat("---", len(cols)), " | ") + " |\n")
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = escapeTableCell(scalarString(row[c]))
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
}

func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return s
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// scalarString formats a decoded JSON scalar (string, float64, bool, or nil)
// for display. Whole-number floats are rendered without a trailing ".0",
// since JSON has no distinct integer type and most Cypher/GDS scalar
// results a caller cares about here are effectively integers.
func scalarString(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	default:
		return fmt.Sprint(val)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sameKeys(cols []string, m map[string]any) bool {
	if len(cols) != len(m) {
		return false
	}
	for _, c := range cols {
		if _, ok := m[c]; !ok {
			return false
		}
	}
	return true
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
