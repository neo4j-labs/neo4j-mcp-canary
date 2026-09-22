// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func ProfileCypherHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleProfileCypher(ctx, request, deps)
	}
}

func handleProfileCypher(ctx context.Context, request *mcpsdk.CallToolRequest, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "Database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	var args ProfileCypherInput
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

	// profile-cypher prepends PROFILE itself; see explain-cypher's identical
	// guard for the same rationale (avoids a double-prefixed "conflicting
	// execution modes" driver error and, for a caller-supplied EXPLAIN,
	// avoids silently executing when the caller asked only to plan).
	switch database.FirstKeyword(Query) {
	case "PROFILE":
		return mcpsdk.NewToolResultError(
			"profile-cypher already prepends PROFILE to your query. Remove the leading PROFILE keyword and retry.",
		), nil
	case "EXPLAIN":
		return mcpsdk.NewToolResultError(
			"profile-cypher executes the query and returns runtime statistics. Remove the leading EXPLAIN keyword " +
				"and retry, or use explain-cypher if you only want the query plan without executing it.",
		), nil
	}

	slog.Info("profiling cypher query", "query", Query)

	execCtx := ctx
	if deps.CypherTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, deps.CypherTimeout)
		defer cancel()
	}

	result, profile, err := deps.DBService.ExecuteProfileQueryStreaming(execCtx, Query, Params, deps.CypherMaxRows, deps.CypherMaxBytes)
	if err != nil {
		// Same context-error classification as write-cypher — see
		// write_cypher_handler.go for the full rationale.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			errMessage := fmt.Sprintf(
				"profile-cypher timed out: query execution exceeded the configured %s limit. "+
					"Reduce the batch size, narrow the MATCH with a more selective WHERE, or use "+
					"apoc.periodic.iterate to process the mutation in chunks, then retry.",
				deps.CypherTimeout,
			)
			slog.Info("profile-cypher query timed out", "query", Query, "timeout", deps.CypherTimeout)
			return mcpsdk.NewToolResultError(errMessage), nil
		case errors.Is(err, context.Canceled):
			slog.Info("profile-cypher query cancelled", "query", Query)
			return mcpsdk.NewToolResultError("profile-cypher cancelled: query execution was cancelled before completion"), nil
		}
		slog.Error("error executing cypher query", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	canonicalJSON, err := deps.DBService.QueryResultToJSON(result)
	if err != nil {
		slog.Error("error formatting query results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	var base database.CypherResponse
	if err := json.Unmarshal([]byte(canonicalJSON), &base); err != nil {
		slog.Error("error decoding formatted query results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	resp := ProfileCypherResponse{CypherResponse: base}
	if profile != nil {
		resp.Profile = buildProfileNode(profile)
	}
	respJSON, err := json.Marshal(resp)
	if err != nil {
		slog.Error("error formatting profile-cypher response", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(respJSON), deps.OutputFormat)
	if err != nil {
		slog.Error("error encoding profile-cypher response", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(respJSON)), nil
}

// --- Output types ---

// ProfileCypherResponse embeds the same rows/truncation envelope
// read-cypher/write-cypher use (database.CypherResponse — encoding/json
// promotes anonymous embedded struct fields automatically) plus the
// profiled plan tree, so a client already parsing CypherResponse's rows/
// truncated/hint contract doesn't need a new level of nesting to find them.
type ProfileCypherResponse struct {
	database.CypherResponse
	Profile ProfileNode `json:"profile"`
}

// ProfileNode mirrors PlanNode's fields plus the runtime counters PROFILE
// adds over EXPLAIN. neo4j.QueryProfile's accessors return (value, bool);
// the bool says whether the server actually reported that stat for this
// operator. Pointer fields + omitempty let "not reported" (nil) read
// differently from "reported as zero".
type ProfileNode struct {
	Operator          string         `json:"operator"`
	Arguments         map[string]any `json:"arguments,omitempty"`
	Identifiers       []string       `json:"identifiers,omitempty"`
	DBHits            *int64         `json:"dbHits,omitempty"`
	Rows              *int64         `json:"rows,omitempty"`
	TimeMs            *float64       `json:"timeMs,omitempty"`
	PageCacheHits     *int64         `json:"pageCacheHits,omitempty"`
	PageCacheMisses   *int64         `json:"pageCacheMisses,omitempty"`
	PageCacheHitRatio *float64       `json:"pageCacheHitRatio,omitempty"`
	Children          []ProfileNode  `json:"children,omitempty"`
}

func buildProfileNode(profile neo4j.QueryProfile) ProfileNode {
	children := profile.Children()
	converted := make([]ProfileNode, 0, len(children))
	for _, c := range children {
		converted = append(converted, buildProfileNode(c))
	}
	node := ProfileNode{
		Operator:    profile.Operator(),
		Arguments:   profile.Arguments(),
		Identifiers: profile.Identifiers(),
		Children:    converted,
	}
	if v, ok := profile.DbHits(); ok {
		node.DBHits = &v
	}
	if v, ok := profile.Rows(); ok {
		node.Rows = &v
	}
	if v, ok := profile.Time(); ok {
		ms := float64(v) / float64(time.Millisecond)
		node.TimeMs = &ms
	}
	if v, ok := profile.PageCacheHits(); ok {
		node.PageCacheHits = &v
	}
	if v, ok := profile.PageCacheMisses(); ok {
		node.PageCacheMisses = &v
	}
	if v, ok := profile.PageCacheHitRatio(); ok {
		node.PageCacheHitRatio = &v
	}
	return node
}
