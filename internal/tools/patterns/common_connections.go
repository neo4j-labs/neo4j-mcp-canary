// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package patterns

import (
	"fmt"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	commonDefaultLimit = 25
	commonMaxLimit     = 200
	commonMaxNodes     = 5
)

type FindCommonConnectionsInput struct {
	Nodes             []graphutil.NodeRef `json:"nodes"`
	RelationshipTypes []string            `json:"relationshipTypes,omitempty"`
	Direction         string              `json:"direction,omitempty"`
	Connection        *graphutil.NodeRef  `json:"connection,omitempty"`
	NodeProperties    []string            `json:"nodeProperties,omitempty"`
	Limit             int                 `json:"limit,omitempty"`
}

func FindCommonConnectionsSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-common-connections",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Find the nodes that every one of 2-%d given nodes is directly connected to — shared neighbors. Read-only.
		Use it for collaboration and overlap questions: shared collaborators, shared dependencies, mutual friends, common customers. Each row is {connection, relationshipTypes}: the shared node and, per input node, the type of relationship that links it.
		direction is relative to the input nodes: OUTGOING finds shared targets (they all point to it), INCOMING finds shared sources (it points to all of them), BOTH ignores direction (default).
		connection narrows the shared nodes by label/properties. Use find-shortest-path instead for longer, indirect connections.`, commonMaxNodes)),
		mcpsdk.WithArray("nodes", "object", mcpsdk.Required(), mcpsdk.Description("2 to 5 nodes to compare. Each is an object that selects one node: "+graphutil.NodeRefDoc)),
		mcpsdk.WithArray("relationshipTypes", "string", mcpsdk.Description("Optional relationship types to follow. Omit for every type.")),
		mcpsdk.WithString("direction", mcpsdk.Enum(graphutil.DirectionOutgoing, graphutil.DirectionIncoming, graphutil.DirectionBoth), mcpsdk.Description("Direction relative to the input nodes. Default BOTH.")),
		mcpsdk.WithObject("connection", mcpsdk.Description("Optional: only return shared nodes that match this. "+graphutil.NodeRefDoc)),
		mcpsdk.WithArray("nodeProperties", "string", mcpsdk.Description("Optional property names (e.g. \"name\") to include on the shared nodes, which otherwise carry only id and labels.")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum shared nodes to return (1-%d). Default %d.", commonMaxLimit, commonDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Find Common Connections"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindCommonConnectionsHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-common-connections", buildFindCommonConnections)
}

func buildFindCommonConnections(in *FindCommonConnectionsInput) (graphutil.Query, error) {
	if len(in.Nodes) < 2 || len(in.Nodes) > commonMaxNodes {
		return graphutil.Query{}, fmt.Errorf("nodes must contain between 2 and %d nodes to compare (got %d)", commonMaxNodes, len(in.Nodes))
	}
	for i, n := range in.Nodes {
		if !n.HasPredicate() {
			return graphutil.Query{}, fmt.Errorf("nodes[%d] must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id", i)
		}
	}
	direction := in.Direction
	if direction == "" {
		direction = graphutil.DirectionBoth
	}
	// The relationship is written from each input node toward the shared
	// node, so OUTGOING/INCOMING carry their usual meaning.
	limit, err := graphutil.IntInRange("limit", in.Limit, commonDefaultLimit, 1, commonMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	var patterns, conds, via []string
	for i, n := range in.Nodes {
		nv, rv := fmt.Sprintf("n%d", i), fmt.Sprintf("r%d", i)
		rel, err := graphutil.SingleRel(direction, rv, typeExpr)
		if err != nil {
			return graphutil.Query{}, err
		}
		pattern, c, err := n.Pattern(nv, b)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("nodes[%d]: %w", i, err)
		}
		patterns = append(patterns, pattern+rel+"(c)")
		conds = append(conds, c...)
		conds = append(conds, fmt.Sprintf("c <> %s", nv))
		for j := 0; j < i; j++ {
			conds = append(conds, fmt.Sprintf("%s <> n%d", nv, j))
		}
		via = append(via, fmt.Sprintf("type(%s)", rv))
	}
	conn := graphutil.NodeRef{}
	if in.Connection != nil {
		conn = *in.Connection
	}
	cLabels, err := graphutil.LabelExpr(conn.Labels)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("connection: %w", err)
	}
	if cLabels != "" {
		conds = append(conds, "c"+cLabels)
	}
	cc, err := conn.Conditions("c", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("connection: %w", err)
	}
	conds = append(conds, cc...)

	return graphutil.Query{
		Cypher: fmt.Sprintf("MATCH %s%s\nRETURN DISTINCT %s AS connection, [%s] AS relationshipTypes\nLIMIT %s",
			strings.Join(patterns, ", "), graphutil.Where(conds), graphutil.NodeSummary("c", in.NodeProperties), strings.Join(via, ", "), b.Param(limit)),
		Params: b.Params(),
	}, nil
}
