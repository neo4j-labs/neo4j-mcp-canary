// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

func CheckEmbeddingDimensionsHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleCheckEmbeddingDimensions(ctx, request, deps)
	}
}

func handleCheckEmbeddingDimensions(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args CheckEmbeddingDimensionsInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if args.IndexName == "" {
		return mcpsdk.NewToolResultError("indexName is required and cannot be empty"), nil
	}
	sampleText := args.SampleText
	if sampleText == "" {
		sampleText = defaultCheckEmbeddingDimensionsSampleText
	}

	embConf, ok := auth.GetEmbeddingConfig(ctx)
	if !ok {
		return mcpsdk.NewToolResultError("the connected Neo4j instance has no embedding provider configured"), nil
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	configuredDimensions, err := vectorIndexByName(execCtx, deps, args.IndexName)
	if err != nil {
		slog.Error("failed to look up vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	vector, err := generateEmbedding(execCtx, deps, sampleText, embConf)
	if err != nil {
		slog.Error("failed to generate embedding", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	actualDimensions := len(vector)
	structuredOutput := CheckEmbeddingDimensionsOutput{
		IndexName:            args.IndexName,
		Provider:             string(embConf.Provider),
		Model:                embConf.Configuration["model"],
		ConfiguredDimensions: configuredDimensions,
		ActualDimensions:     actualDimensions,
		Match:                int64(actualDimensions) == configuredDimensions,
	}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize check-embedding-dimensions result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode check-embedding-dimensions result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// CheckEmbeddingDimensionsOutput is the tool's structured output: whether
// the configured provider/model's actual output size matches the target
// index's configured dimensions.
type CheckEmbeddingDimensionsOutput struct {
	IndexName            string `json:"indexName"`
	Provider             string `json:"provider"`
	Model                string `json:"model,omitempty"`
	ConfiguredDimensions int64  `json:"configuredDimensions"`
	ActualDimensions     int    `json:"actualDimensions"`
	Match                bool   `json:"match"`
}
