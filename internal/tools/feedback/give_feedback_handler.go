// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package feedback

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
)

// GiveFeedbackHandler returns the MCP handler for the give-feedback tool.
func GiveFeedbackHandler(deps *tools.ToolDependencies) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGiveFeedback(ctx, request, deps)
	}
}

func handleGiveFeedback(_ context.Context, request mcp.CallToolRequest, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	var args GiveFeedbackInput
	if err := request.BindArguments(&args); err != nil {
		slog.Error("error binding arguments", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	feedback := args.Feedback
	if feedback == "" {
		errMessage := "feedback parameter is required and cannot be empty"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	// The schema already advertises maxLength, but a client is not required
	// to enforce it before sending a request — validate here too rather than
	// silently truncating, which could change the meaning of the feedback.
	if len(feedback) > feedbackMaxLength {
		errMessage := fmt.Sprintf(
			"feedback is %d characters, which exceeds the %d character limit. Shorten it and retry.",
			len(feedback), feedbackMaxLength,
		)
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	// Feedback is only recorded when analytics/telemetry is enabled, same as
	// every other event in this codebase — but the tool call itself always
	// succeeds from the caller's perspective, regardless of whether
	// telemetry is on. Whether feedback made it to Mixpanel is an
	// operator-configured detail, not something the calling agent needs to
	// reason about.
	if deps.AnalyticsService != nil && deps.AnalyticsService.IsEnabled() {
		deps.AnalyticsService.EmitEvent(deps.AnalyticsService.NewFeedbackEvent(feedback))
	}

	slog.Info("received feedback", "feedback", feedback)
	return mcp.NewToolResultText("Thank you for your feedback."), nil
}
