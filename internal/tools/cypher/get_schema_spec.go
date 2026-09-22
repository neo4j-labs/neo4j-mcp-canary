// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// getSchemaOutputSchema is computed once at package init since SchemaItem's
// shape never changes between calls. The result is wrapped in a "schema"
// object property, rather than a bare top-level array, because MCP requires
// structuredContent (and its outputSchema) to be a JSON object at the top
// level — a raw array fails Claude Desktop's tools/list validation outright.
var getSchemaOutputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"schema": {Type: "array", Items: mcpsdk.MustOutputSchemaFor[SchemaItem]()},
	},
	Required: []string{"schema"},
}

func GetSchemaSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("get-schema",
		mcpsdk.WithDescription(`
		Retrieve the schema information from the Neo4j database, including node labels, relationship types, and property keys.
		If the database contains no data, no schema information is returned.
		Requires APOC to be installed on the target database (uses apoc.meta.schema).`),
		mcpsdk.WithTitleAnnotation("Get Neo4j Schema"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(getSchemaOutputSchema),
	)
}
