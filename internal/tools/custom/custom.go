// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package custom turns operator-defined config.CustomTool entries into MCP
// tools that run a fixed Cypher statement with bound parameters.
package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const neo4jAccessModeErrorCode = "Neo.ClientError.Statement.AccessMode"

// Spec builds the advertised tool: the operator's description and typed
// parameters, annotated read-only or not according to the declared access.
func Spec(def config.CustomTool) mcpsdk.Tool {
	readOnly := def.Access == config.CustomToolAccessRead
	opts := []mcpsdk.ToolOption{mcpsdk.WithDescription(def.Description)}
	for _, p := range def.Params {
		popts := []mcpsdk.PropertyOption{mcpsdk.Description(p.Description)}
		// A parameter with a default is always satisfiable, so the caller
		// is not told it must supply it.
		if p.Required && p.Default == nil {
			popts = append(popts, mcpsdk.Required())
		}
		if p.Default != nil {
			popts = append(popts, mcpsdk.DefaultValue(p.Default))
		}
		if len(p.Enum) > 0 {
			popts = append(popts, mcpsdk.EnumValues(p.Enum...))
		}
		switch p.Type {
		case config.CustomParamInteger:
			opts = append(opts, mcpsdk.WithInteger(p.Name, popts...))
		case config.CustomParamBoolean:
			opts = append(opts, mcpsdk.WithBoolean(p.Name, popts...))
		case config.CustomParamNumber:
			opts = append(opts, mcpsdk.WithNumber(p.Name, popts...))
		default:
			opts = append(opts, mcpsdk.WithString(p.Name, popts...))
		}
	}
	opts = append(opts,
		mcpsdk.WithTitleAnnotation(def.Name),
		mcpsdk.WithReadOnlyHintAnnotation(readOnly),
		mcpsdk.WithDestructiveHintAnnotation(!readOnly),
		mcpsdk.WithIdempotentHintAnnotation(readOnly),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool(def.Name, opts...)
}

// BuildParams validates the caller's arguments against the tool's declared
// parameters and returns the Cypher parameter map: unknown names and
// missing required values are rejected, defaults filled in, values coerced
// to their declared type and checked against any enum.
func BuildParams(def config.CustomTool, args map[string]any) (map[string]any, error) {
	declared := make(map[string]config.CustomToolParam, len(def.Params))
	for _, p := range def.Params {
		declared[p.Name] = p
	}
	for name := range args {
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf("unknown argument %q; valid arguments are %v", name, paramNames(def.Params))
		}
	}

	params := make(map[string]any, len(def.Params))
	for _, p := range def.Params {
		raw, given := args[p.Name]
		if !given || raw == nil {
			switch {
			case p.Default != nil:
				raw = p.Default
			case p.Required:
				return nil, fmt.Errorf("missing required argument %q (%s: %s)", p.Name, p.Type, p.Description)
			default:
				params[p.Name] = nil
				continue
			}
		}
		v, err := config.CoerceCustomParam(p.Type, raw)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", p.Name, err)
		}
		if len(p.Enum) > 0 && !enumContains(p, v) {
			return nil, fmt.Errorf("argument %q must be one of %v, got %v", p.Name, p.Enum, v)
		}
		params[p.Name] = v
	}
	return params, nil
}

// Handler runs the tool's statement. A read tool is classified up front and
// executed in a read transaction, so a statement that writes fails instead
// of silently modifying the graph.
func Handler(deps *tools.ToolDependencies, def config.CustomTool) mcpsdk.ToolHandlerFunc {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if deps.DBService == nil {
			slog.Error("database service is not initialized", "tool", def.Name)
			return mcpsdk.NewToolResultError("database service is not initialized"), nil
		}

		args := map[string]any{}
		if err := request.BindArguments(&args); err != nil {
			return mcpsdk.NewToolResultError(fmt.Sprintf("%s: invalid arguments: %v. Check the argument names and types against the tool's input schema.", def.Name, err)), nil
		}
		params, err := BuildParams(def, args)
		if err != nil {
			return mcpsdk.NewToolResultError(fmt.Sprintf("%s: %v. Fix the argument and retry.", def.Name, err)), nil
		}

		execCtx := ctx
		if deps.CypherTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
			defer cancel()
		}

		readOnly := def.Access == config.CustomToolAccessRead
		if readOnly {
			queryType, err := deps.DBService.GetQueryType(execCtx, def.Statement, params)
			if err != nil {
				slog.Error("error classifying custom tool statement", "tool", def.Name, "error", err)
				return mcpsdk.NewToolResultError(fmt.Sprintf("%s failed: %v", def.Name, err)), nil
			}
			if queryType != neo4j.QueryTypeReadOnly {
				return mcpsdk.NewToolResultError(readAccessViolation(def.Name)), nil
			}
		}

		slog.Info("executing custom tool", "tool", def.Name, "readOnly", readOnly)
		var result *database.QueryResult
		if readOnly {
			result, err = deps.DBService.ExecuteReadQueryStreaming(execCtx, def.Statement, params, deps.CypherMaxRows, deps.CypherMaxBytes)
		} else {
			result, err = deps.DBService.ExecuteWriteQueryStreaming(execCtx, def.Statement, params, deps.CypherMaxRows, deps.CypherMaxBytes)
		}
		if err != nil {
			var neo4jErr *neo4j.Neo4jError
			switch {
			case errors.As(err, &neo4jErr) && neo4jErr.Code == neo4jAccessModeErrorCode:
				return mcpsdk.NewToolResultError(readAccessViolation(def.Name)), nil
			case errors.Is(err, context.DeadlineExceeded):
				return mcpsdk.NewToolResultError(fmt.Sprintf("%s timed out: execution exceeded the configured %s limit. Retry with more selective arguments; if it keeps timing out the tool's statement needs tuning.", def.Name, deps.CypherTimeout)), nil
			case errors.Is(err, context.Canceled):
				return mcpsdk.NewToolResultError(def.Name + " cancelled: execution was cancelled before completion"), nil
			}
			slog.Error("error executing custom tool", "tool", def.Name, "error", err)
			return mcpsdk.NewToolResultError(fmt.Sprintf("%s failed: %v", def.Name, err)), nil
		}

		canonicalJSON, err := deps.DBService.QueryResultToJSON(result)
		if err != nil {
			slog.Error("error formatting custom tool results", "tool", def.Name, "error", err)
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		response, err := tools.EncodeOutput(canonicalJSON, deps.OutputFormat)
		if err != nil {
			slog.Error("error encoding custom tool results", "tool", def.Name, "error", err)
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(canonicalJSON)), nil
	}
}

func readAccessViolation(name string) string {
	return fmt.Sprintf("%s is declared access: read but its statement writes to the graph, so it was not run. The operator must change the tool's access to \"readwrite\" or fix the statement; retrying will fail the same way.", name)
}

func paramNames(ps []config.CustomToolParam) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

func enumContains(p config.CustomToolParam, v any) bool {
	return slices.ContainsFunc(p.Enum, func(e any) bool {
		c, err := config.CoerceCustomParam(p.Type, e)
		return err == nil && c == v
	})
}
