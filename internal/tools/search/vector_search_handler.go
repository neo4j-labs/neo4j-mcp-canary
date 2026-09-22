// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

func VectorSearchHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleVectorSearch(ctx, request, deps)
	}
}

func handleVectorSearch(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args VectorSearchInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if args.IndexName == "" {
		return mcpsdk.NewToolResultError("indexName is required and cannot be empty"), nil
	}
	if len(args.QueryVector) == 0 {
		return mcpsdk.NewToolResultError("queryVector is required and cannot be empty"), nil
	}
	if args.TopK <= 0 {
		return mcpsdk.NewToolResultError("topK must be greater than 0"), nil
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	entity, err := lookupIndexEntity(execCtx, deps, args.IndexName)
	if err != nil {
		slog.Error("failed to look up vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	pattern, varName, err := entityPatternMulti(entity.EntityType, entity.LabelsOrTypes)
	if err != nil {
		slog.Error("failed to build entity pattern for vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	whereClause, filterParams, err := buildFilterClauses(varName, args.Filters)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	returnClause := fmt.Sprintf("%s AS entity, score", varName)
	if len(args.ReturnProperties) > 0 {
		returnClause = fmt.Sprintf("%s AS entity, score", mapProjection(varName, args.ReturnProperties))
	}

	var searchBody strings.Builder
	fmt.Fprintf(&searchBody, "MATCH %s\n", pattern)
	fmt.Fprintf(&searchBody, "  SEARCH %s IN (\n", varName)
	fmt.Fprintf(&searchBody, "    VECTOR INDEX %s\n", quoteIdentifier(args.IndexName))
	searchBody.WriteString("    FOR $queryVector\n")
	if whereClause != "" {
		fmt.Fprintf(&searchBody, "    WHERE %s\n", whereClause)
	}
	searchBody.WriteString("    LIMIT $topK\n")
	fmt.Fprintf(&searchBody, "  ) SCORE AS score\n")
	fmt.Fprintf(&searchBody, "RETURN %s", returnClause)
	cypherQuery := searchBody.String()

	params := map[string]any{"queryVector": args.QueryVector, "topK": args.TopK}
	for k, v := range filterParams {
		params[k] = v
	}

	slog.Info("running vector search", "indexName", args.IndexName, "topK", args.TopK)

	records, err := deps.DBService.ExecuteReadQuery(execCtx, cypherQuery, params)
	if err != nil {
		slog.Error("error executing vector search", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	result := &database.QueryResult{Records: records, RowCount: len(records)}
	canonicalJSON, err := deps.DBService.QueryResultToJSON(result)
	if err != nil {
		slog.Error("error formatting vector search results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(canonicalJSON, deps.OutputFormat)
	if err != nil {
		slog.Error("error encoding vector search results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(canonicalJSON)), nil
}
