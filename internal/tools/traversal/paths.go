// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package traversal

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	shortestDefaultDepth = 6
	shortestMaxDepth     = 15
	shortestDefaultLimit = 5
	shortestMaxLimit     = 50

	allPathsDefaultDepth = 4
	allPathsMaxDepth     = 8
	allPathsDefaultLimit = 20
	allPathsMaxLimit     = 100
)

type FindShortestPathInput struct {
	From              graphutil.NodeRef `json:"from"`
	To                graphutil.NodeRef `json:"to"`
	RelationshipTypes []string          `json:"relationshipTypes,omitempty"`
	Direction         string            `json:"direction,omitempty"`
	MaxDepth          int               `json:"maxDepth,omitempty"`
	AllShortest       bool              `json:"allShortest,omitempty"`
	NodeProperties    []string          `json:"nodeProperties,omitempty"`
	Limit             int               `json:"limit,omitempty"`
}

func FindShortestPathSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-shortest-path",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Find the shortest route between two nodes and return it as {length, nodes, relationships}. Read-only. Nodes carry only id and labels; pass nodeProperties (e.g. ["name"]) to include the properties you need to read.
		Returns one shortest path by default; set allShortest=true to return every path of that same minimum length (alternatives). If no row comes back, there is no connection within maxDepth (default %d, max %d) — try direction BOTH, a larger maxDepth, or more relationship types.
		To enumerate longer alternative routes use find-all-paths; to see everything reachable from one node use traverse.`, shortestDefaultDepth, shortestMaxDepth)),
		mcpsdk.WithObject("from", mcpsdk.Required(), mcpsdk.Description("The start node. "+graphutil.NodeRefDoc)),
		mcpsdk.WithObject("to", mcpsdk.Required(), mcpsdk.Description("The end node. "+graphutil.NodeRefDoc)),
		relTypesOption(),
		directionOption(graphutil.DirectionBoth),
		mcpsdk.WithInteger("maxDepth", mcpsdk.Description(fmt.Sprintf("Maximum hops (1-%d). Default %d.", shortestMaxDepth, shortestDefaultDepth))),
		mcpsdk.WithBoolean("allShortest", mcpsdk.Description("When true, return all paths that tie for the shortest length instead of just one. Default false.")),
		nodePropertiesOption(),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum paths to return (1-%d). Default %d.", shortestMaxLimit, shortestDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Find Shortest Path"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindShortestPathHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-shortest-path", buildFindShortestPath)
}

func buildFindShortestPath(in *FindShortestPathInput) (graphutil.Query, error) {
	if err := requireNode("from", in.From); err != nil {
		return graphutil.Query{}, err
	}
	if err := requireNode("to", in.To); err != nil {
		return graphutil.Query{}, err
	}
	direction, err := resolveDirection(in.Direction, graphutil.DirectionBoth)
	if err != nil {
		return graphutil.Query{}, err
	}
	_, maxDepth, err := resolveDepths(1, in.MaxDepth, shortestDefaultDepth, shortestMaxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, shortestDefaultLimit, 1, shortestMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}
	rel, err := graphutil.QuantifiedRel(direction, typeExpr, 1, maxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	from, err := in.From.Inline("a", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("from: %w", err)
	}
	to, err := in.To.Inline("z", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("to: %w", err)
	}
	selector := "ANY SHORTEST"
	if in.AllShortest {
		selector = "ALL SHORTEST"
	}
	return graphutil.Query{
		Cypher: fmt.Sprintf("MATCH p = %s %s%s%s\nWHERE a <> z\nRETURN %s AS path\nLIMIT %s",
			selector, from, rel, to, graphutil.PathMap("p", in.NodeProperties), b.Param(limit)),
		Params: b.Params(),
	}, nil
}

type FindAllPathsInput struct {
	From              graphutil.NodeRef `json:"from"`
	To                graphutil.NodeRef `json:"to"`
	RelationshipTypes []string          `json:"relationshipTypes,omitempty"`
	Direction         string            `json:"direction,omitempty"`
	MinDepth          int               `json:"minDepth,omitempty"`
	MaxDepth          int               `json:"maxDepth,omitempty"`
	NodeProperties    []string          `json:"nodeProperties,omitempty"`
	Limit             int               `json:"limit,omitempty"`
}

func FindAllPathsSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-all-paths",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Enumerate the distinct routes between two nodes, shortest first, each as {length, nodes, relationships}. Read-only. Nodes carry only id and labels; pass nodeProperties (e.g. ["name"]) to include the properties you need to read.
		The number of routes grows very quickly with depth, so this is bounded: maxDepth defaults to %d (max %d) and at most %d paths are returned (default %d). Start with a small maxDepth and name relationshipTypes. If you only need the quickest connection use find-shortest-path instead.`, allPathsDefaultDepth, allPathsMaxDepth, allPathsMaxLimit, allPathsDefaultLimit)),
		mcpsdk.WithObject("from", mcpsdk.Required(), mcpsdk.Description("The start node. "+graphutil.NodeRefDoc)),
		mcpsdk.WithObject("to", mcpsdk.Required(), mcpsdk.Description("The end node. "+graphutil.NodeRefDoc)),
		relTypesOption(),
		directionOption(graphutil.DirectionBoth),
		mcpsdk.WithInteger("minDepth", mcpsdk.Description("Minimum hops (>= 1). Default 1.")),
		mcpsdk.WithInteger("maxDepth", mcpsdk.Description(fmt.Sprintf("Maximum hops (1-%d). Default %d.", allPathsMaxDepth, allPathsDefaultDepth))),
		nodePropertiesOption(),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum paths to return (1-%d). Default %d.", allPathsMaxLimit, allPathsDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Find All Paths"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindAllPathsHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-all-paths", buildFindAllPaths)
}

func buildFindAllPaths(in *FindAllPathsInput) (graphutil.Query, error) {
	if err := requireNode("from", in.From); err != nil {
		return graphutil.Query{}, err
	}
	if err := requireNode("to", in.To); err != nil {
		return graphutil.Query{}, err
	}
	direction, err := resolveDirection(in.Direction, graphutil.DirectionBoth)
	if err != nil {
		return graphutil.Query{}, err
	}
	minDepth, maxDepth, err := resolveDepths(in.MinDepth, in.MaxDepth, allPathsDefaultDepth, allPathsMaxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, allPathsDefaultLimit, 1, allPathsMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}
	rel, err := graphutil.QuantifiedRel(direction, typeExpr, minDepth, maxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	from, err := in.From.Inline("a", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("from: %w", err)
	}
	to, err := in.To.Inline("z", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("to: %w", err)
	}
	return graphutil.Query{
		Cypher: fmt.Sprintf("MATCH p = %s%s%s\nWHERE a <> z\nRETURN %s AS path\nORDER BY length(p)\nLIMIT %s",
			from, rel, to, graphutil.PathMap("p", in.NodeProperties), b.Param(limit)),
		Params: b.Params(),
	}, nil
}
