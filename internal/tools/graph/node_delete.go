// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type DeleteNodeInput struct {
	graphutil.NodeRef
	Detach      bool `json:"detach,omitempty"`
	MaxAffected int  `json:"maxAffected,omitempty"`
	DryRun      bool `json:"dryRun,omitempty"`
}

func DeleteNodeSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		DESTRUCTIVE and permanent: delete nodes. Returns {matched, performed, deleted, message}.
		Select nodes with id (best), match or filters — labels alone are not enough. By default at most %d node may be deleted per call; if the selection matches more than maxAffected nothing is deleted and performed=false tells you so. Raise maxAffected deliberately (max %d) to delete several nodes.
		A node that still has relationships cannot be deleted unless detach=true, which deletes its relationships too; without it the call fails and tells you so. Always run with dryRun=true first when unsure: it reports how many nodes would be deleted, with sample ids, and deletes nothing.`, defaultMaxAffect, maxMaxAffected)),
	}
	opts = append(opts, graphutil.NodeRefOptions("Optional labels the node must all have; narrows a match but cannot select a node on its own.")...)
	opts = append(opts,
		mcpsdk.WithBoolean("detach", mcpsdk.Description("When true, also delete the nodes' relationships. Default false: deleting a node that still has relationships fails.")),
		mcpsdk.WithInteger("maxAffected", mcpsdk.Description(fmt.Sprintf("Maximum number of nodes this call may delete (1-%d). Default %d.", maxMaxAffected, defaultMaxAffect))),
		mcpsdk.WithBoolean("dryRun", mcpsdk.Description("When true, only report how many nodes would be deleted; delete nothing.")),
		mcpsdk.WithTitleAnnotation("Delete Node"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("delete-node", opts...)
}

func DeleteNodeHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "delete-node", buildDeleteNode)
}

func buildDeleteNode(in *DeleteNodeInput) (graphutil.Query, error) {
	if !in.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("provide id, match or filters to say which nodes to delete; labels alone would select every node with that label. Use find-nodes to look up an id")
	}
	maxAffected, err := resolveMaxAffected(in.MaxAffected)
	if err != nil {
		return graphutil.Query{}, err
	}
	b := graphutil.NewBuilder()
	match, err := nodeMatch(in.NodeRef, "n", b)
	if err != nil {
		return graphutil.Query{}, err
	}
	if in.DryRun {
		return graphutil.Query{Cypher: dryRunQuery(match, "n"), Params: b.Params()}, nil
	}
	del := "DELETE n"
	if in.Detach {
		del = "DETACH DELETE n"
	}
	return graphutil.Query{
		Cypher:    guardedMutation(match, "n", del, "count(*)", "deleted", refusalMessage("node", maxAffected), maxAffected, b),
		Params:    b.Params(),
		Write:     true,
		ErrorHint: "If the node still has relationships, retry with detach=true to delete them too, or remove them first with delete-relationship",
	}, nil
}
