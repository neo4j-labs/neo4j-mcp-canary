// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// FullTextSearchInput is the struct the handler binds incoming arguments
// into via request.BindArguments. The advertised JSON schema is declared
// explicitly in FullTextSearchSpec below.
type FullTextSearchInput struct {
	IndexName   string `json:"indexName"`
	QueryString string `json:"queryString"`
	TopK        int    `json:"topK"`
	Skip        int    `json:"skip,omitempty"`
	Analyzer    string `json:"analyzer,omitempty"`
}

// FullTextSearchSpec declares the MCP tool schema for fulltext-search.
//
// Every input is a structured field and the output is the same rows/
// truncation envelope every other node-returning tool uses — this tool
// never accepts or returns raw Cypher. Requires Neo4j 2026.09+ for the
// SEARCH-clause full-text support this tool relies on (the server
// automatically excludes this tool below that version).
func FullTextSearchSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("fulltext-search",
		mcpsdk.WithDescription(`
		Run a Lucene-backed full-text search against a Neo4j FULLTEXT index via structured fields — it does not accept or return raw Cypher.
		Returns the topK best-matching nodes or relationships (entity type is auto-detected from the index) along with a relevance score, ordered by score.
		queryString supports Lucene query syntax (e.g. "term1 AND term2", or a quoted phrase for an exact match).
		Use list-constraints-and-indexes first to discover available fulltext index names.`),
		mcpsdk.WithString("indexName",
			mcpsdk.Required(),
			mcpsdk.Description("The name of the fulltext index to search."),
		),
		mcpsdk.WithString("queryString",
			mcpsdk.Required(),
			mcpsdk.Description("The Lucene query string to search for."),
		),
		mcpsdk.WithInteger("topK",
			mcpsdk.Required(),
			mcpsdk.Description("The maximum number of results to return."),
		),
		mcpsdk.WithInteger("skip",
			mcpsdk.Description("Optional number of top results to skip, for pagination. Defaults to 0."),
		),
		mcpsdk.WithString("analyzer",
			mcpsdk.Description("Optional analyzer name to use for this query instead of the index's configured default (e.g. \"english\")."),
		),
		mcpsdk.WithTitleAnnotation("Full-Text Search"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(cypherResponseOutputSchema),
	)
}
