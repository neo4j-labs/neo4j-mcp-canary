// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
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
// reason: the reflection path via mcpsdk.WithInputSchema[T] was not emitting the
// `query` / `params` properties into the advertised tool schema. Explicit
// declaration removes that dependency and keeps both tools consistent.
//
// `query` is required; `params` is optional. No defaults on either — an
// auto-filled default on write-cypher would be particularly dangerous because
// the default value could trigger an unintended mutation.
func WriteCypherSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("write-cypher",
		mcpsdk.WithDescription("write-cypher executes any arbitrary Cypher query, with write access, against the user-configured Neo4j database. It does not accept EXPLAIN- or PROFILE-prefixed queries — use explain-cypher or profile-cypher instead."),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("The Cypher query to execute. Required. May contain write operations (CREATE, MERGE, DELETE, SET) and schema or admin commands. Must not be prefixed with EXPLAIN or PROFILE."),
		),
		mcpsdk.WithObject("params",
			mcpsdk.Description("Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."),
		),
		mcpsdk.WithTitleAnnotation("Write Cypher"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(cypherResponseOutputSchema),
	)
}
