// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type CountNodesInput struct {
	graphutil.NodeRef
}

func CountNodesSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(`
		Count the nodes that match the given labels, exact property values and/or filters, and return {count}. Read-only.
		Use this instead of find-nodes when you only need a number, for example to check how many nodes an update-node or delete-node call would affect. With no criteria it counts every node in the database.`),
	}
	opts = append(opts, graphutil.NodeRefOptions("Optional labels the node must all have, e.g. [\"Person\"].")...)
	opts = append(opts,
		mcpsdk.WithTitleAnnotation("Count Nodes"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("count-nodes", opts...)
}

func CountNodesHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "count-nodes", buildCountNodes)
}

func buildCountNodes(in *CountNodesInput) (graphutil.Query, error) {
	b := graphutil.NewBuilder()
	match, err := nodeMatch(in.NodeRef, "n", b)
	if err != nil {
		return graphutil.Query{}, err
	}
	return graphutil.Query{Cypher: match + "\nRETURN count(n) AS count", Params: b.Params()}, nil
}
