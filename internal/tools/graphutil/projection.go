// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"fmt"
	"strings"
)

// PropsExpr renders the properties of varExpr: all of them, or just the
// named keys when props is non-empty (the way to leave out large values
// such as embeddings).
func PropsExpr(varExpr string, props []string) string {
	if len(props) == 0 {
		return fmt.Sprintf("properties(%s)", varExpr)
	}
	refs := make([]string, len(props))
	for i, p := range props {
		refs[i] = "." + QuoteIdentifier(p)
	}
	return fmt.Sprintf("%s{%s}", varExpr, strings.Join(refs, ", "))
}

// NodeMap renders a node as {id, labels, properties}. props, when
// non-empty, restricts which properties are returned.
func NodeMap(v string, props []string) string {
	return fmt.Sprintf("{id: elementId(%s), labels: labels(%s), properties: %s}", v, v, PropsExpr(v, props))
}

// NodeSummary renders a node as {id, labels}, plus the named properties
// when props is non-empty.
func NodeSummary(v string, props []string) string {
	if len(props) == 0 {
		return fmt.Sprintf("{id: elementId(%s), labels: labels(%s)}", v, v)
	}
	return NodeMap(v, props)
}

// RelMap renders a relationship as {id, type, from, to, properties}, where
// from/to are the endpoints' elementIds.
func RelMap(v string, props []string) string {
	return fmt.Sprintf("{id: elementId(%s), type: type(%s), from: elementId(startNode(%s)), to: elementId(endNode(%s)), properties: %s}",
		v, v, v, v, PropsExpr(v, props))
}

// RelSummary renders a relationship as {id, type, from, to} with no properties.
func RelSummary(v string) string {
	return fmt.Sprintf("{id: elementId(%s), type: type(%s), from: elementId(startNode(%s)), to: elementId(endNode(%s))}", v, v, v, v)
}

// PathMap renders a path as {length, nodes, relationships}; nodes carry
// {id, labels} plus nodeProps, relationships carry {id, type, from, to}.
func PathMap(p string, nodeProps []string) string {
	return fmt.Sprintf("{length: length(%[1]s), nodes: [x IN nodes(%[1]s) | %[2]s], relationships: [x IN relationships(%[1]s) | %[3]s]}",
		p, NodeSummary("x", nodeProps), RelSummary("x"))
}
