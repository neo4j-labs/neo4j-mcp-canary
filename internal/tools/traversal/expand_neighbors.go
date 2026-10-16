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
	expandDefaultLimit = 25
	expandMaxLimit     = 200
)

type ExpandNeighborsInput struct {
	Start                  graphutil.NodeRef  `json:"start"`
	RelationshipTypes      []string           `json:"relationshipTypes,omitempty"`
	Direction              string             `json:"direction,omitempty"`
	Neighbor               *graphutil.NodeRef `json:"neighbor,omitempty"`
	NodeProperties         []string           `json:"nodeProperties,omitempty"`
	RelationshipProperties []string           `json:"relationshipProperties,omitempty"`
	Limit                  int                `json:"limit,omitempty"`
}

func ExpandNeighborsSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("expand-neighbors",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Show everything exactly one hop away from a node: each row is one connection {startId, relationship: {id, type, direction}, neighbor: {id, labels, ...}}. Read-only.
		direction is relative to the start node: OUTGOING means the start node is the relationship's source. Use this to see what a node is connected to; use get-node first if you only need counts per relationship type. For more than one hop use traverse; for the whole surrounding subgraph use find-subgraph.
		neighbor narrows to neighbors with certain labels/properties. At most %d rows per call (default %d).`, expandMaxLimit, expandDefaultLimit)),
		mcpsdk.WithObject("start", mcpsdk.Required(), mcpsdk.Description("The node to expand. "+graphutil.NodeRefDoc)),
		relTypesOption(),
		directionOption(graphutil.DirectionBoth),
		mcpsdk.WithObject("neighbor", mcpsdk.Description("Optional: only return neighbors that match this. "+graphutil.NodeRefDoc)),
		nodePropertiesOption(),
		mcpsdk.WithArray("relationshipProperties", "string", mcpsdk.Description("Optional relationship property names to include on each relationship (none by default).")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum connections to return (1-%d). Default %d.", expandMaxLimit, expandDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Expand Neighbors"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func ExpandNeighborsHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "expand-neighbors", buildExpandNeighbors)
}

func buildExpandNeighbors(in *ExpandNeighborsInput) (graphutil.Query, error) {
	if err := requireNode("start", in.Start); err != nil {
		return graphutil.Query{}, err
	}
	direction, err := resolveDirection(in.Direction, graphutil.DirectionBoth)
	if err != nil {
		return graphutil.Query{}, err
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, expandDefaultLimit, 1, expandMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}
	rel, err := graphutil.SingleRel(direction, "r", typeExpr)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	startPattern, conds, err := in.Start.Pattern("s", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("start: %w", err)
	}
	neighbor := graphutil.NodeRef{}
	if in.Neighbor != nil {
		neighbor = *in.Neighbor
	}
	neighborPattern, neighborConds, err := neighbor.Pattern("n", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("neighbor: %w", err)
	}
	conds = append(conds, neighborConds...)

	relExpr := "{id: elementId(r), type: type(r), direction: CASE WHEN startNode(r) = s THEN 'OUTGOING' ELSE 'INCOMING' END"
	if len(in.RelationshipProperties) > 0 {
		relExpr += ", properties: " + graphutil.PropsExpr("r", in.RelationshipProperties)
	}
	relExpr += "}"
	return graphutil.Query{
		Cypher: fmt.Sprintf("MATCH %s%s%s%s\nRETURN elementId(s) AS startId, %s AS relationship, %s AS neighbor\nLIMIT %s",
			startPattern, rel, neighborPattern, graphutil.Where(conds), relExpr, graphutil.NodeSummary("n", in.NodeProperties), b.Param(limit)),
		Params: b.Params(),
	}, nil
}
