// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

// RelRef selects relationships by elementId, type, endpoints, and/or
// properties. Relationships are always matched in their stored direction,
// From → To.
type RelRef struct {
	ID      string             `json:"id,omitempty"`
	Type    string             `json:"type,omitempty"`
	From    *graphutil.NodeRef `json:"from,omitempty"`
	To      *graphutil.NodeRef `json:"to,omitempty"`
	Match   cypher.Params      `json:"match,omitempty"`
	Filters []graphutil.Filter `json:"filters,omitempty"`
}

// HasPredicate reports whether the reference narrows by anything other than
// type, so a mutating tool can never select every relationship of a type.
func (r RelRef) HasPredicate() bool {
	return r.ID != "" || len(r.Match) > 0 || len(r.Filters) > 0 ||
		(r.From != nil && (r.From.HasPredicate())) || (r.To != nil && r.To.HasPredicate())
}

// matchClause renders "MATCH (src)-[r:`T`]->(dst) WHERE ...". r, src and dst
// are bound for the caller.
func (r RelRef) matchClause(b *graphutil.Builder) (string, error) {
	from, to := graphutil.NodeRef{}, graphutil.NodeRef{}
	if r.From != nil {
		from = *r.From
	}
	if r.To != nil {
		to = *r.To
	}
	srcPattern, conds, err := from.Pattern("src", b)
	if err != nil {
		return "", fmt.Errorf("from: %w", err)
	}
	dstPattern, dstConds, err := to.Pattern("dst", b)
	if err != nil {
		return "", fmt.Errorf("to: %w", err)
	}
	conds = append(conds, dstConds...)

	typeExpr := ""
	if r.Type != "" {
		typeExpr = ":" + graphutil.QuoteIdentifier(r.Type)
	}
	if r.ID != "" {
		conds = append(conds, fmt.Sprintf("elementId(r) = %s", b.Param(r.ID)))
	}
	m, err := graphutil.MatchConditions("r", r.Match, b)
	if err != nil {
		return "", err
	}
	f, err := graphutil.FilterConditions("r", r.Filters, b)
	if err != nil {
		return "", err
	}
	conds = append(append(conds, m...), f...)
	return fmt.Sprintf("MATCH %s-[r%s]->%s%s", srcPattern, typeExpr, dstPattern, graphutil.Where(conds)), nil
}

const relRefDoc = `Selects relationships, always in their stored direction from → to.`

// relRefOptions declares the flat properties of a RelRef.
func relRefOptions() []mcpsdk.ToolOption {
	return []mcpsdk.ToolOption{
		mcpsdk.WithString("id", mcpsdk.Description("elementId of a relationship (as returned by any graph tool). The most precise way to select one.")),
		mcpsdk.WithString("type", mcpsdk.Description("Relationship type, e.g. \"KNOWS\".")),
		mcpsdk.WithObject("from", mcpsdk.Description("Selects the start node of the relationship. "+graphutil.NodeRefDoc)),
		mcpsdk.WithObject("to", mcpsdk.Description("Selects the end node of the relationship. "+graphutil.NodeRefDoc)),
		mcpsdk.WithObject("match", mcpsdk.Description(`Exact-equality relationship property map, e.g. {"since": 2020}.`)),
		mcpsdk.WithArray("filters", "object", mcpsdk.Description("Predicates on relationship properties. "+graphutil.FiltersDoc)),
	}
}
