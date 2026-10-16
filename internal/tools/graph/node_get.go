// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const getNodeMaxRows = 5

type GetNodeInput struct {
	graphutil.NodeRef
	ReturnProperties []string `json:"returnProperties,omitempty"`
}

func GetNodeSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(`
		Get one specific node by its id (or by a unique property value) and return it as {id, labels, properties} together with relationshipCounts: how many relationships of each type it has in each direction. Read-only.
		Use the relationshipCounts to decide where to go next with expand-neighbors or traverse.
		Selection needs an id, match or filters. If more than one node matches, up to 5 are returned — that means the key was not unique, so call again with the id of the one you want.`),
	}
	opts = append(opts, graphutil.NodeRefOptions("Optional labels the node must all have; narrows a match but cannot select a node on its own.")...)
	opts = append(opts,
		mcpsdk.WithArray("returnProperties", "string", mcpsdk.Description("Optional property names to return instead of all properties.")),
		mcpsdk.WithTitleAnnotation("Get Node"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("get-node", opts...)
}

func GetNodeHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "get-node", buildGetNode)
}

func buildGetNode(in *GetNodeInput) (graphutil.Query, error) {
	if !in.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("provide id, match or filters to say which node to get; use find-nodes to browse nodes by label")
	}
	b := graphutil.NewBuilder()
	match, err := nodeMatch(in.NodeRef, "n", b)
	if err != nil {
		return graphutil.Query{}, err
	}
	return graphutil.Query{
		Cypher: fmt.Sprintf(`%s
WITH n LIMIT %d
CALL (n) {
  MATCH (n)-[r]-()
  WITH type(r) AS type, CASE WHEN startNode(r) = n THEN 'OUTGOING' ELSE 'INCOMING' END AS direction, count(*) AS count
  RETURN collect({type: type, direction: direction, count: count}) AS relationshipCounts
}
RETURN %s AS node, relationshipCounts`, match, getNodeMaxRows, graphutil.NodeMap("n", in.ReturnProperties)),
		Params: b.Params(),
	}, nil
}
