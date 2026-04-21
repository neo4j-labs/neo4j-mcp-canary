// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func ReadCypherHandler(deps *tools.ToolDependencies) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleReadCypher(ctx, request, deps)
	}
}

func handleReadCypher(ctx context.Context, request mcp.CallToolRequest, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "Database service is not initialized"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	var args ReadCypherInput

	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}
	Query := args.Query
	Params := args.Params

	slog.Info("executing read cypher query", "query", Query)

	// Validate that query is not empty
	if Query == "" {
		errMessage := "Query parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	// Apply the cypher-tool timeout to both classification and execution. This is
	// the time-based half of the protection against unbounded queries; the row-cap
	// and byte-cap passed to ExecuteReadQueryStreaming below are the size-based
	// halves. The three are complementary — a small result that takes forever to
	// compute needs the timeout, a fast query returning five million rows needs
	// the row cap, and a query returning a thousand full nodes needs the byte cap
	// because wide rows overflow the transport regardless of row count.
	// When deps.CypherTimeout is 0 (disabled) we use the caller's context as-is,
	// which preserves the pre-fix behaviour.
	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	// Get queryType by pre-appending "EXPLAIN" to identify if the query is of type "r", if not raise a ToolResultError
	queryType, err := deps.DBService.GetQueryType(execCtx, Query, Params)
	if err != nil {
		slog.Error("error classifying cypher query", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	if queryType != neo4j.QueryTypeReadOnly { // only queryType == "r" are allowed in read-cypher
		errMessage := "read-cypher can only run read-only Cypher statements. For write operations (CREATE, MERGE, DELETE, SET, etc...), schema/admin commands, or PROFILE queries, use write-cypher instead."
		slog.Error("rejected non-read query", "type", queryType, "query", Query)
		return mcp.NewToolResultError(errMessage), nil
	}

	// EXPLAIN-time estimate guard — the proactive layer above the reactive row cap
	// and timeout. We ask the planner how many rows it thinks this query will
	// produce and refuse up-front if that number is above the configured threshold.
	// The planner folds LIMIT clauses into the root EstimatedRows, so legitimate
	// LIMIT-bounded queries pass through; only genuinely unbounded scans on large
	// labels trip the guard.
	//
	// Fail-open on estimate errors: if the extra EXPLAIN hiccups for any reason
	// we log and proceed with execution. The first EXPLAIN in GetQueryType already
	// succeeded (otherwise we wouldn't be here) so the query is syntactically
	// valid, and the row cap + timeout still bound the blast radius if the
	// estimate would have refused. Failing closed here would block legitimate
	// queries for an entirely meta-step hiccup.
	//
	// An estimate of 0 means "no estimate available" (EXPLAIN/PROFILE pre-flight,
	// empty plan, missing key). In that case we also skip the guard: we have no
	// signal to gate on.
	//
	// Three local flags carry state down to the Mixpanel emission at the bottom
	// of the function so we can report (query, estimate, actual) with the right
	// outcome label no matter which path we took.
	var (
		estimatedRows     int64
		estimateConsulted bool
		estimateFailed    bool
	)
	if deps.CypherMaxEstimatedRows > 0 {
		estimateConsulted = true
		estimate, estErr := deps.DBService.EstimateRowCount(execCtx, Query, Params)
		if estErr != nil {
			slog.Warn("failed to estimate row count, proceeding without guard", "error", estErr, "query", Query)
			estimateFailed = true
		} else {
			estimatedRows = estimate
			if estimate > int64(deps.CypherMaxEstimatedRows) {
				errMessage := fmt.Sprintf(
					"read-cypher refused: the planner estimates this query will return %d rows, "+
						"which exceeds the configured threshold of %d. Add a LIMIT clause or a "+
						"more selective filter (WHERE, specific label, index lookup) and retry. "+
						"Note: planner estimates can be imprecise — if you believe this estimate "+
						"is wrong, the threshold can be raised via NEO4J_CYPHER_MAX_ESTIMATED_ROWS.",
					estimate, deps.CypherMaxEstimatedRows,
				)
				slog.Info("rejected query above estimate threshold", "estimated", estimate, "threshold", deps.CypherMaxEstimatedRows, "query", Query)
				emitCypherEstimateAccuracy(deps, "refused_over_estimate", estimate, 0, false)
				return mcp.NewToolResultError(errMessage), nil
			}
		}
	}

	// Execute the Cypher query using the streaming database service. ExecuteReadQueryStreaming
	// uses the session + manual-iteration API (not the eager ExecuteQuery path) so it can
	// stop early when deps.CypherMaxRows or deps.CypherMaxBytes is reached — which is the
	// fix for the multi-million-row hang and the 1 MB transport overflow that motivated
	// this patch.
	result, err := deps.DBService.ExecuteReadQueryStreaming(execCtx, Query, Params, deps.CypherMaxRows, deps.CypherMaxBytes)
	if err != nil {
		// Classify context errors into user-facing messages that mirror the
		// truncation-hint pattern elsewhere in this tool: concrete cause,
		// concrete limit, actionable next step. Anything else surfaces as-is
		// so driver/server errors aren't obscured by our classification layer.
		//
		// DeadlineExceeded is the common case — a query that ran longer than
		// deps.CypherTimeout allows. The previous passthrough surfaced this as
		// "ConnectivityError: context deadline exceeded" via the driver's
		// wrapping, which was actively misleading: readers of that error
		// investigated network/DNS before realising the query itself was the
		// problem. The new message names the timeout explicitly and points at
		// the three remediations that resolve the common causes.
		//
		// Canceled distinguishes "the caller aborted" from "the server took too
		// long" — under MCP this typically means the LLM client moved on or the
		// user cancelled the tool call. No remediation hint because there's no
		// user error to correct.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			errMessage := fmt.Sprintf(
				"read-cypher timed out: query execution exceeded the configured %s limit. "+
					"Bound variable-length patterns (for example [*1..4] instead of [*]), "+
					"add a WHERE filter earlier in the query, or use LIMIT to cap the "+
					"result size, then retry.",
				deps.CypherTimeout,
			)
			slog.Info("read-cypher query timed out", "query", Query, "timeout", deps.CypherTimeout)
			return mcp.NewToolResultError(errMessage), nil
		case errors.Is(err, context.Canceled):
			slog.Info("read-cypher query cancelled", "query", Query)
			return mcp.NewToolResultError("read-cypher cancelled: query execution was cancelled before completion"), nil
		}
		slog.Error("error executing cypher query", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Format the streaming result as a JSON envelope that carries both the rows and
	// the truncation metadata (truncated / truncationReason / rowCount / maxRows /
	// maxBytes / hint). The agent on the other side can then read the hint and
	// retry with a LIMIT or a narrower projection depending on which cap fired.
	response, err := deps.DBService.QueryResultToJSON(result)
	if err != nil {
		slog.Error("error formatting query results", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Emit the estimate-vs-actual telemetry only when the guard was active — if
	// the operator has disabled it (CypherMaxEstimatedRows == 0) there's no
	// calibration signal worth capturing. The "executed" outcome covers the
	// happy path; "estimate_error" covers the fail-open path where we didn't
	// have an estimate but ran the query anyway.
	if estimateConsulted {
		outcome := "executed"
		if estimateFailed {
			outcome = "estimate_error"
		}
		emitCypherEstimateAccuracy(deps, outcome, estimatedRows, result.RowCount, result.Truncated)
	}

	return mcp.NewToolResultText(response), nil
}

// emitCypherEstimateAccuracy dispatches a CYPHER_ESTIMATE_ACCURACY event if
// telemetry is enabled. Kept out of the main handler body because we have
// three emission sites (refusal, executed, estimate-error) and they all share
// the same nil/enabled guard and the same config-echo fields. Inlining would
// triple the visual noise in the handler for no readability benefit.
//
// The function is deliberately tolerant: a nil AnalyticsService or a disabled
// service is a silent no-op. Callers don't need to pre-check.
func emitCypherEstimateAccuracy(deps *tools.ToolDependencies, outcome string, estimatedRows int64, actualRows int, truncated bool) {
	if deps.AnalyticsService == nil || !deps.AnalyticsService.IsEnabled() {
		return
	}
	deps.AnalyticsService.EmitEvent(
		deps.AnalyticsService.NewCypherEstimateEvent(
			outcome,
			estimatedRows,
			actualRows,
			truncated,
			deps.CypherMaxEstimatedRows,
			deps.CypherMaxRows,
		),
	)
}
