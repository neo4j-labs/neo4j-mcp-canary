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

func SetVectorPropertyHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleSetVectorProperty(ctx, request, deps)
	}
}

func handleSetVectorProperty(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args SetVectorPropertyInput
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

	if len(args.Filters) < 1 {
		return mcpsdk.NewToolResultError("at least one filter is required, to avoid silently overwriting every matching entity's vector"), nil
	}
	if args.VectorProperty == "" {
		return mcpsdk.NewToolResultError("vectorProperty is required and cannot be empty"), nil
	}
	if len(args.Vector) == 0 {
		return mcpsdk.NewToolResultError("vector is required and cannot be empty"), nil
	}

	whereClause, filterParams, err := buildFilterClauses(varName, args.Filters)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	procedure := "db.create.setNodeVectorProperty"
	if args.EntityType == "RELATIONSHIP" {
		procedure = "db.create.setRelationshipVectorProperty"
	}

	var cypherBuilder strings.Builder
	fmt.Fprintf(&cypherBuilder, "MATCH %s\n", pattern)
	fmt.Fprintf(&cypherBuilder, "WHERE %s\n", whereClause)
	fmt.Fprintf(&cypherBuilder, "CALL %s(%s, $vectorProperty, $vector)\n", procedure, varName)
	fmt.Fprintf(&cypherBuilder, "RETURN count(%s) AS updated", varName)
	cypherStr := cypherBuilder.String()

	params := map[string]any{"vectorProperty": args.VectorProperty, "vector": args.Vector}
	for k, v := range filterParams {
		params[k] = v
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	slog.Info("setting vector property", "entityType", args.EntityType, "vectorProperty", args.VectorProperty)

	records, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, params)
	if err != nil {
		slog.Error("failed to set vector property", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	if len(records) == 0 {
		errMessage := "set-vector-property query returned no rows"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	updatedRaw, ok := records[0].Get("updated")
	if !ok {
		errMessage := "missing 'updated' column in set-vector-property result"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}
	updated, ok := updatedRaw.(int64)
	if !ok {
		errMessage := "invalid 'updated' column type in set-vector-property result"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	structuredOutput := SetVectorPropertyOutput{Updated: updated}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize set-vector-property result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode set-vector-property result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// SetVectorPropertyOutput is the tool's structured output: the number of
// entities whose vector property was updated.
type SetVectorPropertyOutput struct {
	Updated int64 `json:"updated"`
}
