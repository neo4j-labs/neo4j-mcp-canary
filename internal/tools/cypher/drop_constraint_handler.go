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

// existsConstraintByNameQuery is used as a best-effort existence check before
// the DROP itself, so the response can report whether the name was actually
// present rather than just "the DROP IF EXISTS didn't error".
const existsConstraintByNameQuery = `SHOW CONSTRAINTS YIELD name WHERE name = $name`

func DropConstraintHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleDropConstraint(ctx, request, deps)
	}
}

func handleDropConstraint(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args DropConstraintInput
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

	records, err := deps.DBService.ExecuteReadQuery(execCtx, existsConstraintByNameQuery, map[string]any{"name": args.Name})
	if err != nil {
		slog.Error("failed to check constraint existence", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	existed := len(records) > 0

	cypherStr := fmt.Sprintf("DROP CONSTRAINT %s IF EXISTS", quoteIdentifier(args.Name))

	slog.Info("dropping constraint", "name", args.Name, "existed", existed)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to drop constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := DropConstraintOutput{Name: args.Name, Existed: existed}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize drop-constraint result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode drop-constraint result", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// DropConstraintOutput is the tool's structured output: the name that was
// dropped, and whether a constraint with that name actually existed
// beforehand.
type DropConstraintOutput struct {
	Name    string `json:"name"`
	Existed bool   `json:"existed"`
}
