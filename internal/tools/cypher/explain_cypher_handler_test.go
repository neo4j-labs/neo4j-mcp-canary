// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// fakePlan is a minimal neo4j.Plan implementation for testing PlanNode
// construction without a real driver result.
type fakePlan struct {
	operator    string
	arguments   map[string]any
	identifiers []string
	children    []neo4j.Plan
}

func (f *fakePlan) Operator() string          { return f.operator }
func (f *fakePlan) Arguments() map[string]any { return f.arguments }
func (f *fakePlan) Identifiers() []string     { return f.identifiers }
func (f *fakePlan) Children() []neo4j.Plan    { return f.children }

func TestExplainCypherHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{DBService: nil}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "MATCH (n) RETURN n"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("empty query rejected", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": ""}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for empty query, got: %+v", result)
		}
	})

	t.Run("EXPLAIN-prefixed query rejected", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "EXPLAIN MATCH (n) RETURN n"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
		text, ok := mcpsdk.AsTextContent(result.Content[0])
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "already prepends EXPLAIN") {
			t.Errorf("expected EXPLAIN double-prefix message, got: %s", text.Text)
		}
	})

	t.Run("PROFILE-prefixed query rejected", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "PROFILE MATCH (n) RETURN n"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
		text, ok := mcpsdk.AsTextContent(result.Content[0])
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "profile-cypher") {
			t.Errorf("expected profile-cypher pointer, got: %s", text.Text)
		}
	})

	t.Run("successful explain returns plan tree", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		plan := &fakePlan{
			operator:    "ProduceResults",
			arguments:   map[string]any{"EstimatedRows": int64(1)},
			identifiers: []string{"n"},
			children: []neo4j.Plan{
				&fakePlan{operator: "AllNodesScan", arguments: map[string]any{"EstimatedRows": int64(100)}, identifiers: []string{"n"}},
			},
		}
		mockDB.EXPECT().
			ExplainQuery(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			Return(plan, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "MATCH (n) RETURN n"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
		text, ok := mcpsdk.AsTextContent(result.Content[0])
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "ProduceResults") || !strings.Contains(text.Text, "AllNodesScan") {
			t.Errorf("expected both operators in plan tree, got: %s", text.Text)
		}
	})

	t.Run("ExplainQuery error passthrough", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExplainQuery(gomock.Any(), "MATCH (n RETURN n", gomock.Nil()).
			Return(nil, errors.New("syntax error"))

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "MATCH (n RETURN n"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
	})

	t.Run("nil plan produces explanatory message", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExplainQuery(gomock.Any(), "SHOW DATABASES", gomock.Nil()).
			Return(nil, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "SHOW DATABASES"}},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for nil plan, got: %+v", result)
		}
	})

	t.Run("cypher timeout propagates to service via context", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		var seenCtx context.Context
		mockDB.EXPECT().
			ExplainQuery(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			DoAndReturn(func(ctx context.Context, _ string, _ map[string]any) (neo4j.Plan, error) {
				seenCtx = ctx
				return &fakePlan{operator: "ProduceResults"}, nil
			})

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherTimeout:    5 * time.Second,
		}
		handler := cypher.ExplainCypherHandler(deps)
		request := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParams{Arguments: map[string]any{"query": "MATCH (n) RETURN n"}},
		}
		if _, err := handler(context.Background(), request); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		deadline, ok := seenCtx.Deadline()
		if !ok {
			t.Fatal("expected context passed to ExplainQuery to carry a deadline")
		}
		if time.Until(deadline) > 5*time.Second {
			t.Errorf("deadline further out than configured timeout: %v", time.Until(deadline))
		}
	})
}
