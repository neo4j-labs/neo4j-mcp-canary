// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

type WriteCypherInput struct {
	// No jsonschema default on Query — see rationale on ReadCypherInput.Query.
	// Especially important for write-cypher, where an auto-filled default could trigger
	// an unintended mutation.
	Query string `json:"query" jsonschema:"description=The Cypher query to execute. Required. May contain write operations (CREATE, MERGE, DELETE, SET) and schema or admin commands."`

	// No jsonschema default on Params — see rationale on ReadCypherInput.Params.
	Params Params `json:"params,omitempty" jsonschema:"description=Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."`
}

func WriteCypherSpec() mcp.Tool {
	return mcp.NewTool("write-cypher",
		mcp.WithDescription("write-cypher executes any arbitrary Cypher query, with write access, against the user-configured Neo4j database."),
		mcp.WithInputSchema[WriteCypherInput](),
		mcp.WithTitleAnnotation("Write Cypher"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
