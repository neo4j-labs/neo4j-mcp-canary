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

func FullTextSearchHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleFullTextSearch(ctx, request, deps)
	}
}

func handleFullTextSearch(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args FullTextSearchInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if args.IndexName == "" {
		return mcpsdk.NewToolResultError("indexName is required and cannot be empty"), nil
	}
	if args.QueryString == "" {
		return mcpsdk.NewToolResultError("queryString is required and cannot be empty"), nil
	}
	if args.TopK <= 0 {
		return mcpsdk.NewToolResultError("topK must be greater than 0"), nil
	}
	if args.Skip < 0 {
		return mcpsdk.NewToolResultError("skip must not be negative"), nil
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	entity, err := lookupIndexEntity(execCtx, deps, args.IndexName)
	if err != nil {
		slog.Error("failed to look up fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	pattern, varName, err := entityPatternMulti(entity.EntityType, entity.LabelsOrTypes)
	if err != nil {
		slog.Error("failed to build entity pattern for fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	var searchBody strings.Builder
	fmt.Fprintf(&searchBody, "MATCH %s\n", pattern)
	fmt.Fprintf(&searchBody, "  SEARCH %s IN (\n", varName)
	fmt.Fprintf(&searchBody, "    FULLTEXT INDEX %s\n", quoteIdentifier(args.IndexName))
	searchBody.WriteString("    FOR $queryString\n")
	if args.Analyzer != "" {
		searchBody.WriteString("    WITH ANALYZER $analyzer\n")
	}
	if args.Skip > 0 {
		searchBody.WriteString("    SKIP $skip\n")
	}
	searchBody.WriteString("    LIMIT $topK\n")
	searchBody.WriteString("  ) SCORE AS score\n")
	fmt.Fprintf(&searchBody, "RETURN %s AS entity, score", varName)
	cypherQuery := searchBody.String()

	params := map[string]any{"queryString": args.QueryString, "topK": args.TopK}
	if args.Analyzer != "" {
		params["analyzer"] = args.Analyzer
	}
	if args.Skip > 0 {
		params["skip"] = args.Skip
	}

	slog.Info("running fulltext search", "indexName", args.IndexName, "topK", args.TopK)

	records, err := deps.DBService.ExecuteReadQuery(execCtx, cypherQuery, params)
	if err != nil {
		slog.Error("error executing fulltext search", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	result := &database.QueryResult{Records: records, RowCount: len(records)}
	canonicalJSON, err := deps.DBService.QueryResultToJSON(result)
	if err != nil {
		slog.Error("error formatting fulltext search results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(canonicalJSON, deps.OutputFormat)
	if err != nil {
		slog.Error("error encoding fulltext search results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(canonicalJSON)), nil
}
