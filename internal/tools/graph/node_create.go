// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type CreateNodeInput struct {
	Labels     []string      `json:"labels"`
	Properties cypher.Params `json:"properties,omitempty"`
	MergeOn    []string      `json:"mergeOn,omitempty"`
}

func CreateNodeSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-node",
		mcpsdk.WithDescription(`
		Create a node with one or more labels and properties, and return it (including its elementId).
		Without mergeOn this always creates a new node. With mergeOn (property names that identify the node, e.g. ["email"]) it instead finds the node with those identifying values and updates its properties, or creates it if none exists — so retrying the call never creates a duplicate.
		Property values must be strings, numbers, booleans or arrays of those; nested objects are not valid Neo4j property values.
		Use get-schema first to see which labels and property names the graph already uses.`),
		mcpsdk.WithArray("labels", "string",
			mcpsdk.Required(),
			mcpsdk.Description("One or more labels for the new node, e.g. [\"Person\"]."),
		),
		mcpsdk.WithObject("properties",
			mcpsdk.Description(`Property map for the node, e.g. {"name": "Alice", "age": 30}.`),
		),
		mcpsdk.WithArray("mergeOn", "string",
			mcpsdk.Description("Optional property names (each must be present in properties) that identify the node. When set, an existing node with the same labels and values for these properties is updated instead of creating a duplicate."),
		),
		mcpsdk.WithTitleAnnotation("Create Node"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func CreateNodeHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "create-node", buildCreateNode)
}

func buildCreateNode(in *CreateNodeInput) (graphutil.Query, error) {
	if len(in.Labels) == 0 {
		return graphutil.Query{}, fmt.Errorf("labels is required: pass at least one label, e.g. [\"Person\"]")
	}
	labels, err := graphutil.LabelList(in.Labels)
	if err != nil {
		return graphutil.Query{}, err
	}
	b := graphutil.NewBuilder()
	props := map[string]any(in.Properties)
	if props == nil {
		props = map[string]any{}
	}
	if err := checkPropertyKeys("properties", props); err != nil {
		return graphutil.Query{}, err
	}

	var stmt string
	if len(in.MergeOn) == 0 {
		stmt = fmt.Sprintf("CREATE (n%s %s)", labels, b.Param(props))
	} else {
		keys := append([]string(nil), in.MergeOn...)
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			v, ok := props[k]
			if !ok || v == nil {
				return graphutil.Query{}, fmt.Errorf("mergeOn property %q must also be present, with a non-null value, in properties; add it to properties or remove it from mergeOn", k)
			}
			parts[i] = fmt.Sprintf("%s: %s", graphutil.QuoteIdentifier(k), b.Param(v))
		}
		stmt = fmt.Sprintf("MERGE (n%s {%s})\nSET n += %s", labels, strings.Join(parts, ", "), b.Param(props))
	}
	return graphutil.Query{
		Cypher:    stmt + "\nRETURN " + graphutil.NodeMap("n", nil) + " AS node",
		Params:    b.Params(),
		Write:     true,
		ErrorHint: "Property values must be strings, numbers, booleans or arrays of those; and a uniqueness constraint may already hold a node with these values (use mergeOn to update it instead)",
	}, nil
}

func checkPropertyKeys(field string, props map[string]any) error {
	for k := range props {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%s contains an empty property name; use non-empty property names as keys", field)
		}
	}
	return nil
}
