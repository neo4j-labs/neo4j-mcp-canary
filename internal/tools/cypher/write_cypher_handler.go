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
)

func WriteCypherHandler(deps *tools.ToolDependencies) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleWriteCypher(ctx, request, deps)
	}
}

func handleWriteCypher(ctx context.Context, request mcp.CallToolRequest, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "Database service is not initialized"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	var args WriteCypherInput
	// Use our custom BindArguments that preserves integer types
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	Query := args.Query
	Params := args.Params

	// Validate that query is not empty
	if Query == "" {
		errMessage := "Query parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	slog.Info("executing write cypher query", "query", Query)

	// Apply the cypher-tool timeout to execution. Write queries benefit from the
	// same protection as reads — a CREATE ... RETURN n on a large batch can return
	// just as many rows as any unbounded MATCH, and a long-running MERGE can hang
	// just as happily as a long-running read. See read_cypher_handler.go for the
	// broader rationale.
	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	// Execute the Cypher query using the streaming write path so both the row cap
	// and the byte cap apply to write queries too. write-cypher does not gate on
	// GetQueryType (by design — it's the escape hatch for anything read-cypher
	// refuses), so PROFILE queries that were redirected here from read-cypher will
	// execute normally without hitting the EXPLAIN-wrap conflict.
	result, err := deps.DBService.ExecuteWriteQueryStreaming(execCtx, Query, Params, deps.CypherMaxRows, deps.CypherMaxBytes)
	if err != nil {
		// Classify context errors the same way read-cypher does so operators see
		// consistent, actionable messages across both tools rather than a clean
		// error from one and a raw "ConnectivityError: context deadline exceeded"
		// from the other. The remediation hint differs from the read-cypher
		// variant because the common causes of a write-cypher timeout are
		// different: large CREATE/MERGE batches, SET over a broad MATCH, or an
		// uninstrumented apoc.periodic.iterate call. Unbounded variable-length
		// patterns are not typically what times out on the write side.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			errMessage := fmt.Sprintf(
				"write-cypher timed out: query execution exceeded the configured %s limit. "+
					"Reduce the batch size, narrow the MATCH with a more selective WHERE, "+
					"or use apoc.periodic.iterate to process the mutation in chunks, then retry.",
				deps.CypherTimeout,
			)
			slog.Info("write-cypher query timed out", "query", Query, "timeout", deps.CypherTimeout)
			return mcp.NewToolResultError(errMessage), nil
		case errors.Is(err, context.Canceled):
			slog.Info("write-cypher query cancelled", "query", Query)
			return mcp.NewToolResultError("write-cypher cancelled: query execution was cancelled before completion"), nil
		}
		slog.Error("error executing cypher query", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	response, err := deps.DBService.QueryResultToJSON(result)
	if err != nil {
		slog.Error("error formatting query results", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(response), nil
}
