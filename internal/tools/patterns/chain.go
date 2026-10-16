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
	chainDefaultLimit = 25
	chainMaxLimit     = 200
	chainMaxSteps     = 6
)

// ChainStep is one hop in a relationship chain: the relationship to follow
// and, optionally, what the node it arrives at must look like.
type ChainStep struct {
	Type      string             `json:"type,omitempty"`
	Direction string             `json:"direction,omitempty"`
	Node      *graphutil.NodeRef `json:"node,omitempty"`
}

type FindRelationshipChainInput struct {
	Start          graphutil.NodeRef `json:"start"`
	Steps          []ChainStep       `json:"steps"`
	NodeProperties []string          `json:"nodeProperties,omitempty"`
	Limit          int               `json:"limit,omitempty"`
}

func FindRelationshipChainSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-relationship-chain",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Follow an exact, ordered sequence of relationships from a start node, e.g. Person -WORKS_AT-> Company -LOCATED_IN-> City, and return every route that matches as {length, nodes, relationships}. Read-only.
		Unlike traverse (which follows any path up to a depth), each step here says precisely which relationship type and direction to take, and optionally what the node reached must look like. At most %d steps. If it returns nothing, check the direction of each step, or use expand-neighbors on the start node to see what is actually connected.`, chainMaxSteps)),
		mcpsdk.WithObject("start", mcpsdk.Required(), mcpsdk.Description("Where the chain begins. "+graphutil.NodeRefDoc)),
		mcpsdk.WithArray("steps", "object", mcpsdk.Required(), mcpsdk.Description(`The ordered hops. Each is {"type": relationship type (omit for any), "direction": "OUTGOING" (default) | "INCOMING" | "BOTH", "node": optional criteria the node reached by this hop must meet — `+graphutil.NodeRefDoc+`}.`)),
		mcpsdk.WithArray("nodeProperties", "string", mcpsdk.Description("Optional property names (e.g. \"name\") to include on returned nodes, which otherwise carry only id and labels.")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum chains to return (1-%d). Default %d.", chainMaxLimit, chainDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Find Relationship Chain"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindRelationshipChainHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-relationship-chain", buildFindRelationshipChain)
}

func buildFindRelationshipChain(in *FindRelationshipChainInput) (graphutil.Query, error) {
	if !in.Start.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("start must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id")
	}
	if len(in.Steps) == 0 || len(in.Steps) > chainMaxSteps {
		return graphutil.Query{}, fmt.Errorf("steps must contain between 1 and %d hops (got %d)", chainMaxSteps, len(in.Steps))
	}
	limit, err := graphutil.IntInRange("limit", in.Limit, chainDefaultLimit, 1, chainMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}

	b := graphutil.NewBuilder()
	start, err := in.Start.Inline("n0", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("start: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(start)
	for i, step := range in.Steps {
		typeExpr := ""
		if step.Type != "" {
			typeExpr = ":" + graphutil.QuoteIdentifier(step.Type)
		}
		rel, err := graphutil.SingleRel(step.Direction, "", typeExpr)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("steps[%d]: %w", i, err)
		}
		node := graphutil.NodeRef{}
		if step.Node != nil {
			node = *step.Node
		}
		pattern, err := node.Inline(fmt.Sprintf("n%d", i+1), b)
		if err != nil {
			return graphutil.Query{}, fmt.Errorf("steps[%d].node: %w", i, err)
		}
		sb.WriteString(rel)
		sb.WriteString(pattern)
	}
	return graphutil.Query{
		Cypher: fmt.Sprintf("MATCH p = %s\nRETURN %s AS path\nLIMIT %s", sb.String(), graphutil.PathMap("p", in.NodeProperties), b.Param(limit)),
		Params: b.Params(),
	}, nil
}
