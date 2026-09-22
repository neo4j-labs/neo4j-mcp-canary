// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package gds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

const listGdsProceduresQuery = `
CALL gds.list() YIELD name, description, signature, type
WHERE type = "procedure"
AND name CONTAINS "stream"
AND NOT (name CONTAINS "estimate")
RETURN name, description, signature, type`

func ListGdsProceduresHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleListGdsProcedures(ctx, deps)
	}
}

func handleListGdsProcedures(ctx context.Context, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "Database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	records, err := deps.DBService.ExecuteReadQuery(ctx, listGdsProceduresQuery, nil)
	if err != nil {
		formattedErrorMessage := fmt.Errorf("failed to execute list-gds-procedure query: %v. Ensure that the Graph Data Science (GDS) library is installed and properly configured in your Neo4j database", err)
		slog.Error("failed to execute list gds procedures query", "error", err)
		return mcpsdk.NewToolResultError(formattedErrorMessage.Error()), nil
	}

	canonicalJSON, err := deps.DBService.Neo4jRecordsToJSON(records)
	if err != nil {
		slog.Error("failed to format list-gds-procedures results to JSON", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	wrappedJSON, err := json.Marshal(ListGdsProceduresOutput{Procedures: json.RawMessage(canonicalJSON)})
	if err != nil {
		slog.Error("failed to wrap list-gds-procedures results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(wrappedJSON), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode list-gds-procedures results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(wrappedJSON)), nil
}

// ListGdsProceduresOutput is the top-level structured-output shape for
// list-gds-procedures: an object wrapping the procedure list, since MCP
// requires structuredContent to be a JSON object rather than a bare array —
// see listGdsProceduresOutputSchema.
type ListGdsProceduresOutput struct {
	Procedures json.RawMessage `json:"procedures"`
}
