// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"github.com/google/jsonschema-go/jsonschema"
)

// planNodeSchema is the recursive JSON Schema for PlanNode (see
// explain_cypher_handler.go), declared via $defs rather than
// mcpsdk.MustOutputSchemaFor[PlanNode]() — PlanNode.Children is
// self-referential ([]PlanNode), which the reflection-based schema builder
// rejects outright ("cycle detected"). The top level repeats PlanNode's own
// properties (rather than a bare top-level $ref) so the schema carries an
// explicit top-level "type": "object" — MCP clients (e.g. Claude Desktop)
// validate that literally, without resolving $ref, and reject the whole
// tools/list response otherwise. Shared with profile-cypher's output schema,
// which embeds this same shape (via $ref, which is fine at a nested field)
// for its Profile field.
var planNodeSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"operator":    {Type: "string"},
		"arguments":   {Type: "object"},
		"identifiers": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		"children":    {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/PlanNode"}},
	},
	Defs: map[string]*jsonschema.Schema{
		"PlanNode": {
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"operator":    {Type: "string"},
				"arguments":   {Type: "object"},
				"identifiers": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
				"children":    {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/PlanNode"}},
			},
		},
	},
}

// explainCypherOutputSchema is computed once at package init since
// PlanNode's shape never changes between calls.
var explainCypherOutputSchema = planNodeSchema

// ExplainCypherInput is the struct the handler binds incoming arguments into
// via request.BindArguments. The advertised JSON schema is declared
// explicitly in ExplainCypherSpec below, for the same reason as
// ReadCypherInput — see ReadCypherSpec's rationale comment.
type ExplainCypherInput struct {
	Query  string `json:"query"`
	Params Params `json:"params,omitempty"`
}

// ExplainCypherSpec declares the MCP tool schema for explain-cypher.
//
// explain-cypher covers both read and write statements with a single tool
// and a single ReadOnlyHint=true annotation, rather than splitting into
// explain-read/explain-write variants the way read-cypher/write-cypher do:
// EXPLAIN never executes the underlying statement — the plan lives on the
// query summary, nothing runs against the graph — so the tool is safe
// regardless of what the statement underneath would have done.
func ExplainCypherSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("explain-cypher",
		mcpsdk.WithDescription(
			"explain-cypher returns the query plan Neo4j would use for the given Cypher statement, without executing it. "+
				"Works for read and write statements alike, since EXPLAIN never runs the query. "+
				"Do not prefix the query with EXPLAIN or PROFILE yourself — explain-cypher adds EXPLAIN automatically and "+
				"rejects a query that already carries either prefix. For runtime statistics (dbHits, rows, time per "+
				"operator), use profile-cypher instead, which actually executes the query.",
		),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("The Cypher query to explain. Required. Do not prefix with EXPLAIN or PROFILE."),
		),
		mcpsdk.WithObject("params",
			mcpsdk.Description("Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."),
		),
		mcpsdk.WithTitleAnnotation("Explain Cypher"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(explainCypherOutputSchema),
	)
}
