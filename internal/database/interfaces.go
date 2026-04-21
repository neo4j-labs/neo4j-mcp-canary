// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

//go:generate mockgen -destination=mocks/mock_database.go -package=database_mocks github.com/neo4j-labs/neo4j-mcp-canary/internal/database Service

import (
	"context"
	"errors"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// ErrExplainUnsupported is the sentinel returned by GetQueryType when the caller
// submits an EXPLAIN-prefixed query. EXPLAIN classifies cleanly as read-only at
// the protocol level and would therefore pass the read-cypher policy check, but
// on the execution path it produces zero records (the plan lives on
// ResultSummary rather than in the row stream). The MCP tool response would be
// an empty row envelope with no indication that a plan was generated, which is
// the worst of both worlds: the caller neither sees the plan nor understands
// why the rows are missing.
//
// Returning this sentinel lets the read-cypher handler intercept EXPLAIN early
// and produce a specific remediation message — "remove the EXPLAIN prefix" —
// rather than the generic "use write-cypher" redirect it uses for true write
// operations. write-cypher would exhibit the same empty-envelope behaviour for
// EXPLAIN, so redirecting there would be actively misleading.
//
// The sentinel is exported so handlers and tests can match on it with
// errors.Is. Callers outside the read-cypher path can safely ignore it — it is
// only produced by GetQueryType.
var ErrExplainUnsupported = errors.New("EXPLAIN queries are not supported by read-cypher")

// Truncation reason values for QueryResult.TruncationReason.
const (
	// TruncationReasonNone is the zero value, used when Truncated is false.
	TruncationReasonNone = ""
	// TruncationReasonRows indicates the streaming loop stopped because the
	// accumulated row count reached the configured maxRows cap.
	TruncationReasonRows = "rows"
	// TruncationReasonBytes indicates the streaming loop stopped because the
	// accumulated JSON byte size of the results would have exceeded the configured
	// maxBytes cap. Typically fires on wide-row responses (for example full nodes
	// with many properties) where the per-row size is the dominant factor; the
	// MCP-facing hint in this case steers the caller toward a narrower projection
	// rather than a smaller LIMIT.
	TruncationReasonBytes = "bytes"
)

// QueryResult is the outcome of a streaming Cypher execution. Records holds the
// rows actually returned to the caller — bounded by either the maxRows cap or
// the maxBytes cap passed into ExecuteReadQueryStreaming /
// ExecuteWriteQueryStreaming, whichever fires first.
//
// Truncated is true when the server had more rows to produce but iteration
// stopped early because one of the caps was hit; callers surface this to the
// MCP tool response so the agent can retry with a LIMIT or a narrower projection.
// TruncationReason indicates which cap fired (TruncationReasonRows or
// TruncationReasonBytes) so the hint can give the right advice.
//
// MaxRows and MaxBytes echo back the caps that were applied so the caller can
// cite concrete numbers in the hint without having to thread config through
// two layers. ByteCount is the accumulated JSON byte size of the returned
// records, measured during streaming; it's reported for observability even
// when the byte cap did not fire.
type QueryResult struct {
	Records          []*neo4j.Record
	Truncated        bool
	TruncationReason string
	RowCount         int
	MaxRows          int
	ByteCount        int
	MaxBytes         int
}

// QueryExecutor defines the interface for executing Neo4j queries
type QueryExecutor interface {
	// ExecuteReadQuery executes a read-only Cypher query and returns raw records
	// Returns an error if the query is not read-only.
	ExecuteReadQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error)

	// ExecuteWriteQuery executes a write-only Cypher query and returns raw records
	ExecuteWriteQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error)

	// ExecuteReadQueryStreaming runs a read-only Cypher query using the driver's
	// session + ExecuteRead + manual iteration API, applying two complementary caps:
	// maxRows as a row-count cap, and maxBytes as a JSON-serialised-size cap.
	// Iteration stops when either cap is reached. Unlike ExecuteReadQuery (which
	// is eager and buffers every record before returning), this path can stop early
	// when a cap is hit — protecting the MCP client both from hanging on
	// multi-million-row unbounded queries and from payloads that would overflow
	// the transport layer.
	//
	// A maxRows of 0 disables the row cap. A maxBytes of 0 disables the byte
	// cap. Callers that know their queries are inherently bounded may pass 0
	// for both; most should pass configured positive values.
	ExecuteReadQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error)

	// ExecuteWriteQueryStreaming mirrors ExecuteReadQueryStreaming for write txns.
	// Write queries can also return large result sets (CREATE ... RETURN n on a big
	// batch, for example), so the caps still apply.
	ExecuteWriteQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error)

	// GetQueryType prefixes the provided query with EXPLAIN and returns the query type (e.g. 'r' for read, 'w' for write, 'rw' etc.)
	// This allows read-only tools to determine if a query is safe to run in read-only context.
	GetQueryType(ctx context.Context, cypher string, params map[string]any) (neo4j.QueryType, error)

	// EstimateRowCount returns the planner's estimate for how many rows the given
	// query will produce. It runs EXPLAIN and reads the EstimatedRows argument on
	// the root operator of the resulting plan — which already accounts for LIMIT
	// clauses and filters the planner can reason about, so the value represents the
	// final expected output size rather than intermediate scan sizes.
	//
	// The method is used by read-cypher as a proactive guard: if the estimate is
	// above the configured threshold, the handler refuses the query before starting
	// execution. This complements the reactive row cap and context timeout.
	//
	// Returns 0 when no estimate is available — for example when the query is
	// already an EXPLAIN or PROFILE (can't be re-wrapped), when the plan is empty,
	// or when the EstimatedRows key is missing from the plan arguments. Callers
	// should treat 0 as "no estimate, skip the guard" rather than "estimated zero rows".
	EstimateRowCount(ctx context.Context, cypher string, params map[string]any) (int64, error)
}

// RecordFormatter defines the interface for formatting Neo4j records
type RecordFormatter interface {
	// Neo4jRecordsToJSON converts Neo4j records to JSON string
	Neo4jRecordsToJSON(records []*neo4j.Record) (string, error)

	// QueryResultToJSON formats a streaming QueryResult as a JSON envelope that
	// surfaces both the rows and the truncation metadata to MCP consumers. The
	// envelope shape (rowCount / truncated / maxRows / maxBytes / hint alongside
	// rows) is self-describing so an agent that hits either cap can understand
	// why its result is incomplete and what to do about it.
	QueryResultToJSON(result *QueryResult) (string, error)
}

type Helpers interface {
	VerifyConnectivity(ctx context.Context) error
}

// Service combines query execution and record formatting
type Service interface {
	QueryExecutor
	RecordFormatter
	Helpers
}
