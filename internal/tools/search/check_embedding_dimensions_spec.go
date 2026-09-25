// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// defaultCheckEmbeddingDimensionsSampleText is used when sampleText is
// omitted — the embedding dimension a model produces doesn't depend on the
// input text's content or length, so any short placeholder is fine.
const defaultCheckEmbeddingDimensionsSampleText = "dimension check"

// CheckEmbeddingDimensionsInput is the struct the handler binds incoming
// arguments into via request.BindArguments. The advertised JSON schema is
// declared explicitly in CheckEmbeddingDimensionsSpec below.
type CheckEmbeddingDimensionsInput struct {
	IndexName  string `json:"indexName"`
	SampleText string `json:"sampleText,omitempty"`
}

// checkEmbeddingDimensionsOutputSchema is computed once at package init
// since CheckEmbeddingDimensionsOutput's shape never changes between calls.
var checkEmbeddingDimensionsOutputSchema = mcpsdk.MustOutputSchemaFor[CheckEmbeddingDimensionsOutput]()

// CheckEmbeddingDimensionsSpec declares the MCP tool schema for
// check-embedding-dimensions.
//
// This exists to catch a specific gap: set-vector-property's `text` field
// generates an embedding via the calling instance's configured GenAI
// provider, but if that provider/model's output size doesn't match a
// vector index's configured dimensions, storing the vector still succeeds
// (Neo4j doesn't validate size at write time) and the mismatch only
// surfaces later as a confusing failure at search time. This tool lets a
// caller validate a provider/model against a target index up front,
// without writing anything.
func CheckEmbeddingDimensionsSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("check-embedding-dimensions",
		mcpsdk.WithDescription(`
		Check whether the calling instance's configured embedding provider produces vectors matching a vector index's configured dimensions — via structured fields only, no Cypher in or out.
		Generates one throwaway embedding from sampleText (or a short built-in default if omitted — the output size doesn't depend on the input text) — via this MCP server's own direct HTTP call for openai (including any OpenAI-compatible local server like LM Studio or Ollama via its baseUrl config), or via Neo4j's ai.text.embed for azure-openai/vertexai/bedrock-titan — then compares its length against the named vector index's configured dimensions (read from SHOW INDEXES' options column, the only place that resolved value is exposed).
		Fails clearly if the instance has no embedding provider configured, or if indexName doesn't name an existing VECTOR index. Nothing is written to the database either way.`),
		mcpsdk.WithString("indexName",
			mcpsdk.Required(),
			mcpsdk.Description("The name of the vector index to check against."),
		),
		mcpsdk.WithString("sampleText",
			mcpsdk.Description("Text to embed for the check. Optional — a short built-in default is used when omitted, since the output dimension count doesn't depend on the input."),
		),
		mcpsdk.WithTitleAnnotation("Check Embedding Dimensions"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(checkEmbeddingDimensionsOutputSchema),
	)
}
