// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

type VectorSearchInput struct {
	IndexName   string    `json:"indexName" jsonschema:"description=The name of the vector index to search"`
	QueryVector []float64 `json:"queryVector" jsonschema:"description=The query embedding vector (must match the index dimensions)"`
	TopK        int       `json:"topK" jsonschema:"default=10,description=The number of nearest neighbours to return"`
}

func VectorSearchSpec() mcp.Tool {
	return mcp.NewTool("vector-search",
		mcp.WithDescription(`Search a Neo4j vector index for nodes whose embeddings are most similar to the provided query vector.
Returns the matched nodes and their similarity scores, ordered by relevance.
Use get-schema first to discover available vector indexes, their dimensions, and similarity functions.
The queryVector must have the same number of dimensions as the target index.`),
		mcp.WithInputSchema[VectorSearchInput](),
		mcp.WithTitleAnnotation("Vector Search"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
