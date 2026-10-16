// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import "github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

// NodeRefDoc describes a nested node reference object in a tool schema.
const NodeRefDoc = `An object that selects nodes: {"id": elementId string, "labels": [labels the node must all have], "match": {property: exact value}, "filters": [{"property", "operator", "value"}]}. All given criteria are AND-ed. Prefer "id" (returned by every tool) when you have it.`

// FiltersDoc describes a filters array property.
const FiltersDoc = `Optional predicates, AND-ed: each item is {"property": string, "operator": one of =, <>, <, >, <=, >=, IN, CONTAINS, STARTS WITH, ENDS WITH, IS NULL, IS NOT NULL, "value": the comparison value (an array for IN; omitted for IS NULL / IS NOT NULL)}.`

// NodeRefOptions declares the flat id/labels/match/filters properties a tool
// uses to select nodes (the flattened form of NodeRef).
func NodeRefOptions(labelsDoc string) []mcpsdk.ToolOption {
	return []mcpsdk.ToolOption{
		mcpsdk.WithString("id", mcpsdk.Description("elementId of a node (as returned by any graph tool). The most precise way to select a node.")),
		mcpsdk.WithArray("labels", "string", mcpsdk.Description(labelsDoc)),
		mcpsdk.WithObject("match", mcpsdk.Description(`Exact-equality property map, e.g. {"name": "Alice"}. Values must be non-null.`)),
		mcpsdk.WithArray("filters", "object", mcpsdk.Description(FiltersDoc)),
	}
}
