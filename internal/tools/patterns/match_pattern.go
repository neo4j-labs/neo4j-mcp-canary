// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package patterns

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	matchDefaultLimit = 25
	matchMaxLimit     = 200
	maxPatternNodes   = 6
	maxPatternRels    = 6
)

// PatternNode is one named node in a match-pattern pattern.
type PatternNode struct {
	Alias string `json:"alias"`
	graphutil.NodeRef
}

// PatternRel is one relationship in a match-pattern pattern, connecting two
// PatternNode aliases. It matches in its stored direction From → To unless
// Undirected is set.
type PatternRel struct {
	From       string             `json:"from"`
	To         string             `json:"to"`
	Types      []string           `json:"types,omitempty"`
	Undirected bool               `json:"undirected,omitempty"`
	Match      cypher.Params      `json:"match,omitempty"`
	Filters    []graphutil.Filter `json:"filters,omitempty"`
}

type MatchPatternInput struct {
	Nodes            []PatternNode `json:"nodes"`
	Relationships    []PatternRel  `json:"relationships,omitempty"`
	Mode             string        `json:"mode,omitempty"`
	ReturnProperties []string      `json:"returnProperties,omitempty"`
	AllowSameNode    bool          `json:"allowSameNode,omitempty"`
	Limit            int           `json:"limit,omitempty"`
}

func MatchPatternSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("match-pattern",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Find every place in the graph where a described pattern occurs — without writing Cypher. Read-only.
		Describe the pattern as named nodes plus the relationships between them, e.g. nodes [{"alias":"p","labels":["Person"],"match":{"name":"Alice"}}, {"alias":"c","labels":["Company"]}] and relationships [{"from":"p","to":"c","types":["WORKS_AT"]}].
		mode "rows" (default) returns each match as {nodes: {alias: node}, relationships: [...]}; mode "count" returns just how many matches there are; mode "exists" returns just whether at least one exists — prefer these two when you do not need the matches themselves.
		The pattern must be connected (every node linked to the others by relationships) and has at most %d nodes and %d relationships. Different aliases match different nodes unless allowSameNode=true. Use get-schema first to see valid labels and relationship types.`, maxPatternNodes, maxPatternRels)),
		mcpsdk.WithArray("nodes", "object", mcpsdk.Required(), mcpsdk.Description(`The nodes in the pattern. Each is {"alias": unique name used to refer to it, plus optional "id", "labels", "match", "filters" — `+graphutil.NodeRefDoc+`}.`)),
		mcpsdk.WithArray("relationships", "object", mcpsdk.Description(`The relationships in the pattern. Each is {"from": alias, "to": alias, "types": [optional relationship types], "undirected": optional bool (default false: match the stored direction from → to), "match": optional property map, "filters": optional property filters}.`)),
		mcpsdk.WithString("mode", mcpsdk.Enum("rows", "count", "exists"), mcpsdk.Description(`"rows" (default) returns the matches, "count" returns the number of matches, "exists" returns true/false.`)),
		mcpsdk.WithArray("returnProperties", "string", mcpsdk.Description("Optional property names to return on every matched node instead of all of them.")),
		mcpsdk.WithBoolean("allowSameNode", mcpsdk.Description("When true, two different aliases may match the same node. Default false.")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum matches to return in rows mode (1-%d). Default %d.", matchMaxLimit, matchDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Match Pattern"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func MatchPatternHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "match-pattern", buildMatchPattern)
}

func buildMatchPattern(in *MatchPatternInput) (graphutil.Query, error) {
	if len(in.Nodes) == 0 {
		return graphutil.Query{}, fmt.Errorf("nodes is required: describe at least one node, e.g. [{\"alias\": \"p\", \"labels\": [\"Person\"]}]")
	}
	if len(in.Nodes) > maxPatternNodes || len(in.Relationships) > maxPatternRels {
		return graphutil.Query{}, fmt.Errorf("pattern is too large: at most %d nodes and %d relationships are allowed (got %d and %d). Split it into smaller patterns", maxPatternNodes, maxPatternRels, len(in.Nodes), len(in.Relationships))
	}
	aliases := make(map[string]int, len(in.Nodes))
	for i, n := range in.Nodes {
		if strings.TrimSpace(n.Alias) == "" {
			return graphutil.Query{}, fmt.Errorf("nodes[%d].alias is required: give every node a short unique name such as \"a\"", i)
		}
		if _, dup := aliases[n.Alias]; dup {
			return graphutil.Query{}, fmt.Errorf("alias %q is used more than once; every node needs its own alias", n.Alias)
		}
		aliases[n.Alias] = i
	}
	if err := checkConnected(in, aliases); err != nil {
		return graphutil.Query{}, err
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, matchDefaultLimit, 1, matchMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	var conds []string
	for _, n := range in.Nodes {
		v := graphutil.QuoteIdentifier(n.Alias)
		labels, err := graphutil.LabelExpr(n.Labels)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("node %q: %w", n.Alias, err)
		}
		if labels != "" {
			conds = append(conds, v+labels)
		}
		c, err := n.Conditions(v, b)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("node %q: %w", n.Alias, err)
		}
		conds = append(conds, c...)
	}
	if !in.AllowSameNode {
		for i := 0; i < len(in.Nodes); i++ {
			for j := i + 1; j < len(in.Nodes); j++ {
				conds = append(conds, fmt.Sprintf("%s <> %s", graphutil.QuoteIdentifier(in.Nodes[i].Alias), graphutil.QuoteIdentifier(in.Nodes[j].Alias)))
			}
		}
	}

	var patterns, relVars []string
	for i, r := range in.Relationships {
		relVar := fmt.Sprintf("r%d", i)
		relVars = append(relVars, relVar)
		typeExpr, err := graphutil.TypeExpr(r.Types)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("relationships[%d]: %w", i, err)
		}
		direction := graphutil.DirectionOutgoing
		if r.Undirected {
			direction = graphutil.DirectionBoth
		}
		rel, err := graphutil.SingleRel(direction, relVar, typeExpr)
		if err != nil {
			return graphutil.Query{}, err
		}
		patterns = append(patterns, fmt.Sprintf("(%s)%s(%s)", graphutil.QuoteIdentifier(r.From), rel, graphutil.QuoteIdentifier(r.To)))
		m, err := graphutil.MatchConditions(relVar, r.Match, b)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("relationships[%d]: %w", i, err)
		}
		f, err := graphutil.FilterConditions(relVar, r.Filters, b)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("relationships[%d]: %w", i, err)
		}
		conds = append(append(conds, m...), f...)
	}
	if len(patterns) == 0 {
		patterns = append(patterns, fmt.Sprintf("(%s)", graphutil.QuoteIdentifier(in.Nodes[0].Alias)))
	}
	match := "MATCH " + strings.Join(patterns, ", ") + graphutil.Where(conds)

	switch in.Mode {
	case "", "rows":
		entries := make([]string, len(in.Nodes))
		for i, n := range in.Nodes {
			entries[i] = fmt.Sprintf("%s: %s", graphutil.QuoteIdentifier(n.Alias), graphutil.NodeMap(graphutil.QuoteIdentifier(n.Alias), in.ReturnProperties))
		}
		rels := make([]string, len(relVars))
		for i, v := range relVars {
			rels[i] = graphutil.RelSummary(v)
		}
		return graphutil.Query{
			Cypher: fmt.Sprintf("%s\nRETURN {%s} AS nodes, [%s] AS relationships\nLIMIT %s", match, strings.Join(entries, ", "), strings.Join(rels, ", "), b.Param(limit)),
			Params: b.Params(),
		}, nil
	case "count":
		return graphutil.Query{Cypher: match + "\nRETURN count(*) AS count", Params: b.Params()}, nil
	case "exists":
		return graphutil.Query{Cypher: fmt.Sprintf("RETURN EXISTS {\n  %s\n} AS exists", strings.ReplaceAll(match, "\n", "\n  ")), Params: b.Params()}, nil
	}
	return graphutil.Query{}, fmt.Errorf(`mode must be "rows", "count" or "exists" (got %q)`, in.Mode)
}

// checkConnected rejects patterns whose nodes are not all linked by
// relationships — a disconnected pattern is a Cartesian product, which is
// both expensive and almost never what the caller meant.
func checkConnected(in *MatchPatternInput, aliases map[string]int) error {
	parent := make([]int, len(in.Nodes))
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	valid := func() string {
		names := make([]string, 0, len(aliases))
		for a := range aliases {
			names = append(names, fmt.Sprintf("%q", a))
		}
		sort.Strings(names)
		return strings.Join(names, ", ")
	}
	for i, r := range in.Relationships {
		from, okFrom := aliases[r.From]
		to, okTo := aliases[r.To]
		if !okFrom || !okTo {
			bad := r.From
			if okFrom {
				bad = r.To
			}
			return fmt.Errorf("relationships[%d] refers to alias %q which is not defined in nodes; the defined aliases are %s", i, bad, valid())
		}
		parent[find(from)] = find(to)
	}
	root := find(0)
	for i, n := range in.Nodes {
		if find(i) != root {
			return fmt.Errorf("pattern is not connected: node %q has no chain of relationships to node %q. Add a relationship that links them, or make separate match-pattern calls", n.Alias, in.Nodes[0].Alias)
		}
	}
	return nil
}
