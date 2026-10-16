// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"fmt"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
)

// NodeRef is the structured way every graph tool identifies nodes: by
// elementId (ID), and/or by Labels (all must be present), exact-equality
// Match, and richer Filters. All given criteria are AND-ed.
type NodeRef struct {
	ID      string        `json:"id,omitempty"`
	Labels  []string      `json:"labels,omitempty"`
	Match   cypher.Params `json:"match,omitempty"`
	Filters []Filter      `json:"filters,omitempty"`
}

// HasPredicate reports whether the reference narrows by anything other than
// labels. Mutating tools require this so a label alone can never select
// every node of that label.
func (r NodeRef) HasPredicate() bool {
	return r.ID != "" || len(r.Match) > 0 || len(r.Filters) > 0
}

// Conditions renders the ID, Match and Filters criteria as AND-able
// conditions on varExpr.
func (r NodeRef) Conditions(varExpr string, b *Builder) ([]string, error) {
	var conds []string
	if r.ID != "" {
		conds = append(conds, fmt.Sprintf("elementId(%s) = %s", varExpr, b.Param(r.ID)))
	}
	m, err := MatchConditions(varExpr, r.Match, b)
	if err != nil {
		return nil, err
	}
	conds = append(conds, m...)
	f, err := FilterConditions(varExpr, r.Filters, b)
	if err != nil {
		return nil, err
	}
	return append(conds, f...), nil
}

// Pattern renders "(v:`A`&`B`)" and the WHERE conditions that go with it.
func (r NodeRef) Pattern(v string, b *Builder) (pattern string, conds []string, err error) {
	labels, err := LabelExpr(r.Labels)
	if err != nil {
		return "", nil, err
	}
	conds, err = r.Conditions(v, b)
	if err != nil {
		return "", nil, err
	}
	return "(" + v + labels + ")", conds, nil
}

// Inline renders "(v:`A` WHERE cond AND cond)" for use inside a path
// pattern, where the predicate has to apply before path selection
// (ANY SHORTEST etc.) rather than after it.
func (r NodeRef) Inline(v string, b *Builder) (string, error) {
	labels, err := LabelExpr(r.Labels)
	if err != nil {
		return "", err
	}
	conds, err := r.Conditions(v, b)
	if err != nil {
		return "", err
	}
	if len(conds) == 0 {
		return "(" + v + labels + ")", nil
	}
	return "(" + v + labels + " WHERE " + strings.Join(conds, " AND ") + ")", nil
}
