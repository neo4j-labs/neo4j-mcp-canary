// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func ExplainCypherHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleExplainCypher(ctx, request, deps)
	}
}

func handleExplainCypher(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "Database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args ExplainCypherInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	Query := args.Query
	Params := args.Params

	if Query == "" {
		errMessage := "Query parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	// explain-cypher prepends EXPLAIN itself; a caller-supplied EXPLAIN or
	// PROFILE prefix would either double up (server error) or change what's
	// actually being asked for, so reject both up front with a targeted
	// message rather than letting the driver's "conflicting execution modes"
	// error leak through.
	switch database.FirstKeyword(Query) {
	case "EXPLAIN":
		return mcpsdk.NewToolResultError(
			"explain-cypher already prepends EXPLAIN to your query. Remove the leading EXPLAIN keyword and retry.",
		), nil
	case "PROFILE":
		return mcpsdk.NewToolResultError(
			"explain-cypher only plans queries, it does not execute them. Remove the leading PROFILE keyword and retry, " +
				"or use profile-cypher if you want the query to actually run and return profiling statistics.",
		), nil
	}

	slog.Info("explaining cypher query", "query", Query)

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	plan, err := deps.DBService.ExplainQuery(execCtx, Query, Params)
	if err != nil {
		slog.Error("error explaining cypher query", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	if plan == nil {
		return mcpsdk.NewToolResultError(
			"explain-cypher: the planner did not return a plan for this statement " +
				"(uncommon for administrative commands the planner doesn't model as a regular query plan).",
		), nil
	}

	node := buildPlanNode(plan)
	canonicalJSON, err := json.Marshal(node)
	if err != nil {
		slog.Error("error formatting explain-cypher plan", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(canonicalJSON), deps.OutputFormat)
	if err != nil {
		slog.Error("error encoding explain-cypher plan", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(canonicalJSON)), nil
}

// --- Output types ---

// PlanNode mirrors neo4j.Plan's shape as a JSON-tagged recursive struct —
// the same hand-rolled approach get-schema's SchemaItem/SchemaDetail use.
// Shared with profile_cypher_handler.go's ProfileNode, which carries the
// same fields plus PROFILE's runtime counters.
type PlanNode struct {
	Operator    string         `json:"operator"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	Identifiers []string       `json:"identifiers,omitempty"`
	Children    []PlanNode     `json:"children,omitempty"`
}

func buildPlanNode(plan neo4j.Plan) PlanNode {
	children := plan.Children()
	converted := make([]PlanNode, 0, len(children))
	for _, c := range children {
		converted = append(converted, buildPlanNode(c))
	}
	return PlanNode{
		Operator:    plan.Operator(),
		Arguments:   plan.Arguments(),
		Identifiers: plan.Identifiers(),
		Children:    converted,
	}
}
