// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type UpdateRelationshipInput struct {
	RelRef
	Set         cypher.Params `json:"set,omitempty"`
	Remove      []string      `json:"remove,omitempty"`
	MaxAffected int           `json:"maxAffected,omitempty"`
	DryRun      bool          `json:"dryRun,omitempty"`
}

func UpdateRelationshipSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		Update the properties of existing relationships: set or overwrite properties, or remove properties. Returns {matched, performed, updated, message}.
		A relationship's type and end nodes cannot be changed — to do that, delete-relationship and then create-relationship.
		%s Select with id (best), match, filters, or a from/to node that has id/match/filters — a type alone is not enough. By default at most %d relationship may be changed per call; if the selection matches more than maxAffected nothing is changed and performed=false tells you so. Raise maxAffected deliberately (max %d) to update several.
		Set dryRun=true to see how many relationships would be affected, plus sample ids, without changing anything.`, relRefDoc, defaultMaxAffect, maxMaxAffected)),
	}
	opts = append(opts, relRefOptions()...)
	opts = append(opts,
		mcpsdk.WithObject("set", mcpsdk.Description(`Properties to set or overwrite, e.g. {"weight": 0.8}.`)),
		mcpsdk.WithArray("remove", "string", mcpsdk.Description("Property names to remove.")),
		mcpsdk.WithInteger("maxAffected", mcpsdk.Description(fmt.Sprintf("Maximum number of relationships this call may change (1-%d). Default %d.", maxMaxAffected, defaultMaxAffect))),
		mcpsdk.WithBoolean("dryRun", mcpsdk.Description("When true, only report how many relationships match; change nothing.")),
		mcpsdk.WithTitleAnnotation("Update Relationship"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("update-relationship", opts...)
}

func UpdateRelationshipHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "update-relationship", buildUpdateRelationship)
}

func buildUpdateRelationship(in *UpdateRelationshipInput) (graphutil.Query, error) {
	if !in.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("provide id, match, filters, or a from/to node with id/match/filters to say which relationships to update; a type alone would select every relationship of that type. Use find-relationships to look up an id")
	}
	if len(in.Set) == 0 && len(in.Remove) == 0 {
		return graphutil.Query{}, fmt.Errorf("nothing to update: provide at least one of set or remove")
	}
	if err := checkPropertyKeys("set", in.Set); err != nil {
		return graphutil.Query{}, err
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

	var steps []string
	if len(in.Set) > 0 {
		steps = append(steps, "SET r += "+b.Param(map[string]any(in.Set)))
	}
	if len(in.Remove) > 0 {
		refs := make([]string, len(in.Remove))
		for i, k := range in.Remove {
			if strings.TrimSpace(k) == "" {
				return graphutil.Query{}, fmt.Errorf("remove contains an empty property name; list the property names to remove")
			}
			refs[i] = "r." + graphutil.QuoteIdentifier(k)
		}
		steps = append(steps, "REMOVE "+strings.Join(refs, ", "))
	}
	return graphutil.Query{
		Cypher: guardedMutation(match, "r", strings.Join(steps, "\n  "),
			"collect("+graphutil.RelMap("r", nil)+")", "updated", refusalMessage("relationship", maxAffected), maxAffected, b),
		Params:    b.Params(),
		Write:     true,
		ErrorHint: "Property values must be strings, numbers, booleans or arrays of those",
	}, nil
}
