// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package traversal

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const directionDoc = "OUTGOING follows relationships in their stored direction, INCOMING follows them backwards, BOTH ignores direction. If a search unexpectedly finds nothing, try BOTH."

func directionOption(def string) mcpsdk.ToolOption {
	return mcpsdk.WithString("direction",
		mcpsdk.Enum(graphutil.DirectionOutgoing, graphutil.DirectionIncoming, graphutil.DirectionBoth),
		mcpsdk.Description(fmt.Sprintf("Which way to follow relationships. Default %s. %s", def, directionDoc)),
	)
}

func relTypesOption() mcpsdk.ToolOption {
	return mcpsdk.WithArray("relationshipTypes", "string",
		mcpsdk.Description("Optional relationship types to follow, e.g. [\"DEPENDS_ON\"]. Omit to follow every type — name the types where you can, it is much faster and gives more relevant results."),
	)
}

func nodePropertiesOption() mcpsdk.ToolOption {
	return mcpsdk.WithArray("nodeProperties", "string",
		mcpsdk.Description("Optional node property names (e.g. \"name\") to include on returned nodes, which otherwise carry only id and labels."),
	)
}

// resolveDirection applies the tool's default and validates the value.
func resolveDirection(direction, def string) (string, error) {
	if direction == "" {
		return def, nil
	}
	switch direction {
	case graphutil.DirectionOutgoing, graphutil.DirectionIncoming, graphutil.DirectionBoth:
		return direction, nil
	}
	return "", fmt.Errorf("direction must be OUTGOING, INCOMING or BOTH (got %q)", direction)
}

// resolveDepths validates minDepth/maxDepth, applying defaults and the
// tool's hard cap so no traversal is unbounded.
func resolveDepths(minDepth, maxDepth, defMax, capMax int) (int, int, error) {
	maxD, err := graphutil.IntInRange("maxDepth", maxDepth, defMax, 1, capMax)
	if err != nil {
		return 0, 0, err
	}
	minD, err := graphutil.IntInRange("minDepth", minDepth, 1, 1, maxD)
	if err != nil {
		return 0, 0, fmt.Errorf("%w (minDepth cannot exceed maxDepth, which is %d)", err, maxD)
	}
	return minD, maxD, nil
}

func requireNode(field string, ref graphutil.NodeRef) error {
	if !ref.HasPredicate() {
		return fmt.Errorf("%s must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id", field)
	}
	return nil
}
