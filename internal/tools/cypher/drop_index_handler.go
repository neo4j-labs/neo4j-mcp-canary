// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

// existsIndexByNameQuery is used as a best-effort existence check before the
// DROP itself, so the response can report whether the name was actually
// present rather than just "the DROP IF EXISTS didn't error".
const existsIndexByNameQuery = `SHOW INDEXES YIELD name WHERE name = $name`

func DropIndexHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleDropIndex(ctx, request, deps)
	}
}

func handleDropIndex(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args DropIndexInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if args.Name == "" {
		errMessage := "name parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	records, err := deps.DBService.ExecuteReadQuery(execCtx, existsIndexByNameQuery, map[string]any{"name": args.Name})
	if err != nil {
		slog.Error("failed to check index existence", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	existed := len(records) > 0

	cypherStr := fmt.Sprintf("DROP INDEX %s IF EXISTS", quoteIdentifier(args.Name))

	slog.Info("dropping index", "name", args.Name, "existed", existed)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to drop index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := DropIndexOutput{Name: args.Name, Existed: existed}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize drop-index result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode drop-index result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// DropIndexOutput is the tool's structured output: the name that was
// dropped, and whether an index with that name actually existed beforehand.
type DropIndexOutput struct {
	Name    string `json:"name"`
	Existed bool   `json:"existed"`
}
