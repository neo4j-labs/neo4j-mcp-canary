// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package feedback_test

import (
	"context"
	"strings"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	amocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/feedback"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/mock/gomock"
)

func requestWithFeedback(text string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{"feedback": text},
		},
	}
}

func TestGiveFeedbackHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("emits a feedback event when analytics is enabled", func(t *testing.T) {
		analyticsService := amocks.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Return(true)
		wantEvent := analytics.TrackEvent{Event: "MCP-NEO4J-CANARY_FEEDBACK"}
		analyticsService.EXPECT().NewFeedbackEvent("This tool is great!").Return(wantEvent)
		analyticsService.EXPECT().EmitEvent(wantEvent)

		deps := &tools.ToolDependencies{AnalyticsService: analyticsService}
		handler := feedback.GiveFeedbackHandler(deps)

		result, err := handler(context.Background(), requestWithFeedback("This tool is great!"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected a success result, got: %+v", result)
		}
	})

	t.Run("does not emit when analytics is disabled, but still succeeds", func(t *testing.T) {
		analyticsService := amocks.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Return(false)
		// No NewFeedbackEvent/EmitEvent expectations: gomock fails the test if
		// either is called when telemetry is disabled.

		deps := &tools.ToolDependencies{AnalyticsService: analyticsService}
		handler := feedback.GiveFeedbackHandler(deps)

		result, err := handler(context.Background(), requestWithFeedback("Some feedback"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected a success result even with telemetry disabled, got: %+v", result)
		}
	})

	t.Run("rejects empty feedback", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := feedback.GiveFeedbackHandler(deps)

		result, err := handler(context.Background(), requestWithFeedback(""))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected an error result for empty feedback, got: %+v", result)
		}
	})

	t.Run("rejects feedback over the 300 character limit", func(t *testing.T) {
		deps := &tools.ToolDependencies{}
		handler := feedback.GiveFeedbackHandler(deps)

		tooLong := strings.Repeat("a", 301)
		result, err := handler(context.Background(), requestWithFeedback(tooLong))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected an error result for over-limit feedback, got: %+v", result)
		}
	})

	t.Run("accepts feedback at exactly the 300 character limit", func(t *testing.T) {
		analyticsService := amocks.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Return(false)

		deps := &tools.ToolDependencies{AnalyticsService: analyticsService}
		handler := feedback.GiveFeedbackHandler(deps)

		exactly300 := strings.Repeat("a", 300)
		result, err := handler(context.Background(), requestWithFeedback(exactly300))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected a success result for exactly-300-char feedback, got: %+v", result)
		}
	})
}
