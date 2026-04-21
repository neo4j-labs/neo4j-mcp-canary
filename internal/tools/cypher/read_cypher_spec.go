// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// ReadCypherInput is the struct the handler binds incoming arguments into via
// request.BindArguments. The JSON schema advertised to MCP clients is NOT
// generated from this struct — it is declared explicitly in ReadCypherSpec
// below. See the rationale on ReadCypherSpec for why.
//
// The `jsonschema:...` struct tags that previously drove the advertised schema
// were removed when we switched to explicit declaration; keeping stale tags
// here would invite confusion about which source of truth applies.
type ReadCypherInput struct {
	Query  string `json:"query"`
	Params Params `json:"params,omitempty"`
}

// ReadCypherSpec declares the MCP tool schema for read-cypher.
//
// The schema is declared explicitly with mcp.WithString / mcp.WithObject rather
// than reflected from ReadCypherInput via mcp.WithInputSchema[T]. The previous
// reflection-based approach depended on google/jsonschema-go producing a schema
// with the expected `properties` and `required` keys for struct-tagged inputs;
// in the shipping build the advertised tool schema came through to MCP clients
// as `{"properties": {}, "required": []}`, leaving callers with no way to know
// this tool accepts a `query` or a `params` field. Declaring the schema
// explicitly removes the dependency on the reflection path entirely and makes
// the wire contract visible at the call site. Handler decoding is unaffected
// because BindArguments still unmarshals into ReadCypherInput.
//
// `query` is required; `params` is optional and omitted when the query has no
// $-placeholders. No defaults are advertised on either field — on a large graph
// an auto-filled query would silently kick off a costly full scan, and an
// empty-object default for `params` risks some client libraries serialising it
// as the string "{}", which would then fail to unmarshal on the server side.
func ReadCypherSpec() mcp.Tool {
	return mcp.NewTool("read-cypher",
		mcp.WithDescription("read-cypher can run only read-only Cypher statements. For write operations (CREATE, MERGE, DELETE, SET, etc...), schema/admin commands, or PROFILE queries, use write-cypher instead."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The read-only Cypher query to execute. Required."),
		),
		mcp.WithObject("params",
			mcp.Description("Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."),
		),
		mcp.WithTitleAnnotation("Read Cypher"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
