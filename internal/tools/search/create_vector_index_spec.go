// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// CreateVectorIndexInput is the struct the handler binds incoming arguments
// into via request.BindArguments. The advertised JSON schema is declared
// explicitly in CreateVectorIndexSpec below.
type CreateVectorIndexInput struct {
	Name                 string   `json:"name,omitempty"`
	EntityType           string   `json:"entityType"`
	Label                string   `json:"label,omitempty"`
	RelationshipType     string   `json:"relationshipType,omitempty"`
	Property             string   `json:"property"`
	Dimensions           int      `json:"dimensions"`
	SimilarityFunction   string   `json:"similarityFunction,omitempty"`
	FilterableProperties []string `json:"filterableProperties,omitempty"`
	QuantizationType     string   `json:"quantizationType,omitempty"`
	HnswM                int      `json:"hnswM,omitempty"`
	HnswEfConstruction   int      `json:"hnswEfConstruction,omitempty"`
}

// createVectorIndexOutputSchema is computed once at package init since
// CreateVectorIndexOutput's shape never changes between calls.
var createVectorIndexOutputSchema = mcpsdk.MustOutputSchemaFor[CreateVectorIndexOutput]()

// CreateVectorIndexSpec declares the MCP tool schema for create-vector-index.
//
// Every input is a structured field and the output is a structured
// confirmation object — this tool never accepts or returns raw Cypher; the
// generated CREATE VECTOR INDEX statement is a pure internal implementation
// detail.
func CreateVectorIndexSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-vector-index",
		mcpsdk.WithDescription(`
		Create a vector index on the Neo4j database via structured fields — it does not accept or return raw Cypher.
		A vector index enables approximate nearest-neighbor similarity search (via vector-search) over a single embedding property on a node label or relationship type.
		dimensions and similarityFunction should match whatever embedding model produced the vectors you plan to store (e.g. 1536 dimensions / cosine similarity for many OpenAI embedding models) — a mismatch will not be caught at index-creation time, only later when stored vectors don't fit.
		filterableProperties registers extra properties that can later be used in vector-search's filters for in-index WHERE filtering; a property NOT listed here cannot be filtered on later, even if it exists on the entity.
		SHOW INDEXES has no columns for the resolved vector.* configuration, so the response's config field is the only place to see the effective dimensions/similarityFunction/quantizationType/hnswM/hnswEfConstruction actually applied (including defaults for anything you didn't set).
		When name is omitted, a generated name is assigned and returned in the response so it can be used later with drop-index or vector-search.`),
		mcpsdk.WithString("name",
			mcpsdk.Description("Optional name for the index. When omitted, a name is generated automatically and returned in the response."),
		),
		mcpsdk.WithString("entityType",
			mcpsdk.Required(),
			mcpsdk.Enum("NODE", "RELATIONSHIP"),
			mcpsdk.Description("Whether the index applies to a node label (NODE) or a relationship type (RELATIONSHIP)."),
		),
		mcpsdk.WithString("label",
			mcpsdk.Description("The node label the index applies to. Required when entityType is NODE; must not be set when entityType is RELATIONSHIP."),
		),
		mcpsdk.WithString("relationshipType",
			mcpsdk.Description("The relationship type the index applies to. Required when entityType is RELATIONSHIP; must not be set when entityType is NODE."),
		),
		mcpsdk.WithString("property",
			mcpsdk.Required(),
			mcpsdk.Description("The property that holds the embedding vector."),
		),
		mcpsdk.WithInteger("dimensions",
			mcpsdk.Required(),
			mcpsdk.Description("The number of dimensions of the embedding vectors to be indexed, 1-4096. Must match the embedding model that produced the vectors."),
		),
		mcpsdk.WithString("similarityFunction",
			mcpsdk.Enum("cosine", "euclidean"),
			mcpsdk.Description("The vector similarity function to use. Defaults to cosine."),
		),
		mcpsdk.WithArray("filterableProperties", "string",
			mcpsdk.Description("Optional extra properties to register as filterable for later use in vector-search's filters. A property not listed here cannot be filtered on later."),
		),
		mcpsdk.WithString("quantizationType",
			mcpsdk.Enum("none", "scalar", "binary"),
			mcpsdk.Description("The vector quantization type to use. Defaults to binary."),
		),
		mcpsdk.WithInteger("hnswM",
			mcpsdk.Description("The HNSW graph's max number of connections per node. Defaults to 16."),
		),
		mcpsdk.WithInteger("hnswEfConstruction",
			mcpsdk.Description("The HNSW graph's construction-time search breadth. Defaults to 100."),
		),
		mcpsdk.WithTitleAnnotation("Create Vector Index"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(createVectorIndexOutputSchema),
	)
}
