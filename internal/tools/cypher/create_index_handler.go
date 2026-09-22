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

// showIndexByNameQuery fetches back the canonical row for a single index by
// name right after creating it.
const showIndexByNameQuery = `SHOW INDEXES YIELD id, name, state, populationPercent, type, entityType, labelsOrTypes, properties, indexProvider, owningConstraint WHERE name = $name`

func CreateIndexHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleCreateIndex(ctx, request, deps)
	}
}

func handleCreateIndex(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args CreateIndexInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	switch args.IndexType {
	case "RANGE", "TEXT", "POINT", "LOOKUP":
	case "VECTOR", "FULLTEXT":
		errMessage := fmt.Sprintf("indexType %q is not supported by create-index (needs index-specific configuration this tool doesn't model)", args.IndexType)
		return mcpsdk.NewToolResultError(errMessage), nil
	default:
		errMessage := fmt.Sprintf("indexType must be one of RANGE, TEXT, POINT, LOOKUP, got %q", args.IndexType)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var pattern, varName string
	if args.IndexType == "LOOKUP" {
		if args.Label != "" || args.RelationshipType != "" || len(args.Properties) != 0 {
			errMessage := "LOOKUP indexes every label or relationship type automatically; label, relationshipType, and properties must not be set"
			return mcpsdk.NewToolResultError(errMessage), nil
		}
		switch args.EntityType {
		case "NODE":
			pattern, varName = "(n)", "n"
		case "RELATIONSHIP":
			pattern, varName = "()-[r]-()", "r"
		default:
			errMessage := fmt.Sprintf("entityType must be NODE or RELATIONSHIP, got %q", args.EntityType)
			return mcpsdk.NewToolResultError(errMessage), nil
		}
	} else {
		switch args.IndexType {
		case "TEXT", "POINT":
			if len(args.Properties) != 1 {
				errMessage := fmt.Sprintf("indexType %s requires exactly 1 property, got %d", args.IndexType, len(args.Properties))
				return mcpsdk.NewToolResultError(errMessage), nil
			}
		case "RANGE":
			if len(args.Properties) == 0 {
				errMessage := "indexType RANGE requires at least 1 property"
				return mcpsdk.NewToolResultError(errMessage), nil
			}
		}

		var err error
		pattern, varName, err = entityPattern(args.EntityType, args.Label, args.RelationshipType)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
	}

	name := args.Name
	if name == "" {
		name = generatedSchemaName()
	}

	var cypherStr string
	switch args.IndexType {
	case "RANGE", "TEXT", "POINT":
		cypherStr = fmt.Sprintf("CREATE %s INDEX %s IF NOT EXISTS FOR %s ON %s",
			args.IndexType, quoteIdentifier(name), pattern, parenList(varName, args.Properties))
	case "LOOKUP":
		if args.EntityType == "NODE" {
			cypherStr = fmt.Sprintf("CREATE LOOKUP INDEX %s IF NOT EXISTS FOR (n) ON EACH labels(n)", quoteIdentifier(name))
		} else {
			cypherStr = fmt.Sprintf("CREATE LOOKUP INDEX %s IF NOT EXISTS FOR ()-[r]-() ON EACH type(r)", quoteIdentifier(name))
		}
	}

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	slog.Info("creating index", "name", name, "indexType", args.IndexType)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to create index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	records, err := deps.DBService.ExecuteReadQuery(execCtx, showIndexByNameQuery, map[string]any{"name": name})
	if err != nil {
		slog.Error("failed to fetch created index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	if len(records) == 0 {
		errMessage := fmt.Sprintf("index %q was created but could not be found afterward", name)
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	indexes, err := processIndexes(records)
	if err != nil {
		slog.Error("failed to process created index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := CreateIndexOutput{Index: indexes[0]}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize created index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode created index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// CreateIndexOutput is the tool's structured output: the canonical,
// server-confirmed view of the index that was just created, in the same
// shape ListConstraintsAndIndexesHandler's IndexInfo already reports.
type CreateIndexOutput struct {
	Index IndexInfo `json:"index"`
}
