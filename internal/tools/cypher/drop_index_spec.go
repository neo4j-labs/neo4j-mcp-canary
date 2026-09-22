// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// DropIndexInput is the struct the handler binds incoming arguments into via
// request.BindArguments. The JSON schema advertised to MCP clients is
// declared explicitly in DropIndexSpec below.
type DropIndexInput struct {
	Name string `json:"name"`
}

// dropIndexOutputSchema is computed once at package init since
// DropIndexOutput's shape never changes between calls.
var dropIndexOutputSchema = mcpsdk.MustOutputSchemaFor[DropIndexOutput]()

// DropIndexSpec declares the MCP tool schema for drop-index.
//
// Only a structured name identifies the index to drop — this tool never
// accepts or returns raw Cypher.
func DropIndexSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("drop-index",
		mcpsdk.WithDescription(`
		Drop an index from the Neo4j database by name via a structured field — it does not accept or return raw Cypher.
		Removing an index may slow down queries that relied on it, but does not affect data or any constraint it does not back.
		Use list-constraints-and-indexes first to find the exact name if it isn't already known. Dropping a name that doesn't exist is not an error — the response's existed field reports whether the index was actually present.`),
		mcpsdk.WithString("name",
			mcpsdk.Required(),
			mcpsdk.Description("The name of the index to drop."),
		),
		mcpsdk.WithTitleAnnotation("Drop Index"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(dropIndexOutputSchema),
	)
}
