// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

var allowedSimilarityFunctions = map[string]bool{"cosine": true, "euclidean": true}
var allowedQuantizationTypes = map[string]bool{"none": true, "scalar": true, "binary": true}

func CreateVectorIndexHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleCreateVectorIndex(ctx, request, deps)
	}
}

func handleCreateVectorIndex(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args CreateVectorIndexInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	labelOrType, err := resolveSingleLabelOrType(args.EntityType, args.Label, args.RelationshipType)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	pattern, varName, err := entityPatternMulti(args.EntityType, []string{labelOrType})
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if args.Property == "" {
		return mcpsdk.NewToolResultError("property is required and cannot be empty"), nil
	}
	if args.Dimensions < 1 || args.Dimensions > 4096 {
		return mcpsdk.NewToolResultError(fmt.Sprintf("dimensions must be between 1 and 4096, got %d", args.Dimensions)), nil
	}

	similarityFunction := strings.ToLower(args.SimilarityFunction)
	if similarityFunction == "" {
		similarityFunction = "cosine"
	}
	if !allowedSimilarityFunctions[similarityFunction] {
		return mcpsdk.NewToolResultError(fmt.Sprintf("similarityFunction must be cosine or euclidean, got %q", args.SimilarityFunction)), nil
	}

	quantizationType := args.QuantizationType
	if quantizationType == "" {
		quantizationType = "binary"
	}
	if !allowedQuantizationTypes[quantizationType] {
		return mcpsdk.NewToolResultError(fmt.Sprintf("quantizationType must be none, scalar, or binary, got %q", args.QuantizationType)), nil
	}

	hnswM := args.HnswM
	if hnswM == 0 {
		hnswM = 16
	} else if hnswM < 1 {
		return mcpsdk.NewToolResultError(fmt.Sprintf("hnswM must be >= 1, got %d", args.HnswM)), nil
	}

	hnswEfConstruction := args.HnswEfConstruction
	if hnswEfConstruction == 0 {
		hnswEfConstruction = 100
	} else if hnswEfConstruction < 1 {
		return mcpsdk.NewToolResultError(fmt.Sprintf("hnswEfConstruction must be >= 1, got %d", args.HnswEfConstruction)), nil
	}

	name := args.Name
	if name == "" {
		name = generatedSchemaName()
	}

	var cypherBuilder strings.Builder
	fmt.Fprintf(&cypherBuilder, "CREATE VECTOR INDEX %s IF NOT EXISTS FOR %s ON %s.%s\n",
		quoteIdentifier(name), pattern, varName, quoteIdentifier(args.Property))
	if len(args.FilterableProperties) > 0 {
		fmt.Fprintf(&cypherBuilder, "WITH %s\n", bracketList(varName, args.FilterableProperties))
	}
	fmt.Fprintf(&cypherBuilder,
		"OPTIONS { indexConfig: { `vector.dimensions`: %d, `vector.similarity_function`: %s, `vector.quantization.type`: %s, `vector.hnsw.m`: %d, `vector.hnsw.ef_construction`: %d } }",
		args.Dimensions, quoteStringLiteral(similarityFunction), quoteStringLiteral(quantizationType), hnswM, hnswEfConstruction)
	cypherStr := cypherBuilder.String()

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	slog.Info("creating vector index", "name", name, "entityType", args.EntityType)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to create vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	indexInfo, err := fetchIndexByName(execCtx, deps, name)
	if err != nil {
		slog.Error("failed to fetch created vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := CreateVectorIndexOutput{
		Index: indexInfo,
		Config: VectorIndexConfig{
			Dimensions:         args.Dimensions,
			SimilarityFunction: similarityFunction,
			QuantizationType:   quantizationType,
			HnswM:              hnswM,
			HnswEfConstruction: hnswEfConstruction,
		},
	}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize created vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode created vector index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// VectorIndexConfig echoes the resolved vector.* configuration actually
// applied — SHOW INDEXES has no columns for these, so this is the only
// place a caller can see the effective values (including defaults for
// anything left unset).
type VectorIndexConfig struct {
	Dimensions         int    `json:"dimensions"`
	SimilarityFunction string `json:"similarityFunction"`
	QuantizationType   string `json:"quantizationType"`
	HnswM              int    `json:"hnswM"`
	HnswEfConstruction int    `json:"hnswEfConstruction"`
}

// CreateVectorIndexOutput is the tool's structured output: the canonical,
// server-confirmed view of the index that was just created, plus the
// resolved vector config.
type CreateVectorIndexOutput struct {
	Index  IndexInfo         `json:"index"`
	Config VectorIndexConfig `json:"config"`
}
