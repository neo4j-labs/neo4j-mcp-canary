// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package graphutil holds the building blocks shared by the parameter-only
// graph tools (tools.CategoryGraph, CategoryTraversal, CategoryPatterns):
// identifier quoting, structured filters, node references, result
// projections, and the generic handler that runs a built query. No tool in
// those categories accepts raw Cypher; every value reaches the database as a
// bound parameter and every identifier is backtick-quoted.
package graphutil

import (
	"fmt"
	"strings"
)

// Builder allocates sequentially named query parameters (, , ...) so
// no caller-supplied text — property keys, aliases, values — ever appears in
// a parameter name.
type Builder struct {
	params map[string]any
}

func NewBuilder() *Builder {
	return &Builder{params: map[string]any{}}
}

// Param registers v and returns its "$pN" placeholder.
func (b *Builder) Param(v any) string {
	name := fmt.Sprintf("p%d", len(b.params))
	b.params[name] = v
	return "$" + name
}

func (b *Builder) Params() map[string]any {
	return b.params
}

// QuoteIdentifier backtick-quotes a Cypher identifier (label, relationship
// type, property key, or alias), doubling any embedded backtick.
func QuoteIdentifier(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// LabelExpr renders labels as a conjunctive label expression, e.g.
// [A B] → ":`A`&`B`". Empty input renders as "".
func LabelExpr(labels []string) (string, error) {
	return joinQuoted(":", "&", labels, "label")
}

// LabelList renders labels for CREATE/MERGE/SET/REMOVE, where labels are
// colon-separated rather than "&"-joined, e.g. [A B] → ":`A`:`B`".
func LabelList(labels []string) (string, error) {
	return joinQuoted(":", ":", labels, "label")
}

// TypeExpr renders relationship types as a disjunctive type expression, e.g.
// [A B] → ":`A`|`B`". Empty input renders as "".
func TypeExpr(types []string) (string, error) {
	return joinQuoted(":", "|", types, "relationship type")
}

func joinQuoted(prefix, sep string, names []string, what string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		if strings.TrimSpace(n) == "" {
			return "", fmt.Errorf("%s names must not be empty; check the %s list for blank entries", what, what)
		}
		quoted[i] = QuoteIdentifier(n)
	}
	return prefix + strings.Join(quoted, sep), nil
}

// Where renders conds as a " WHERE a AND b" suffix, or "" when empty.
func Where(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}
