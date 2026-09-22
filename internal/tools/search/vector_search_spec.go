// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// VectorSearchInput is the struct the handler binds incoming arguments
// into via request.BindArguments. The advertised JSON schema is declared
// explicitly in VectorSearchSpec below, following this repo's hand-built-
// schema convention.
type VectorSearchInput struct {
	IndexName        string    `json:"indexName"`
	QueryVector      []float64 `json:"queryVector"`
	TopK             int       `json:"topK"`
	Filters          []Filter  `json:"filters,omitempty"`
	ReturnProperties []string  `json:"returnProperties,omitempty"`
}

// VectorSearchSpec declares the MCP tool schema for vector-search.
//
// Every input is a structured field and the output is the same rows/
// truncation envelope every other node-returning tool uses — this tool
// never accepts or returns raw Cypher.
func VectorSearchSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("vector-search",
		mcpsdk.WithDescription(`
		Run an approximate nearest-neighbor similarity search against a Neo4j VECTOR index via structured fields — it does not accept or return raw Cypher.
		Returns the topK closest matching nodes or relationships (entity type is auto-detected from the index) along with a similarity score, ordered by score.
		filters lets you narrow results by exact/comparison predicates (=, <, >, <=, >=, IN), but ONLY on properties the index was created with as filterable via create-vector-index's filterableProperties — filtering on any other property is a hard Neo4j limitation, not a tool restriction.
		By default the full entity (including its embedding property) is returned; set returnProperties to project only the named properties instead, which is the recommended way to avoid returning the raw embedding vector in every result.
		Use list-constraints-and-indexes first to discover available vector index names.`),
		mcpsdk.WithString("indexName",
			mcpsdk.Required(),
			mcpsdk.Description("The name of the vector index to search."),
		),
		mcpsdk.WithArray("queryVector", "number",
			mcpsdk.Required(),
			mcpsdk.Description("The embedding vector to search for nearest neighbors of."),
		),
		mcpsdk.WithInteger("topK",
			mcpsdk.Required(),
			mcpsdk.Description("The maximum number of results to return."),
		),
		mcpsdk.WithArray("filters", "object",
			mcpsdk.Description("Optional AND-joined predicates: each item is {property, operator, value} where operator is one of =, <, >, <=, >=, IN. Only works on properties registered as filterable when the index was created."),
		),
		mcpsdk.WithArray("returnProperties", "string",
			mcpsdk.Description("Optional list of property names to project instead of returning the full entity. Recommended to exclude the embedding property from results."),
		),
		mcpsdk.WithTitleAnnotation("Vector Search"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(cypherResponseOutputSchema),
	)
}
