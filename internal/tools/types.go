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
	// SchemaSampleSize is forwarded to apoc.meta.schema's `sample` parameter
	// and caps how many nodes per label APOC examines when inferring the schema.
	SchemaSampleSize int
	// CypherMaxRows is the row cap applied to read-cypher and write-cypher results
	// by the streaming execution path. When the server produces more rows than this
	// cap, iteration stops early and the response is flagged as truncated with a hint
	// telling the caller to add a LIMIT. A value of 0 disables the cap (intended for
	// tests or very trusting deployments).
	CypherMaxRows int
	// CypherMaxBytes is the byte cap applied alongside CypherMaxRows. Each record
	// is measured via JSON marshalling as it streams in; when the running total
	// would exceed this cap, iteration stops. The cap protects against wide-row
	// responses (for example 1000 full Company nodes with 19 properties each)
	// that would otherwise blow past the MCP transport's ~1 MB ceiling and fail
	// with no structured signal to the agent. When the cap fires, the truncation
	// hint steers the agent toward projecting fewer properties rather than
	// lowering the LIMIT. A value of 0 disables the cap.
	CypherMaxBytes int
	// CypherTimeout is the per-call context timeout applied by the read-cypher and
	// write-cypher handlers. It wraps both query-type classification and execution.
	// A value of 0 disables the timeout and runs with the caller's context as-is.
	CypherTimeout time.Duration
	// CypherMaxEstimatedRows is the EXPLAIN-time row estimate above which read-cypher
	// refuses the query. The handler reads the planner's root EstimatedRows and, if
	// it exceeds this threshold, returns a "add a LIMIT" error without executing.
	// This is the proactive counterpart to CypherMaxRows (reactive row cap) and
	// CypherTimeout (reactive time cap): it catches queries the planner already
	// knows will be huge, before we spend any time on them. A value of 0 disables
	// the guard.
	CypherMaxEstimatedRows int
}
