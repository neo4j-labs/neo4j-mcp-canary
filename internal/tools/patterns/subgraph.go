// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package patterns

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	subgraphDefaultDepth = 2
	subgraphMaxDepth     = 4
	subgraphDefaultNodes = 50
	subgraphMaxNodes     = 200
)

type FindSubgraphInput struct {
	Start             graphutil.NodeRef `json:"start"`
	RelationshipTypes []string          `json:"relationshipTypes,omitempty"`
	Direction         string            `json:"direction,omitempty"`
	MaxDepth          int               `json:"maxDepth,omitempty"`
	MaxNodes          int               `json:"maxNodes,omitempty"`
	NodeProperties    []string          `json:"nodeProperties,omitempty"`
}

func FindSubgraphSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-subgraph",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Extract the whole neighborhood around a node as one self-contained subgraph: every node within maxDepth hops (default %d, max %d) plus every relationship between those nodes, returned as a single row {nodes, relationships, nodeCount, truncated}. Read-only.
		Use it when you need to see how a cluster fits together (for example to answer "what is around this entity?" or to summarize a component) rather than a list of paths. nodes carry {id, labels}, relationships {id, type, from, to}, so the result can be read as a graph directly.
		The subgraph is capped at maxNodes (default %d, max %d); truncated=true means there was more — lower maxDepth or name relationshipTypes. For just one hop with relationship details use expand-neighbors.`, subgraphDefaultDepth, subgraphMaxDepth, subgraphDefaultNodes, subgraphMaxNodes)),
		mcpsdk.WithObject("start", mcpsdk.Required(), mcpsdk.Description("The node at the center. If it selects several nodes, they are all included. "+graphutil.NodeRefDoc)),
		mcpsdk.WithArray("relationshipTypes", "string", mcpsdk.Description("Optional relationship types to include. Omit for every type.")),
		mcpsdk.WithString("direction", mcpsdk.Enum(graphutil.DirectionOutgoing, graphutil.DirectionIncoming, graphutil.DirectionBoth), mcpsdk.Description("Which way to expand from the start. Default BOTH.")),
		mcpsdk.WithInteger("maxDepth", mcpsdk.Description(fmt.Sprintf("How many hops out to go (1-%d). Default %d.", subgraphMaxDepth, subgraphDefaultDepth))),
		mcpsdk.WithInteger("maxNodes", mcpsdk.Description(fmt.Sprintf("Maximum nodes in the subgraph (1-%d). Default %d.", subgraphMaxNodes, subgraphDefaultNodes))),
		mcpsdk.WithArray("nodeProperties", "string", mcpsdk.Description("Optional property names (e.g. \"name\") to include on returned nodes, which otherwise carry only id and labels.")),
		mcpsdk.WithTitleAnnotation("Find Subgraph"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindSubgraphHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-subgraph", buildFindSubgraph)
}

func buildFindSubgraph(in *FindSubgraphInput) (graphutil.Query, error) {
	if !in.Start.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("start must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id")
	}
	direction := in.Direction
	if direction == "" {
		direction = graphutil.DirectionBoth
	}
	maxDepth, err := graphutil.IntInRange("maxDepth", in.MaxDepth, subgraphDefaultDepth, 1, subgraphMaxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}
	maxNodes, err := graphutil.IntInRange("maxNodes", in.MaxNodes, subgraphDefaultNodes, 1, subgraphMaxNodes)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}
	reach, err := graphutil.QuantifiedRel(direction, typeExpr, 1, maxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	startPattern, conds, err := in.Start.Pattern("s", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("start: %w", err)
	}
	// Relationships among the collected nodes are matched in stored
	// direction so each is returned once, whichever way the walk went.
	edge := fmt.Sprintf("(x)-[r%s]->(y)", typeExpr)

	return graphutil.Query{
		Cypher: fmt.Sprintf(`MATCH %[1]s%[2]s
WITH collect(s) AS starts
CALL (starts) {
  UNWIND starts AS s
  MATCH ANY SHORTEST (s)%[3]s(n)
  RETURN collect(DISTINCT n) AS reached
}
CALL (starts, reached) {
  UNWIND starts + reached AS x
  WITH collect(DISTINCT x) AS everything
  RETURN everything[..%[4]s] AS ns, size(everything) > %[4]s AS truncated
}
CALL (ns) {
  UNWIND ns AS x
  MATCH %[5]s
  WHERE y IN ns
  RETURN collect(DISTINCT r) AS rels
}
RETURN [x IN ns | %[6]s] AS nodes, [r IN rels | %[7]s] AS relationships, size(ns) AS nodeCount, truncated`,
			startPattern, graphutil.Where(conds), reach, b.Param(maxNodes), edge,
			graphutil.NodeSummary("x", in.NodeProperties), graphutil.RelSummary("r")),
		Params: b.Params(),
	}, nil
}
