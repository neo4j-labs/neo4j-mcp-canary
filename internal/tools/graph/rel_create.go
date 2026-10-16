// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type CreateRelationshipInput struct {
	From           graphutil.NodeRef `json:"from"`
	To             graphutil.NodeRef `json:"to"`
	Type           string            `json:"type"`
	Properties     cypher.Params     `json:"properties,omitempty"`
	AllowDuplicate bool              `json:"allowDuplicate,omitempty"`
}

func CreateRelationshipSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-relationship",
		mcpsdk.WithDescription(`
		Create a directed relationship from one existing node to another and return it. Returns {fromMatched, toMatched, relationship, message}.
		Both ends must each select exactly one node (use id, or match/filters that identify a single node). If either selects zero or several nodes, nothing is created and message says which end to fix — use find-nodes to look up ids.
		By default an identical relationship (same type, same direction, same two nodes) is reused rather than duplicated, so retrying is safe; its properties are updated to the given ones. Set allowDuplicate=true to always create a new one.
		Create the nodes first with create-node if they do not exist yet.`),
		mcpsdk.WithObject("from", mcpsdk.Required(), mcpsdk.Description("Selects the start node. "+graphutil.NodeRefDoc)),
		mcpsdk.WithObject("to", mcpsdk.Required(), mcpsdk.Description("Selects the end node. "+graphutil.NodeRefDoc)),
		mcpsdk.WithString("type", mcpsdk.Required(), mcpsdk.Description("Relationship type, e.g. \"KNOWS\". By convention UPPER_SNAKE_CASE.")),
		mcpsdk.WithObject("properties", mcpsdk.Description(`Optional relationship property map, e.g. {"since": 2020}.`)),
		mcpsdk.WithBoolean("allowDuplicate", mcpsdk.Description("When true, always create a new relationship even if an identical one exists. Default false.")),
		mcpsdk.WithTitleAnnotation("Create Relationship"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func CreateRelationshipHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "create-relationship", buildCreateRelationship)
}

func buildCreateRelationship(in *CreateRelationshipInput) (graphutil.Query, error) {
	if in.Type == "" {
		return graphutil.Query{}, fmt.Errorf("type is required, e.g. \"KNOWS\"")
	}
	if !in.From.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("from must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id")
	}
	if !in.To.HasPredicate() {
		return graphutil.Query{}, fmt.Errorf("to must select a node with id, match or filters; labels alone are not enough. Use find-nodes to look up an id")
	}
	if err := checkPropertyKeys("properties", in.Properties); err != nil {
		return graphutil.Query{}, err
	}
	props := map[string]any(in.Properties)
	if props == nil {
		props = map[string]any{}
	}

	b := graphutil.NewBuilder()
	fromMatch, err := nodeMatch(in.From, "src", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("from: %w", err)
	}
	toMatch, err := nodeMatch(in.To, "dst", b)
	if err != nil {
		return graphutil.Query{}, fmt.Errorf("to: %w", err)
	}
	typ := graphutil.QuoteIdentifier(in.Type)
	write := fmt.Sprintf("MERGE (src)-[r:%s]->(dst)\n    SET r += %s", typ, b.Param(props))
	if in.AllowDuplicate {
		write = fmt.Sprintf("CREATE (src)-[r:%s %s]->(dst)", typ, b.Param(props))
	}
	message := b.Param("No relationship was created: `from` and `to` must each select exactly one node, but fromMatched / toMatched show how many they selected. Narrow the one that is not 1 (use id for an exact node); use find-nodes to look up ids.")

	return graphutil.Query{
		Cypher: fmt.Sprintf(`%s
WITH collect(src) AS sources
%s
WITH sources, collect(dst) AS targets
OPTIONAL CALL (sources, targets) {
  WITH sources[0] AS src, targets[0] AS dst
  WHERE size(sources) = 1 AND size(targets) = 1
  %s
  RETURN %s AS relationship
}
RETURN size(sources) AS fromMatched, size(targets) AS toMatched, relationship, CASE WHEN relationship IS NULL THEN %s END AS message`,
			fromMatch, toMatch, write, graphutil.RelMap("r", nil), message),
		Params:    b.Params(),
		Write:     true,
		ErrorHint: "Property values must be strings, numbers, booleans or arrays of those",
	}, nil
}
