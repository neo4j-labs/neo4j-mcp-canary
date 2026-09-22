// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package eventing

import (
	"context"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// OnToolCallComplete is called after every tool call completes.
func (e *Emitter) OnToolCallComplete(_ context.Context, request *mcpsdk.CallToolRequest, result *mcpsdk.CallToolResult) {
	if e.an == nil || !e.an.IsEnabled() {
		return
	}

	toolName := request.Params.Name

	success := true
	if result != nil {
		success = !result.IsError
	}

	// Build vector info based on tool type. read-cypher/write-cypher/
	// explain-cypher/profile-cypher all carry a raw "query" argument and can
	// invoke a vector/fulltext procedure through it (the only way to do so
	// before the search category existed), so they share the same
	// string-detection path. The search category's own tools don't need
	// detection — the tool identity already says what happened — except
	// create-vector-index/create-fulltext-index, which are index-schema
	// operations rather than a search/property-set matching these flags'
	// existing semantics, so they're deliberately left untagged.
	var vectorInfo *analytics.ToolVectorInfo
	switch toolName {
	case "read-cypher", "write-cypher", "explain-cypher", "profile-cypher":
		vectorInfo = extractCypherVectorInfo(request)
	case "vector-search":
		vectorSearchTrue := true
		vectorInfo = &analytics.ToolVectorInfo{
			VectorSearch: &vectorSearchTrue,
		}
	case "fulltext-search":
		fullTextSearchTrue := true
		vectorInfo = &analytics.ToolVectorInfo{
			FullTextSearch: &fullTextSearchTrue,
		}
	case "set-vector-property":
		vectorPropertySetTrue := true
		vectorInfo = &analytics.ToolVectorInfo{
			VectorPropertySet: &vectorPropertySetTrue,
		}
	}

	// Emit tool event (connection info sent separately in CONNECTION_INITIALIZED event)
	e.an.EmitEvent(e.an.NewToolEvent(toolName, success, vectorInfo, e.cfg.OutputFormat))

	// Handle GDS events for cypher tools that actually execute the query.
	// profile-cypher executes for real (same as write-cypher) so a
	// gds.graph.project/drop call through it really does create/drop a
	// projection; explain-cypher never executes at all, so it's excluded —
	// tagging it would report a projection that was never actually created.
	if toolName == "read-cypher" || toolName == "write-cypher" || toolName == "profile-cypher" {
		e.emitGDSEventsIfNeeded(request)
	}
}

// extractCypherVectorInfo inspects a Cypher query to detect vector search, vector property set,
// and full-text search operations. Detection is based on well-known procedure names and Cypher patterns.
func extractCypherVectorInfo(request *mcpsdk.CallToolRequest) *analytics.ToolVectorInfo {
	queryRaw, ok := request.Params.Arguments["query"]
	if !ok {
		return nil
	}
	queryStr, ok := queryRaw.(string)
	if !ok {
		return nil
	}

	lowerQuery := strings.ToLower(queryStr)

	// Detect vector search: db.index.vector.queryNodes / db.index.vector.queryRelationships
	vectorSearch := strings.Contains(lowerQuery, "db.index.vector.query")

	// Detect vector property set: db.create.setNodeVectorProperty / db.create.setRelationshipVectorProperty
	vectorPropertySet := strings.Contains(lowerQuery, "db.create.setnodevectorproperty") ||
		strings.Contains(lowerQuery, "db.create.setrelationshipvectorproperty")

	// Detect full-text search: db.index.fulltext.queryNodes / db.index.fulltext.queryRelationships
	fullTextSearch := strings.Contains(lowerQuery, "db.index.fulltext.querynodes") ||
		strings.Contains(lowerQuery, "db.index.fulltext.queryrelationships")

	// Only return info if at least one operation was detected
	if !vectorSearch && !vectorPropertySet && !fullTextSearch {
		return nil
	}

	return &analytics.ToolVectorInfo{
		VectorSearch:      &vectorSearch,
		VectorPropertySet: &vectorPropertySet,
		FullTextSearch:    &fullTextSearch,
	}
}

// emitGDSEventsIfNeeded checks if the cypher query contains GDS calls and emits appropriate events
func (e *Emitter) emitGDSEventsIfNeeded(request *mcpsdk.CallToolRequest) {
	queryRaw, ok := request.Params.Arguments["query"]
	if !ok {
		return
	}

	queryStr, ok := queryRaw.(string)
	if !ok {
		return
	}

	lowerQuery := strings.ToLower(queryStr)
	if strings.Contains(lowerQuery, "call gds.graph.project") {
		e.an.EmitEvent(e.an.NewGDSProjCreatedEvent())
	}
	if strings.Contains(lowerQuery, "call gds.graph.drop") {
		e.an.EmitEvent(e.an.NewGDSProjDropEvent())
	}
}
