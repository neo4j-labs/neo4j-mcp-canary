// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package gds

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"github.com/google/jsonschema-go/jsonschema"
)

// listGdsProceduresOutputSchema describes the structured-output shape:
// listGdsProceduresQuery's fixed YIELD list (name, description, signature,
// type) isn't backed by a Go struct, so this is hand-written rather than
// reflected. (jsonschema-go is a generic JSON Schema library, not an MCP
// SDK, so importing it here doesn't violate mcpsdk's "only package allowed
// to import an MCP SDK directly" boundary.) The procedure list is wrapped in
// a "procedures" object property, rather than a bare top-level array,
// because MCP requires structuredContent (and its outputSchema) to be a
// JSON object at the top level — a raw array fails Claude Desktop's
// tools/list validation outright.
var listGdsProceduresOutputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"procedures": {
			Type: "array",
			Items: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name":        {Type: "string"},
					"description": {Type: "string"},
					"signature":   {Type: "string"},
					"type":        {Type: "string"},
				},
			},
		},
	},
	Required: []string{"procedures"},
}

func ListGDSProceduresSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("list-gds-procedures",
		mcpsdk.WithDescription(
			"Use this tool to discover what graph science and analytics functions are available in the current Neo4j environment. "+
				"It returns a structured list describing each function — what it does, how to use it, the inputs it needs, and what kind of results it produces. "+
				"Do this before any reasoning, query generation, or analysis so you know what capabilities exist. "+
				"Graph science and analytics functions help you with centrality, community detection, similarity, path finding, and identifying dependencies between nodes. "+
				"The tool helps you understand the analytical capabilities of the system so that you can plan or compose the right graph science operations automatically. "+
				"An empty response indicates that GDS is not installed and the user should be told to install it. "+
				"Remember to use unique names for graph data science projections to avoid collisions and to drop them afterwards to save memory. "+
				"You must always tell the user the function you will use.",
		),
		mcpsdk.WithTitleAnnotation("List available Neo4j GDS procedures"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(listGdsProceduresOutputSchema),
	)
}
