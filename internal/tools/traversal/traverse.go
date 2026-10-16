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
	traverseDefaultDepth = 3
	traverseMaxDepth     = 10
	traverseDefaultLimit = 50
	traverseMaxLimit     = 500
)

type TraverseInput struct {
	Start             graphutil.NodeRef  `json:"start"`
	RelationshipTypes []string           `json:"relationshipTypes,omitempty"`
	Direction         string             `json:"direction,omitempty"`
	MinDepth          int                `json:"minDepth,omitempty"`
	MaxDepth          int                `json:"maxDepth,omitempty"`
	Target            *graphutil.NodeRef `json:"target,omitempty"`
	ReturnMode        string             `json:"returnMode,omitempty"`
	NodeProperties    []string           `json:"nodeProperties,omitempty"`
	Limit             int                `json:"limit,omitempty"`
}

func TraverseSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("traverse",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Walk outward from a start node along relationships up to maxDepth hops (default %d, max %d). Read-only.
		returnMode "nodes" (default) answers "what is reachable from here?": every node reachable within the depth limit, each once, with the shortest depth at which it was reached — use it for dependencies, ancestors/descendants, hierarchies and impact analysis (e.g. OUTGOING over DEPENDS_ON). returnMode "paths" returns the actual routes, which can be many; use it only when you need to see how nodes connect.
		target narrows the result to reachable nodes with certain labels/properties. For a single hop use expand-neighbors; for routes between two known nodes use find-shortest-path or find-all-paths.
		Name relationshipTypes whenever you can: following every type fans out quickly.`, traverseDefaultDepth, traverseMaxDepth)),
		mcpsdk.WithObject("start", mcpsdk.Required(), mcpsdk.Description("Where to start. "+graphutil.NodeRefDoc)),
		relTypesOption(),
		directionOption(graphutil.DirectionOutgoing),
		mcpsdk.WithInteger("minDepth", mcpsdk.Description("Minimum hops from the start (>= 1). Default 1.")),
		mcpsdk.WithInteger("maxDepth", mcpsdk.Description(fmt.Sprintf("Maximum hops from the start (1-%d). Default %d.", traverseMaxDepth, traverseDefaultDepth))),
		mcpsdk.WithObject("target", mcpsdk.Description("Optional: only return reachable nodes that match this. "+graphutil.NodeRefDoc)),
		mcpsdk.WithString("returnMode", mcpsdk.Enum("nodes", "paths"), mcpsdk.Description(`"nodes" (default) returns each reachable node once with its depth; "paths" returns every route.`)),
		nodePropertiesOption(),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum rows to return (1-%d). Default %d.", traverseMaxLimit, traverseDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Traverse"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func TraverseHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "traverse", buildTraverse)
}

func buildTraverse(in *TraverseInput) (graphutil.Query, error) {
	if err := requireNode("start", in.Start); err != nil {
		return graphutil.Query{}, err
	}
	direction, err := resolveDirection(in.Direction, graphutil.DirectionOutgoing)
	if err != nil {
		return graphutil.Query{}, err
	}
	minDepth, maxDepth, err := resolveDepths(in.MinDepth, in.MaxDepth, traverseDefaultDepth, traverseMaxDepth)
	if err != nil {
		return graphutil.Query{}, err
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, traverseDefaultLimit, 1, traverseMaxLimit)
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
	start, err := in.Start.Inline("s", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("start: %w", err)
	}
	target := "(t)"
	if in.Target != nil {
		if target, err = in.Target.Inline("t", b); err != nil {
			return graphutil.Query{}, fmt.Errorf("target: %w", err)
		}
	}

	var cypherText string
	switch in.ReturnMode {
	case "", "nodes":
		cypherText = fmt.Sprintf(`MATCH p = ANY SHORTEST %s%s%s
RETURN elementId(s) AS startId, %s AS node, length(p) AS depth
ORDER BY depth
LIMIT %s`, start, rel, target, graphutil.NodeSummary("t", in.NodeProperties), b.Param(limit))
	case "paths":
		cypherText = fmt.Sprintf(`MATCH p = %s%s%s
RETURN %s AS path
ORDER BY length(p)
LIMIT %s`, start, rel, target, graphutil.PathMap("p", in.NodeProperties), b.Param(limit))
	default:
		return graphutil.Query{}, fmt.Errorf(`returnMode must be "nodes" or "paths" (got %q)`, in.ReturnMode)
	}
	return graphutil.Query{Cypher: cypherText, Params: b.Params()}, nil
}
