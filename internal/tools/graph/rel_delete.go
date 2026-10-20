// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type DeleteRelationshipInput struct {
	RelRef
	MaxAffected int  `json:"maxAffected,omitempty"`
	DryRun      bool `json:"dryRun,omitempty"`
}

func DeleteRelationshipSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		DESTRUCTIVE and permanent: delete relationships (the nodes at either end are kept). Returns {matched, performed, deleted, message}.
		%s Select with id (best), match, filters, or a from/to node that has id/match/filters — a type alone is not enough. By default at most %d relationship may be deleted per call; if the selection matches more than maxAffected nothing is deleted and performed=false tells you so. Raise maxAffected deliberately (max %d) to delete several.
		Always run with dryRun=true first when unsure: it reports how many relationships would be deleted, with sample ids, and deletes nothing.`, relRefDoc, defaultMaxAffect, maxMaxAffected)),
	}
	opts = append(opts, relRefOptions()...)
	opts = append(opts,
		mcpsdk.WithInteger("maxAffected", mcpsdk.Description(fmt.Sprintf("Maximum number of relationships this call may delete (1-%d). Default %d.", maxMaxAffected, defaultMaxAffect))),
		mcpsdk.WithBoolean("dryRun", mcpsdk.Description("When true, only report how many relationships would be deleted; delete nothing.")),
		mcpsdk.WithTitleAnnotation("Delete Relationship"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("delete-relationship", opts...)
}

func DeleteRelationshipHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "delete-relationship", buildDeleteRelationship)
}

func buildDeleteRelationship(in *DeleteRelationshipInput) (graphutil.Query, error) {
	if !in.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("provide id, match, filters, or a from/to node with id/match/filters to say which relationships to delete; a type alone would select every relationship of that type. Use find-relationships to look up an id")
	}
	maxAffected, err := resolveMaxAffected(in.MaxAffected)
	if err != nil {
		return graphutil.Query{}, err
	}
	b := graphutil.NewBuilder()
	match, err := in.matchClause(b)
	if err != nil {
		return graphutil.Query{}, err
	}
	if in.DryRun {
		return graphutil.Query{Cypher: dryRunQuery(match, "r"), Params: b.Params()}, nil
	}
	return graphutil.Query{
		Cypher: guardedMutation(match, "r", "DELETE r", "count(*)", "deleted", "relationship", maxAffected, b),
		Params: b.Params(),
		Write:  true,
	}, nil
}
