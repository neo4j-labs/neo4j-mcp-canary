// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// CreateIndexInput is the struct the handler binds incoming arguments into
// via request.BindArguments. The JSON schema advertised to MCP clients is
// declared explicitly in CreateIndexSpec below.
type CreateIndexInput struct {
	Name             string   `json:"name,omitempty"`
	IndexType        string   `json:"indexType"`
	EntityType       string   `json:"entityType"`
	Label            string   `json:"label,omitempty"`
	RelationshipType string   `json:"relationshipType,omitempty"`
	Properties       []string `json:"properties,omitempty"`
}

// createIndexOutputSchema is computed once at package init since
// CreateIndexOutput's shape never changes between calls.
var createIndexOutputSchema = mcpsdk.MustOutputSchemaFor[CreateIndexOutput]()

// CreateIndexSpec declares the MCP tool schema for create-index.
//
// Every input is a structured field and the output is a structured
// confirmation object — this tool never accepts or returns raw Cypher.
//
// Scope is deliberately limited to RANGE, TEXT, POINT, and LOOKUP indexes.
// VECTOR and FULLTEXT indexes are not supported by this tool (they need
// index-specific configuration this schema doesn't model) and are rejected
// with a clear error rather than silently ignored.
func CreateIndexSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-index",
		mcpsdk.WithDescription(`
		Create an index on the Neo4j database via structured fields — it does not accept or return raw Cypher.
		Supports RANGE (general-purpose, supports composite properties), TEXT (single string property), POINT (single spatial property), and LOOKUP (indexes every label or relationship type automatically, no label/property needed) indexes. VECTOR and FULLTEXT indexes are not supported by this tool and are rejected.
		Every fresh database already ships default LOOKUP indexes for both nodes and relationships, so creating another LOOKUP index of the same entity type may hit a server-side conflict that IF NOT EXISTS (which only guards exact name collisions) cannot prevent — that failure is expected and is simply returned as this tool's error.
		When name is omitted, a generated name is assigned and returned in the response so it can be used later with drop-index.`),
		mcpsdk.WithString("name",
			mcpsdk.Description("Optional name for the index. When omitted, a name is generated automatically and returned in the response."),
		),
		mcpsdk.WithString("indexType",
			mcpsdk.Required(),
			mcpsdk.Enum("RANGE", "TEXT", "POINT", "LOOKUP"),
			mcpsdk.Description("The kind of index to create. VECTOR and FULLTEXT are not supported by this tool."),
		),
		mcpsdk.WithString("entityType",
			mcpsdk.Required(),
			mcpsdk.Enum("NODE", "RELATIONSHIP"),
			mcpsdk.Description("Whether the index applies to a node label (NODE) or a relationship type (RELATIONSHIP)."),
		),
		mcpsdk.WithString("label",
			mcpsdk.Description("The node label the index applies to. Required for RANGE/TEXT/POINT when entityType is NODE; must not be set for LOOKUP."),
		),
		mcpsdk.WithString("relationshipType",
			mcpsdk.Description("The relationship type the index applies to. Required for RANGE/TEXT/POINT when entityType is RELATIONSHIP; must not be set for LOOKUP."),
		),
		mcpsdk.WithArray("properties", "string",
			mcpsdk.Description("The property key(s) the index covers. TEXT and POINT accept exactly one; RANGE accepts one or more (composite). LOOKUP indexes every label/type automatically and must not set this."),
		),
		mcpsdk.WithTitleAnnotation("Create Index"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(createIndexOutputSchema),
	)
}
