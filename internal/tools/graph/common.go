// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	defaultFindLimit = 25
	maxFindLimit     = 200
	defaultMaxAffect = 1
	maxMaxAffected   = 1000
	dryRunSampleIDs  = 10
)

// nodeMatch renders "MATCH (v:`L`) WHERE ..." for a node reference.
func nodeMatch(ref graphutil.NodeRef, v string, b *graphutil.Builder) (string, error) {
	pattern, conds, err := ref.Pattern(v, b)
	if err != nil {
		return "", err
	}
	return "MATCH " + pattern + graphutil.Where(conds), nil
}

func orderClause(v, orderBy, direction string) (string, error) {
	if orderBy == "" {
		return "", nil
	}
	switch strings.ToUpper(direction) {
	case "", "ASC":
		return fmt.Sprintf("\nORDER BY %s.%s ASC", v, graphutil.QuoteIdentifier(orderBy)), nil
	case "DESC":
		return fmt.Sprintf("\nORDER BY %s.%s DESC", v, graphutil.QuoteIdentifier(orderBy)), nil
	}
	return "", fmt.Errorf("orderDirection must be ASC or DESC (got %q)", direction)
}

func pagination(b *graphutil.Builder, limit, skip int) (string, error) {
	l, err := graphutil.IntInRange("limit", limit, defaultFindLimit, 1, maxFindLimit)
	if err != nil {
		return "", err
	}
	if skip < 0 {
		return "", fmt.Errorf("skip must be 0 or greater (got %d)", skip)
	}
	out := ""
	if skip > 0 {
		out += "\nSKIP " + b.Param(skip)
	}
	return out + "\nLIMIT " + b.Param(l), nil
}

func resolveMaxAffected(n int) (int, error) {
	return graphutil.IntInRange("maxAffected", n, defaultMaxAffect, 1, maxMaxAffected)
}

// guardedMutation wraps a MATCH so the mutation only runs when the match
// selected between 1 and maxAffected entities. It always returns exactly one
// row — {matched, performed, <resultAlias>, message} — so a refused call
// tells the caller why instead of silently doing nothing: either too many
// entities matched, or none did (performed is false and message says so, so
// a wrong id is not mistaken for a successful change).
// mutation runs once per matched entity, bound to v; resultExpr is an
// aggregate over those executions. entity ("node" or "relationship") names
// what the messages talk about.
func guardedMutation(matchClause, v, mutation, resultExpr, resultAlias, entity string, maxAffected int, b *graphutil.Builder) string {
	return fmt.Sprintf(`%[1]s
WITH collect(%[2]s) AS items
WITH items, size(items) AS matched
WITH items, matched, matched <= %[3]s AS allowed
CALL (items, allowed) {
  UNWIND CASE WHEN allowed THEN items ELSE [] END AS %[2]s
  %[4]s
  RETURN %[5]s AS %[6]s
}
RETURN matched, allowed AND matched > 0 AS performed, %[6]s, CASE WHEN matched = 0 THEN %[8]s WHEN allowed THEN null ELSE %[7]s END AS message`,
		matchClause, v, b.Param(maxAffected), mutation, resultExpr, resultAlias,
		b.Param(refusalMessage(entity, maxAffected)), b.Param(noMatchMessage(entity)))
}

// dryRunQuery counts what a mutation would touch without changing anything.
// The aggregation is in its own WITH: a constant such as false next to an
// aggregate in the same RETURN acts as a grouping key, which makes an empty
// match return no row at all instead of matched: 0.
func dryRunQuery(matchClause, v string) string {
	return fmt.Sprintf(`%s
WITH count(%[2]s) AS matched, collect(elementId(%[2]s))[..%[3]d] AS sampleIds
RETURN matched, sampleIds, false AS performed,
  CASE WHEN matched = 0 THEN 'dryRun: nothing matched this selection, so a real call would change nothing' ELSE 'dryRun: nothing was changed' END AS message`,
		matchClause, v, dryRunSampleIDs)
}

func refusalMessage(entity string, maxAffected int) string {
	return fmt.Sprintf("Nothing was changed: the match selected more %s than maxAffected (%d). Narrow the match (use id for one exact %s), or raise maxAffected if you really mean to change them all; set dryRun=true to preview first.",
		entity+"s", maxAffected, entity)
}

func noMatchMessage(entity string) string {
	return fmt.Sprintf("Nothing was changed: no %[1]s matched this selection. Check the id (node ids and relationship ids are different; look the right one up with find-%[1]ss) or the labels, match and filters.", entity)
}
