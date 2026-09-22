// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"github.com/google/jsonschema-go/jsonschema"
)

// profileCypherOutputSchema is hand-built rather than reflected via
// mcpsdk.MustOutputSchemaFor[ProfileCypherResponse]() for the same reason
// explain-cypher's schema is: ProfileNode.Children is self-referential
// ([]ProfileNode), which the reflection-based schema builder rejects
// outright ("cycle detected"). The top-level shape mirrors
// database.CypherResponse's own fields (see cypherResponseOutputSchema in
// read_cypher_spec.go) plus the profiled plan tree.
var profileCypherOutputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"rows":             {Type: "array", Items: &jsonschema.Schema{Type: "object"}},
		"rowCount":         {Type: "integer"},
		"truncated":        {Type: "boolean"},
		"truncationReason": {Type: "string"},
		"maxRows":          {Type: "integer"},
		"maxBytes":         {Type: "integer"},
		"hint":             {Type: "string"},
		"profile":          {Ref: "#/$defs/ProfileNode"},
	},
	Defs: map[string]*jsonschema.Schema{
		"ProfileNode": {
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"operator":          {Type: "string"},
				"arguments":         {Type: "object"},
				"identifiers":       {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
				"dbHits":            {Type: "integer"},
				"rows":              {Type: "integer"},
				"timeMs":            {Type: "number"},
				"pageCacheHits":     {Type: "integer"},
				"pageCacheMisses":   {Type: "integer"},
				"pageCacheHitRatio": {Type: "number"},
				"children":          {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/ProfileNode"}},
			},
		},
	},
}

// ProfileCypherInput is the struct the handler binds incoming arguments into
// via request.BindArguments. The advertised JSON schema is declared
// explicitly in ProfileCypherSpec below, for the same reason as
// ReadCypherInput — see ReadCypherSpec's rationale comment.
type ProfileCypherInput struct {
	Query  string `json:"query"`
	Params Params `json:"params,omitempty"`
}

// ProfileCypherSpec declares the MCP tool schema for profile-cypher.
//
// Annotations mirror write-cypher, not read-cypher: PROFILE always executes
// the statement for real in a write-capable session — the same convention
// GetQueryType's FirstKeyword=="PROFILE" short-circuit already relies on —
// so this tool is not safe to expose as read-only just because the caller's
// underlying statement happens to be a read.
func ProfileCypherSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("profile-cypher",
		mcpsdk.WithDescription(
			"profile-cypher executes the given Cypher statement (with write access) and returns both the query "+
				"results and a profiled plan with runtime statistics (dbHits, rows, time, page cache hit ratio per "+
				"operator). Do not prefix the query with EXPLAIN or PROFILE yourself — profile-cypher adds PROFILE "+
				"automatically and rejects a query that already carries either prefix. For a plan without executing, "+
				"use explain-cypher instead.",
		),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("The Cypher query to profile. Required. May contain write operations. Do not prefix with EXPLAIN or PROFILE."),
		),
		mcpsdk.WithObject("params",
			mcpsdk.Description("Optional parameters to bind to $-placeholders in the query. Must be a JSON object. Omit when the query has no placeholders."),
		),
		mcpsdk.WithTitleAnnotation("Profile Cypher"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(profileCypherOutputSchema),
	)
}
