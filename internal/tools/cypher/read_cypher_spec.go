// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

type ReadCypherInput struct {
	// No jsonschema default on Query: a default value would let a naive client auto-fill
	// the query field, which on a large graph would silently execute a costly full scan.
	// The handler already validates that Query is non-empty and returns a friendly error
	// when it isn't, so no default is needed.
	Query string `json:"query" jsonschema:"description=The read-only Cypher query to execute. Required."`

	// No jsonschema default on Params: the field is optional (omitempty) and omitted
	// entirely when the query has no $-placeholders. Advertising an empty-object default
	// risks some schema libraries serialising it as the string "{}", which would then
	// fail to unmarshal on the server side.
	Params Params `json:"params,omitempty" jsonschema:"description=Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."`
}

func ReadCypherSpec() mcp.Tool {
	return mcp.NewTool("read-cypher",
		mcp.WithDescription("read-cypher can run only read-only Cypher statements. For write operations (CREATE, MERGE, DELETE, SET, etc...), schema/admin commands, or PROFILE queries, use write-cypher instead."),
		mcp.WithInputSchema[ReadCypherInput](),
		mcp.WithTitleAnnotation("Read Cypher"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
