// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

import (
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
)

// ToolDependencies contains all dependencies needed by tools
type ToolDependencies struct {
	DBService        database.Service
	AnalyticsService analytics.Service
	// SchemaSampleSize is the per-label / per-relationship-type budget used by
	// the get-schema sampling fallback. Total work scales with sampleSize ×
	// the number of distinct labels and relationship types in the graph, not
	// with the total number of records. A value of 0 falls back to
	// DefaultFallbackSampleSize.
	SchemaSampleSize int
	// SchemaTimeout is the maximum duration for the primary schema procedures
	// (db.schema.nodeTypeProperties / relTypeProperties). If exceeded, the handler
	// falls back to a per-label / per-type sampling approach.
	// A value of 0 disables the timeout (no fallback).
	SchemaTimeout time.Duration
}
