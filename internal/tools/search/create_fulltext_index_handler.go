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

func CreateFullTextIndexHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleCreateFullTextIndex(ctx, request, deps)
	}
}

func handleCreateFullTextIndex(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args CreateFullTextIndexInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	switch args.EntityType {
	case "NODE":
		if len(args.Labels) == 0 {
			return mcpsdk.NewToolResultError("labels is required (non-empty) when entityType is NODE"), nil
		}
		if len(args.RelationshipTypes) != 0 {
			return mcpsdk.NewToolResultError("relationshipTypes must not be set when entityType is NODE"), nil
		}
	case "RELATIONSHIP":
		if len(args.RelationshipTypes) == 0 {
			return mcpsdk.NewToolResultError("relationshipTypes is required (non-empty) when entityType is RELATIONSHIP"), nil
		}
		if len(args.Labels) != 0 {
			return mcpsdk.NewToolResultError("labels must not be set when entityType is RELATIONSHIP"), nil
		}
	default:
		return mcpsdk.NewToolResultError(fmt.Sprintf("entityType must be NODE or RELATIONSHIP, got %q", args.EntityType)), nil
	}

	labelsOrTypes := args.Labels
	if args.EntityType == "RELATIONSHIP" {
		labelsOrTypes = args.RelationshipTypes
	}
	pattern, varName, err := entityPatternMulti(args.EntityType, labelsOrTypes)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	if len(args.Properties) == 0 {
		return mcpsdk.NewToolResultError("properties is required and cannot be empty"), nil
	}

	name := args.Name
	if name == "" {
		name = generatedSchemaName()
	}

	var cypherBuilder strings.Builder
	fmt.Fprintf(&cypherBuilder, "CREATE FULLTEXT INDEX %s IF NOT EXISTS FOR %s ON EACH %s",
		quoteIdentifier(name), pattern, bracketList(varName, args.Properties))
	if args.Analyzer != "" || args.EventuallyConsistent {
		cypherBuilder.WriteString("\nOPTIONS { indexConfig: { ")
		var configParts []string
		if args.Analyzer != "" {
			configParts = append(configParts, fmt.Sprintf("`fulltext.analyzer`: %s", quoteStringLiteral(args.Analyzer)))
		}
		if args.EventuallyConsistent {
			configParts = append(configParts, "`fulltext.eventually_consistent`: true")
		}
		cypherBuilder.WriteString(strings.Join(configParts, ", "))
		cypherBuilder.WriteString(" } }")
	}
	cypherStr := cypherBuilder.String()

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	slog.Info("creating fulltext index", "name", name, "entityType", args.EntityType)

	if _, err := deps.DBService.ExecuteWriteQuery(execCtx, cypherStr, nil); err != nil {
		slog.Error("failed to create fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	indexInfo, err := fetchIndexByName(execCtx, deps, name)
	if err != nil {
		slog.Error("failed to fetch created fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := CreateFullTextIndexOutput{Index: indexInfo}
	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize created fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode created fulltext index", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// CreateFullTextIndexOutput is the tool's structured output: the canonical,
// server-confirmed view of the index that was just created.
type CreateFullTextIndexOutput struct {
	Index IndexInfo `json:"index"`
}
