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

// fakeQueryProfile is a minimal neo4j.QueryProfile implementation for
// testing ProfileNode construction without a real driver result. Each
// "reported" field controls whether its accessor returns ok=true.
type fakeQueryProfile struct {
	operator          string
	arguments         map[string]any
	identifiers       []string
	children          []neo4j.QueryProfile
	dbHits            *int64
	rows              *int64
	pageCacheHits     *int64
	pageCacheMisses   *int64
	pageCacheHitRatio *float64
	elapsed           *time.Duration
}

func (f *fakeQueryProfile) Operator() string               { return f.operator }
func (f *fakeQueryProfile) Arguments() map[string]any      { return f.arguments }
func (f *fakeQueryProfile) Identifiers() []string          { return f.identifiers }
func (f *fakeQueryProfile) Children() []neo4j.QueryProfile { return f.children }

func (f *fakeQueryProfile) DbHits() (int64, bool) { //nolint:staticcheck // method name must match neo4j.QueryProfile's own DbHits (not DBHits)
	if f.dbHits == nil {
		return 0, false
	}
	return *f.dbHits, true
}

func (f *fakeQueryProfile) Rows() (int64, bool) {
	if f.rows == nil {
		return 0, false
	}
	return *f.rows, true
}

func (f *fakeQueryProfile) PageCacheHits() (int64, bool) {
	if f.pageCacheHits == nil {
		return 0, false
	}
	return *f.pageCacheHits, true
}

func (f *fakeQueryProfile) PageCacheMisses() (int64, bool) {
	if f.pageCacheMisses == nil {
		return 0, false
	}
	return *f.pageCacheMisses, true
}

func (f *fakeQueryProfile) PageCacheHitRatio() (float64, bool) {
	if f.pageCacheHitRatio == nil {
		return 0, false
	}
	return *f.pageCacheHitRatio, true
}

func (f *fakeQueryProfile) Time() (time.Duration, bool) {
	if f.elapsed == nil {
		return 0, false
	}
	return *f.elapsed, true
}

func TestProfileCypherHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	defer ctrl.Finish()

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{DBService: nil}
		handler := cypher.ProfileCypherHandler(deps)
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
		handler := cypher.ProfileCypherHandler(deps)
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

	t.Run("PROFILE-prefixed query rejected", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ProfileCypherHandler(deps)
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
		if !strings.Contains(text.Text, "already prepends PROFILE") {
			t.Errorf("expected PROFILE double-prefix message, got: %s", text.Text)
		}
	})

	t.Run("EXPLAIN-prefixed query rejected", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService}
		handler := cypher.ProfileCypherHandler(deps)
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
		if !strings.Contains(text.Text, "explain-cypher") {
			t.Errorf("expected explain-cypher pointer, got: %s", text.Text)
		}
	})

	t.Run("successful profile returns rows and profile tree", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		dbHits := int64(42)
		rows := int64(1)
		profile := &fakeQueryProfile{operator: "ProduceResults", dbHits: &dbHits, rows: &rows}
		mockDB.EXPECT().
			ExecuteProfileQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(okResult(), profile, nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService, CypherMaxRows: 1000}
		handler := cypher.ProfileCypherHandler(deps)
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
		if !strings.Contains(text.Text, "\"dbHits\": 42") && !strings.Contains(text.Text, "\"dbHits\":42") {
			t.Errorf("expected reported dbHits in profile output, got: %s", text.Text)
		}
		if strings.Contains(text.Text, "pageCacheHits") {
			t.Errorf("un-reported pageCacheHits should be omitted, got: %s", text.Text)
		}
	})

	t.Run("execution error passthrough", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteProfileQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(nil, nil, errors.New("boom"))

		deps := &tools.ToolDependencies{DBService: mockDB, AnalyticsService: analyticsService, CypherMaxRows: 1000}
		handler := cypher.ProfileCypherHandler(deps)
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

	t.Run("timeout classification", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteProfileQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(nil, nil, context.DeadlineExceeded)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    5 * time.Second,
		}
		handler := cypher.ProfileCypherHandler(deps)
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
		text, ok := mcpsdk.AsTextContent(result.Content[0])
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "profile-cypher timed out") {
			t.Errorf("expected timeout message, got: %s", text.Text)
		}
	})
}
