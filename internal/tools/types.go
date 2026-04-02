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
	SchemaSampleSize int
	// SchemaTimeout is the maximum duration for the primary schema procedures
	// (db.schema.nodeTypeProperties / relTypeProperties). If exceeded, the handler
	// falls back to a Spark-connector-inspired sampling approach.
	// A value of 0 disables the timeout (no fallback).
	SchemaTimeout time.Duration
}
