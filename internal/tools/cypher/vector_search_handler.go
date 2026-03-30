// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	vectorSearchQuery = `CALL db.index.vector.queryNodes($indexName, $topK, $queryVector)
YIELD node, score
RETURN node, score`
)

func VectorSearchHandler(deps *tools.ToolDependencies) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleVectorSearch(ctx, request, deps)
	}
}

func handleVectorSearch(ctx context.Context, request mcp.CallToolRequest, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	var args VectorSearchInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	if args.IndexName == "" {
		errMessage := "indexName parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	if len(args.QueryVector) == 0 {
		errMessage := "queryVector parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	if args.TopK <= 0 {
		args.TopK = 10
	}

	slog.Info("executing vector search", "indexName", args.IndexName, "topK", args.TopK, "vectorDimensions", len(args.QueryVector))

	params := map[string]any{
		"indexName":   args.IndexName,
		"topK":       args.TopK,
		"queryVector": args.QueryVector,
	}

	records, err := deps.DBService.ExecuteReadQuery(ctx, vectorSearchQuery, params)
	if err != nil {
		slog.Error("error executing vector search", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("vector search failed: %s", err.Error())), nil
	}

	response, err := deps.DBService.Neo4jRecordsToJSON(records)
	if err != nil {
		slog.Error("error formatting vector search results", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(response), nil
}
