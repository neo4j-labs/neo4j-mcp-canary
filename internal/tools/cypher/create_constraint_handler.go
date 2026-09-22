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

// showConstraintByNameQuery fetches back the canonical row for a single
// constraint by name right after creating it, so the response can echo the
// server's own view (type, entityType, labelsOrTypes, properties, and any
// owned index) rather than restating what the caller asked for.
const showConstraintByNameQuery = `SHOW CONSTRAINTS YIELD id, name, type, entityType, labelsOrTypes, properties, ownedIndex, propertyType WHERE name = $name`

func CreateConstraintHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleCreateConstraint(ctx, request, deps)
	}
}

func handleCreateConstraint(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args CreateConstraintInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	pattern, varName, err := entityPattern(args.EntityType, args.Label, args.RelationshipType)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	switch args.ConstraintType {
	case "PROPERTY_EXISTENCE":
		if len(args.Properties) != 1 {
			errMessage := fmt.Sprintf("constraintType PROPERTY_EXISTENCE requires exactly 1 property (existence constraints have no composite form), got %d", len(args.Properties))
			return mcpsdk.NewToolResultError(errMessage), nil
		}
	case "UNIQUENESS", "KEY":
		if len(args.Properties) == 0 {
			errMessage := fmt.Sprintf("constraintType %s requires at least 1 property", args.ConstraintType)
			return mcpsdk.NewToolResultError(errMessage), nil
		}
	default:
		errMessage := fmt.Sprintf("constraintType must be UNIQUENESS, KEY, or PROPERTY_EXISTENCE, got %q", args.ConstraintType)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	name := args.Name
	if name == "" {
		name = generatedSchemaName()
	}

	var cypherStr string
	switch args.ConstraintType {
	case "UNIQUENESS":
		cypherStr = fmt.Sprintf("CREATE CONSTRAINT %s IF NOT EXISTS FOR %s REQUIRE %s IS UNIQUE",
			quoteIdentifier(name), pattern, parenList(varName, args.Properties))
	case "KEY":
		keyClause := "IS NODE KEY"
		if args.EntityType == "RELATIONSHIP" {
			keyClause = "IS RELATIONSHIP KEY"
		}
		cypherStr = fmt.Sprintf("CREATE CONSTRAINT %s IF NOT EXISTS FOR %s REQUIRE %s %s",
			quoteIdentifier(name), pattern, parenList(varName, args.Properties), keyClause)
	case "PROPERTY_EXISTENCE":
		cypherStr = fmt.Sprintf("CREATE CONSTRAINT %s IF NOT EXISTS FOR %s REQUIRE %s.%s IS NOT NULL",
			quoteIdentifier(name), pattern, varName, quoteIdentifier(args.Properties[0]))
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	slog.Info("creating constraint", "name", name, "constraintType", args.ConstraintType)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to create constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	records, err := deps.DBService.ExecuteReadQuery(execCtx, showConstraintByNameQuery, map[string]any{"name": name})
	if err != nil {
		slog.Error("failed to fetch created constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	if len(records) == 0 {
		errMessage := fmt.Sprintf("constraint %q was created but could not be found afterward", name)
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	constraints, err := processConstraints(records)
	if err != nil {
		slog.Error("failed to process created constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := CreateConstraintOutput{Constraint: constraints[0]}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize created constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode created constraint", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// CreateConstraintOutput is the tool's structured output: the canonical,
// server-confirmed view of the constraint that was just created, in the same
// shape ListConstraintsAndIndexesHandler's ConstraintInfo already reports.
type CreateConstraintOutput struct {
	Constraint ConstraintInfo `json:"constraint"`
}
