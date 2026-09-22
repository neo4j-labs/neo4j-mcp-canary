// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// CreateFullTextIndexInput is the struct the handler binds incoming
// arguments into via request.BindArguments. The advertised JSON schema is
// declared explicitly in CreateFullTextIndexSpec below.
type CreateFullTextIndexInput struct {
	Name                 string   `json:"name,omitempty"`
	EntityType           string   `json:"entityType"`
	Labels               []string `json:"labels,omitempty"`
	RelationshipTypes    []string `json:"relationshipTypes,omitempty"`
	Properties           []string `json:"properties"`
	Analyzer             string   `json:"analyzer,omitempty"`
	EventuallyConsistent bool     `json:"eventuallyConsistent,omitempty"`
}

// createFullTextIndexOutputSchema is computed once at package init since
// CreateFullTextIndexOutput's shape never changes between calls.
var createFullTextIndexOutputSchema = mcpsdk.MustOutputSchemaFor[CreateFullTextIndexOutput]()

// CreateFullTextIndexSpec declares the MCP tool schema for
// create-fulltext-index.
//
// Every input is a structured field and the output is a structured
// confirmation object — this tool never accepts or returns raw Cypher; the
// generated CREATE FULLTEXT INDEX statement is a pure internal
// implementation detail.
func CreateFullTextIndexSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-fulltext-index",
		mcpsdk.WithDescription(`
		Create a fulltext index on the Neo4j database via structured fields — it does not accept or return raw Cypher.
		A fulltext index enables Lucene-backed text search (via fulltext-search) over one or more properties, across one or more node labels or relationship types.
		analyzer and eventuallyConsistent are optional refinements: when neither is set, the server's own default configuration applies untouched.
		When name is omitted, a generated name is assigned and returned in the response so it can be used later with drop-index or fulltext-search.`),
		mcpsdk.WithString("name",
			mcpsdk.Description("Optional name for the index. When omitted, a name is generated automatically and returned in the response."),
		),
		mcpsdk.WithString("entityType",
			mcpsdk.Required(),
			mcpsdk.Enum("NODE", "RELATIONSHIP"),
			mcpsdk.Description("Whether the index applies to node labels (NODE) or relationship types (RELATIONSHIP)."),
		),
		mcpsdk.WithArray("labels", "string",
			mcpsdk.Description("The node label(s) the index applies to. Required (non-empty) when entityType is NODE; must not be set when entityType is RELATIONSHIP."),
		),
		mcpsdk.WithArray("relationshipTypes", "string",
			mcpsdk.Description("The relationship type(s) the index applies to. Required (non-empty) when entityType is RELATIONSHIP; must not be set when entityType is NODE."),
		),
		mcpsdk.WithArray("properties", "string",
			mcpsdk.Required(),
			mcpsdk.Description("The property key(s) to index for text search."),
		),
		mcpsdk.WithString("analyzer",
			mcpsdk.Description("Optional Lucene analyzer name (e.g. \"english\"). When omitted, the server's own default analyzer applies."),
		),
		mcpsdk.WithBoolean("eventuallyConsistent",
			mcpsdk.Description("Optional. When true, the index is populated asynchronously in the background instead of synchronously with the write. Defaults to the server's own default when omitted."),
		),
		mcpsdk.WithTitleAnnotation("Create Fulltext Index"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(createFullTextIndexOutputSchema),
	)
}
