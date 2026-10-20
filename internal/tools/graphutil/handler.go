// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
)

// Query is what a tool's build function produces: the Cypher (without the
// version prefix, which the handler adds) and its bound parameters.
type Query struct {
	Cypher string
	Params map[string]any
	// Write routes execution to a write transaction. Tools that are
	// read-only in every mode never set it.
	Write bool
	// ErrorHint is appended to a database error so the caller learns how to
	// fix the most likely cause.
	ErrorHint string
}

// DropNullProperties removes null-valued entries from every "properties" map
// in a result envelope. A requested property a node or relationship doesn't
// have comes back from Cypher as null; dropping it saves tokens and avoids
// implying the property exists. Nulls elsewhere (such as a refusal's
// message) are kept — they carry meaning. Numbers are decoded as
// json.Number so large integers keep their precision.
func DropNullProperties(canonicalJSON string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(canonicalJSON))
	dec.UseNumber()
	var resp database.CypherResponse
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("decoding results: %w", err)
	}
	for _, row := range resp.Rows {
		dropNulls(row)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("encoding results: %w", err)
	}
	return string(out), nil
}

func dropNulls(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if props, ok := child.(map[string]any); ok && k == "properties" {
				for pk, pv := range props {
					if pv == nil {
						delete(props, pk)
					}
				}
			}
			dropNulls(child)
		}
	case []any:
		for _, child := range x {
			dropNulls(child)
		}
	}
}

// OutputSchema is the structured-output schema shared by every graph tool:
// the standard rows/truncation envelope.
var OutputSchema = mcpsdk.MustOutputSchemaFor[database.CypherResponse]()

// cypherVersionPrefix pins every graph tool's statement to Cypher 25
// regardless of the database's default Cypher version.
const cypherVersionPrefix = "CYPHER 25\n"

// NewHandler builds a tool handler from a pure build function: it binds the
// request's arguments into T, asks build for the Query, runs it with the
// configured row/byte caps and timeout, and renders the result in the
// configured output format. Splitting build from execution keeps each
// tool's Cypher unit-testable without a database.
func NewHandler[T any](deps *tools.ToolDependencies, toolName string, build func(*T) (Query, error)) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if deps.DBService == nil {
			slog.Error("database service is not initialized", "tool", toolName)
			return mcpsdk.NewToolResultError("database service is not initialized"), nil
		}

		var args T
		if err := checkArguments(toolName, request, reflect.TypeOf(args)); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := request.BindArguments(&args); err != nil {
			slog.Error("error binding arguments", "tool", toolName, "error", err)
			return mcpsdk.NewToolResultError(fmt.Sprintf("%s: invalid arguments: %v. Check the argument names and types against the tool's input schema.", toolName, err)), nil
		}

		q, err := build(&args)
		if err != nil {
			return mcpsdk.NewToolResultError(fmt.Sprintf("%s: %v", toolName, err)), nil
		}
		cypherText := cypherVersionPrefix + q.Cypher

		execCtx := ctx
		if deps.CypherTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
			defer cancel()
		}

		slog.Info("executing graph tool", "tool", toolName, "write", q.Write)
		var result *database.QueryResult
		if q.Write {
			result, err = deps.DBService.ExecuteWriteQueryStreaming(execCtx, cypherText, q.Params, deps.CypherMaxRows, deps.CypherMaxBytes)
		} else {
			result, err = deps.DBService.ExecuteReadQueryStreaming(execCtx, cypherText, q.Params, deps.CypherMaxRows, deps.CypherMaxBytes)
		}
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return mcpsdk.NewToolResultError(fmt.Sprintf(
					"%s timed out: execution exceeded the configured %s limit. Narrow the request with more specific labels, match or filters, lower maxDepth, or lower limit, then retry.",
					toolName, deps.CypherTimeout)), nil
			case errors.Is(err, context.Canceled):
				return mcpsdk.NewToolResultError(toolName + " cancelled: execution was cancelled before completion"), nil
			}
			slog.Error("error executing graph tool", "tool", toolName, "error", err)
			if strings.Contains(err.Error(), "OutOfMemory") {
				return mcpsdk.NewToolResultError(fmt.Sprintf(
					"%s needed more memory than the server allows for a single query. Narrow the request: name relationshipTypes, lower maxDepth or limit, use more specific labels/match/filters, and avoid starting from or passing through very highly connected nodes (get-node shows a node's relationship counts). Retrying the same request will fail the same way.",
					toolName)), nil
			}
			msg := fmt.Sprintf("%s failed: %v", toolName, err)
			if q.ErrorHint != "" {
				msg += ". " + q.ErrorHint
			}
			return mcpsdk.NewToolResultError(msg), nil
		}

		canonicalJSON, err := deps.DBService.QueryResultToJSON(result)
		if err != nil {
			slog.Error("error formatting results", "tool", toolName, "error", err)
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		canonicalJSON, err = DropNullProperties(canonicalJSON)
		if err != nil {
			slog.Error("error trimming results", "tool", toolName, "error", err)
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		response, err := tools.EncodeOutput(canonicalJSON, deps.OutputFormat)
		if err != nil {
			slog.Error("error encoding results", "tool", toolName, "error", err)
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(canonicalJSON)), nil
	}
}
