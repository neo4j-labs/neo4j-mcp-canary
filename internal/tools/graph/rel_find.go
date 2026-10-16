// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type FindRelationshipsInput struct {
	RelRef
	ReturnProperties   []string `json:"returnProperties,omitempty"`
	EndpointProperties []string `json:"endpointProperties,omitempty"`
	Limit              int      `json:"limit,omitempty"`
	Skip               int      `json:"skip,omitempty"`
}

func FindRelationshipsSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		Find relationships by type, endpoints and/or properties, and return each as {id, type, from, to, properties} plus summaries of its two end nodes. Read-only.
		%s Select by type, by the nodes at either end (from / to), or by relationship properties; with no criteria it returns the first relationships in the database, so narrow where you can.
		To explore everything around one node use expand-neighbors instead. Results are paginated: at most %d per call (default %d); use skip to page.
		endpointProperties adds the named properties (e.g. "name") to the end-node summaries so you don't need a follow-up lookup.`, relRefDoc, maxFindLimit, defaultFindLimit)),
	}
	opts = append(opts, relRefOptions()...)
	opts = append(opts,
		mcpsdk.WithArray("returnProperties", "string", mcpsdk.Description("Optional relationship property names to return instead of all of them.")),
		mcpsdk.WithArray("endpointProperties", "string", mcpsdk.Description("Optional node property names to include on the from/to node summaries (they carry only id and labels by default).")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum relationships to return (1-%d). Default %d.", maxFindLimit, defaultFindLimit))),
		mcpsdk.WithInteger("skip", mcpsdk.Description("Number of relationships to skip, for paging. Default 0.")),
		mcpsdk.WithTitleAnnotation("Find Relationships"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("find-relationships", opts...)
}

func FindRelationshipsHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-relationships", buildFindRelationships)
}

func buildFindRelationships(in *FindRelationshipsInput) (graphutil.Query, error) {
	b := graphutil.NewBuilder()
	match, err := in.matchClause(b)
	if err != nil {
		return graphutil.Query{}, err
	}
	page, err := pagination(b, in.Limit, in.Skip)
	if err != nil {
		return graphutil.Query{}, err
	}
	return graphutil.Query{
		Cypher: fmt.Sprintf("%s\nRETURN %s AS relationship, %s AS fromNode, %s AS toNode%s",
			match, graphutil.RelMap("r", in.ReturnProperties),
			graphutil.NodeSummary("src", in.EndpointProperties), graphutil.NodeSummary("dst", in.EndpointProperties), page),
		Params: b.Params(),
	}, nil
}
