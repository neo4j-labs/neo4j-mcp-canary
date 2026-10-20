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

type UpdateNodeInput struct {
	graphutil.NodeRef
	Set          cypher.Params `json:"set,omitempty"`
	Remove       []string      `json:"remove,omitempty"`
	AddLabels    []string      `json:"addLabels,omitempty"`
	RemoveLabels []string      `json:"removeLabels,omitempty"`
	MaxAffected  int           `json:"maxAffected,omitempty"`
	DryRun       bool          `json:"dryRun,omitempty"`
}

func UpdateNodeSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		Update the properties and/or labels of existing nodes: set or overwrite properties, remove properties, add labels, remove labels. Returns {matched, performed, updated, message}.
		Select nodes with id (best), match or filters — labels alone are not enough. By default at most %d node may be changed per call; if the selection matches more than maxAffected nothing is changed and performed=false tells you so. Raise maxAffected deliberately (max %d) to update several nodes at once.
		Set dryRun=true to see how many nodes would be affected, plus sample ids, without changing anything. Setting the same values again is harmless.`, defaultMaxAffect, maxMaxAffected)),
	}
	opts = append(opts, graphutil.NodeRefOptions("Optional labels the node must all have; narrows a match but cannot select a node on its own.")...)
	opts = append(opts,
		mcpsdk.WithObject("set", mcpsdk.Description(`Properties to set or overwrite, e.g. {"status": "active"}. Other properties are left untouched.`)),
		mcpsdk.WithArray("remove", "string", mcpsdk.Description("Property names to remove.")),
		mcpsdk.WithArray("addLabels", "string", mcpsdk.Description("Labels to add.")),
		mcpsdk.WithArray("removeLabels", "string", mcpsdk.Description("Labels to remove.")),
		mcpsdk.WithInteger("maxAffected", mcpsdk.Description(fmt.Sprintf("Maximum number of nodes this call may change (1-%d). Default %d.", maxMaxAffected, defaultMaxAffect))),
		mcpsdk.WithBoolean("dryRun", mcpsdk.Description("When true, only report how many nodes match; change nothing.")),
		mcpsdk.WithTitleAnnotation("Update Node"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("update-node", opts...)
}

func UpdateNodeHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "update-node", buildUpdateNode)
}

func buildUpdateNode(in *UpdateNodeInput) (graphutil.Query, error) {
	if !in.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("provide id, match or filters to say which nodes to update; labels alone would select every node with that label. Use find-nodes to look up an id")
	}
	if len(in.Set) == 0 && len(in.Remove) == 0 && len(in.AddLabels) == 0 && len(in.RemoveLabels) == 0 {
		return graphutil.Query{}, fmt.Errorf("nothing to update: provide at least one of set, remove, addLabels or removeLabels")
	}
	if err := checkPropertyKeys("set", in.Set); err != nil {
		return graphutil.Query{}, err
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

	var steps []string
	if len(in.Set) > 0 {
		steps = append(steps, "SET n += "+b.Param(map[string]any(in.Set)))
	}
	var removals []string
	for _, k := range in.Remove {
		if strings.TrimSpace(k) == "" {
			return graphutil.Query{}, fmt.Errorf("remove contains an empty property name; list the property names to remove")
		}
		removals = append(removals, "n."+graphutil.QuoteIdentifier(k))
	}
	if len(in.RemoveLabels) > 0 {
		l, err := graphutil.LabelList(in.RemoveLabels)
		if err != nil {
			return graphutil.Query{}, err
		}
		removals = append(removals, "n"+l)
	}
	if len(removals) > 0 {
		steps = append(steps, "REMOVE "+strings.Join(removals, ", "))
	}
	if len(in.AddLabels) > 0 {
		l, err := graphutil.LabelList(in.AddLabels)
		if err != nil {
			return graphutil.Query{}, err
		}
		steps = append(steps, "SET n"+l)
	}

	return graphutil.Query{
		Cypher: guardedMutation(match, "n", strings.Join(steps, "\n  "),
			"collect("+graphutil.NodeMap("n", nil)+")", "updated", "node", maxAffected, b),
		Params:    b.Params(),
		Write:     true,
		ErrorHint: "Property values must be strings, numbers, booleans or arrays of those; a uniqueness constraint may also reject the new value",
	}, nil
}
