// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// WriteCypherInput is the struct the handler binds incoming arguments into via
// request.BindArguments. The JSON schema advertised to MCP clients is NOT
// generated from this struct — it is declared explicitly in WriteCypherSpec
// below. See the rationale on ReadCypherSpec for the full story; this tool
// follows the same declaration pattern for the same reason.
type WriteCypherInput struct {
	Query  string `json:"query"`
	Params Params `json:"params,omitempty"`
}

// WriteCypherSpec declares the MCP tool schema for write-cypher.
//
// Matches the explicit-declaration approach used by ReadCypherSpec for the same
// reason: the reflection path via mcp.WithInputSchema[T] was not emitting the
// `query` / `params` properties into the advertised tool schema. Explicit
// declaration removes that dependency and keeps both tools consistent.
//
// `query` is required; `params` is optional. No defaults on either — an
// auto-filled default on write-cypher would be particularly dangerous because
// the default value could trigger an unintended mutation.
func WriteCypherSpec() mcp.Tool {
	return mcp.NewTool("write-cypher",
		mcp.WithDescription("write-cypher executes any arbitrary Cypher query, with write access, against the user-configured Neo4j database."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The Cypher query to execute. Required. May contain write operations (CREATE, MERGE, DELETE, SET) and schema or admin commands."),
		),
		mcp.WithObject("params",
			mcp.Description("Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."),
		),
		mcp.WithTitleAnnotation("Write Cypher"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
