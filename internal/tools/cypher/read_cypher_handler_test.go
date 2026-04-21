// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	amocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// okResult is a small helper that builds an empty, non-truncated QueryResult for
// tests that only care about the happy-path plumbing. The real row content is
// exercised separately in service_test.go; these handler tests deliberately keep
// record-level assertions out of scope.
func okResult() *database.QueryResult {
	return &database.QueryResult{Records: []*neo4j.Record{}, RowCount: 0, Truncated: false, MaxRows: 0}
}

func TestReadCypherHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := amocks.NewMockService(ctrl)
	// Analytics is a mock but most tests don't care about telemetry — they just
	// need IsEnabled() to be a no-op. A single AnyTimes() expectation returning
	// false lets the handler call through without each subtest having to stub
	// the call. Subtests that specifically verify telemetry emission use their
	// own mock with IsEnabled() returning true plus EmitEvent expectations.
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)
	// Note: Handlers no longer emit events directly - events are emitted via hooks in server.go
	defer ctrl.Finish()

	t.Run("successful cypher execution with parameters", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n:Person {name: $name}) RETURN n", map[string]any{"name": "Alice"}).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n:Person {name: $name}) RETURN n", map[string]any{"name": "Alice"}, 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[{"n":{"name":"Alice"}}],"rowCount":1,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query":  "MATCH (n:Person {name: $name}) RETURN n",
					"params": map[string]any{"name": "Alice"},
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if result == nil || result.IsError {
			t.Error("Expected success result")
		}
	})

	t.Run("successful cypher execution without parameters", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN count(n)", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN count(n)", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[{"count(n)":42}],"rowCount":1,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN count(n)",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if result == nil || result.IsError {
			t.Error("Expected success result")
		}
	})

	t.Run("invalid arguments binding", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		// Test with invalid argument structure that should cause BindArguments to fail
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: "invalid string instead of map",
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for invalid arguments")
		}
	})

	t.Run("missing required arguments", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		// The handler should NOT call ExecuteReadQuery when query is empty
		// No expectations set for mockDB since it shouldn't be called

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"invalid_field": "value",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		// Now the handler should return an error for empty query
		if result == nil || !result.IsError {
			t.Error("Expected error result for missing query parameter")
		}
	})

	t.Run("empty query parameter", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		// The handler should NOT call ExecuteReadQuery when query is empty
		// No expectations set for mockDB since it shouldn't be called

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		// Handler should return an error for empty query
		if result == nil || !result.IsError {
			t.Error("Expected error result for empty query parameter")
		}
	})

	t.Run("nil database service", func(t *testing.T) {
		deps := &tools.ToolDependencies{
			DBService:        nil,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for nil database service")
		}
	})
	t.Run("nil analytics service", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: nil,
		}

		handler := cypher.ReadCypherHandler(deps)
		result, err := handler(context.Background(), mcp.CallToolRequest{})

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for nil analytics service")
		}
	})

	t.Run("database query execution failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "INVALID CYPHER", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "INVALID CYPHER", gomock.Nil(), 1000, 0).
			Return(nil, errors.New("syntax error"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "INVALID CYPHER",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for query execution failure")
		}
	})

	t.Run("JSON formatting failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return("", errors.New("JSON marshaling failed"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)

		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for JSON formatting failure")
		}
	})

	t.Run("non-read query type returns error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "CREATE (n:Test)", gomock.Nil()).
			Return(neo4j.QueryTypeWriteOnly, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "CREATE (n:Test)",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for non-read query type")
		}
	})

	t.Run("explain query failure", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeUnknown, errors.New("driver error"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Errorf("Expected no error from handler, got: %v", err)
		}
		if result == nil || !result.IsError {
			t.Error("Expected error result for explain failure")
		}
	})

	// Truncation surfacing: when the streaming executor reports Truncated=true,
	// the handler must pass the result through to the formatter and return its JSON
	// verbatim. We don't re-assert the exact envelope shape here (that's covered in
	// service_json_test.go) — we only check that the wiring passes the truncated
	// result to QueryResultToJSON and that the response body carries the hint.
	t.Run("truncated result surfaces hint", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n:Company) RETURN n.name", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		truncated := &database.QueryResult{
			Records:          []*neo4j.Record{},
			RowCount:         1000,
			Truncated:        true,
			TruncationReason: database.TruncationReasonRows,
			MaxRows:          1000,
		}
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n:Company) RETURN n.name", gomock.Nil(), 1000, 0).
			Return(truncated, nil)
		mockDB.EXPECT().
			QueryResultToJSON(truncated).
			Return(`{"rows":[],"rowCount":1000,"truncated":true,"truncationReason":"rows","maxRows":1000,"hint":"Results were truncated at 1000 rows. Add a LIMIT clause or a more selective filter and retry for a complete result."}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n:Company) RETURN n.name",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result with truncated envelope, got %+v", result)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content on result")
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, `"truncated":true`) {
			t.Errorf("expected truncated=true in response, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "Add a LIMIT") {
			t.Errorf("expected LIMIT hint in response, got: %s", text.Text)
		}
	})

	// Byte-cap truncation surfacing: when streaming stops because a per-call byte
	// cap would have been exceeded (typically from wide-row responses — full nodes
	// with many properties), the handler should pass the result through unchanged.
	// The hint delivered to the agent steers it toward a narrower projection
	// rather than a smaller LIMIT because when rows are wide, lowering the LIMIT
	// proportionally is inefficient. We pin the "narrower projection" guidance
	// so the agent's retry loop has a concrete remediation.
	t.Run("truncated by bytes surfaces projection hint", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n:Company) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		truncated := &database.QueryResult{
			Records:          []*neo4j.Record{},
			RowCount:         425,
			Truncated:        true,
			TruncationReason: database.TruncationReasonBytes,
			MaxRows:          1000,
			MaxBytes:         900_000,
			ByteCount:        899_521,
		}
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n:Company) RETURN n", gomock.Nil(), 1000, 900_000).
			Return(truncated, nil)
		mockDB.EXPECT().
			QueryResultToJSON(truncated).
			Return(`{"rows":[],"rowCount":425,"truncated":true,"truncationReason":"bytes","maxBytes":900000,"hint":"Results were truncated because the response size would have exceeded 900000 bytes. Each row is large — try projecting fewer properties (for example RETURN n.id, n.name instead of RETURN n) or filter down to fewer rows, then retry."}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherMaxBytes:   900_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n:Company) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result with truncated envelope, got %+v", result)
		}
		text, _ := result.Content[0].(mcp.TextContent)
		if !strings.Contains(text.Text, `"truncationReason":"bytes"`) {
			t.Errorf("expected truncationReason=bytes in response, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "projecting fewer properties") {
			t.Errorf("expected narrower-projection hint in byte-cap response, got: %s", text.Text)
		}
	})

	// PROFILE redirect: the service-side pre-flight in GetQueryType now classifies
	// PROFILE as QueryTypeWriteOnly, so the handler returns the standard policy
	// message rather than leaking the "conflicting execution modes" driver error.
	// The behavioural check here is the same as the existing "non-read query type"
	// case — we're just pinning the PROFILE → WriteOnly classification in a test.
	t.Run("PROFILE query rejected with policy message", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "PROFILE MATCH (n) RETURN count(n)", gomock.Nil()).
			Return(neo4j.QueryTypeWriteOnly, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "PROFILE MATCH (n) RETURN count(n)",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for PROFILE, got: %+v", result)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content on result")
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "use write-cypher") {
			t.Errorf("expected clean policy message redirecting to write-cypher, got: %s", text.Text)
		}
		if strings.Contains(text.Text, "conflicting execution modes") {
			t.Errorf("raw driver error leaked through; expected clean policy message, got: %s", text.Text)
		}
	})

	// Timeout plumbing: with CypherTimeout set the handler must wrap the caller's
	// context in context.WithTimeout before calling through. We verify this by
	// inspecting the ctx the mock receives and asserting it carries a deadline
	// within the configured window.
	t.Run("cypher timeout propagates to service via context", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		timeout := 5 * time.Second
		before := time.Now()

		var seenCtx context.Context
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN count(n)", gomock.Nil()).
			DoAndReturn(func(ctx context.Context, _ string, _ map[string]any) (neo4j.QueryType, error) {
				seenCtx = ctx
				return neo4j.QueryTypeReadOnly, nil
			})
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN count(n)", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    timeout,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN count(n)",
				},
			},
		}

		_, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seenCtx == nil {
			t.Fatal("expected GetQueryType to have been called with a non-nil context")
		}
		deadline, ok := seenCtx.Deadline()
		if !ok {
			t.Fatal("expected ctx to carry a deadline when CypherTimeout is set")
		}
		// Allow a generous slack window: the deadline must be after "before" and
		// within ~2s of the configured timeout from the start of the test. Tight
		// bounds would flake on slow CI.
		if deadline.Before(before.Add(timeout-time.Second)) || deadline.After(before.Add(timeout+2*time.Second)) {
			t.Errorf("deadline %v not within expected window around %v+%v", deadline, before, timeout)
		}
	})

	// Estimate guard — planner says too many rows, handler refuses before executing.
	// We pin the exact error shape here because the agent on the other end relies on
	// the hint ("add a LIMIT", concrete estimate vs threshold) to decide what to
	// retry with. If the wording drifts the agent's retry loop stops working.
	t.Run("estimate above threshold rejects query", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n:Company) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), "MATCH (n:Company) RETURN n", gomock.Nil()).
			Return(int64(5_700_000), nil)
		// Crucial: no ExecuteReadQueryStreaming or QueryResultToJSON expectations.
		// If the handler calls through, gomock will fail with an unexpected-call error.

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       analyticsService,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n:Company) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for over-estimate, got: %+v", result)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content on result")
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		// The message must carry both numbers (so the agent can reason about scale)
		// and the LIMIT hint (so the agent knows what to change).
		if !strings.Contains(text.Text, "5700000") {
			t.Errorf("expected estimated row count in error, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "1000000") {
			t.Errorf("expected threshold value in error, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "LIMIT") {
			t.Errorf("expected LIMIT hint in error, got: %s", text.Text)
		}
	})

	// Estimate below threshold — guard invoked, passes, execution proceeds. This
	// pins the fall-through path: the estimate was consulted but did not block.
	t.Run("estimate below threshold proceeds to execution", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n:Company) RETURN n LIMIT 50", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), "MATCH (n:Company) RETURN n LIMIT 50", gomock.Nil()).
			Return(int64(50), nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n:Company) RETURN n LIMIT 50", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       analyticsService,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n:Company) RETURN n LIMIT 50",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result when estimate is under threshold, got: %+v", result)
		}
	})

	// Fail-open on estimate error: the first EXPLAIN in GetQueryType already
	// succeeded, so the query is valid. A hiccup in the second EXPLAIN (this one)
	// shouldn't block execution — the row cap and timeout are still there as a
	// safety net. We verify the handler logs and proceeds all the way through to
	// QueryResultToJSON.
	t.Run("estimate error fails open", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN n LIMIT 10", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), "MATCH (n) RETURN n LIMIT 10", gomock.Nil()).
			Return(int64(0), errors.New("transient driver error"))
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN n LIMIT 10", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       analyticsService,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "MATCH (n) RETURN n LIMIT 10",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result despite estimate error (fail-open), got: %+v", result)
		}
	})

	// Telemetry "executed" outcome: guard passes, query runs. Verify the handler
	// emits a CYPHER_ESTIMATE_ACCURACY event carrying the estimate, the actual row
	// count, and the full config echo. A fresh telemetry mock (IsEnabled → true)
	// overrides the package-level analyticsService's IsEnabled → false so the
	// gated emission path actually runs.
	//
	// The sentinel TrackEvent returned from NewCypherEstimateEvent is also the
	// expected argument to EmitEvent. Asserting value equality here (not
	// gomock.Any) pins the handler's contract to pass the constructor's return
	// value through unchanged — a regression where the handler built the event
	// but dropped it would fail this test.
	t.Run("executed outcome emits accuracy event", func(t *testing.T) {
		const q = "MATCH (n:Company) RETURN n LIMIT 50"
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), q, gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), q, gomock.Nil()).
			Return(int64(50), nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), q, gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		telemetryMock := amocks.NewMockService(ctrl)
		telemetryMock.EXPECT().IsEnabled().AnyTimes().Return(true)
		sentinel := analytics.TrackEvent{Event: "test-executed-sentinel"}
		telemetryMock.EXPECT().
			NewCypherEstimateEvent("executed", int64(50), 0, false, 1_000_000, 1000).
			Return(sentinel)
		telemetryMock.EXPECT().EmitEvent(sentinel)

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       telemetryMock,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": q},
			},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got: %+v", result)
		}
	})

	// Telemetry "refused_over_estimate" outcome: guard fires, query never runs.
	// We still emit the event so refusals are counted in the Mixpanel stream
	// alongside executions. Crucially, no ExecuteReadQueryStreaming or
	// QueryResultToJSON expectations are set: if the handler reaches either path
	// gomock's unexpected-call detection fails the test.
	t.Run("refused_over_estimate outcome emits accuracy event", func(t *testing.T) {
		const q = "MATCH (n:Company) RETURN n"
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), q, gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), q, gomock.Nil()).
			Return(int64(5_700_000), nil)

		telemetryMock := amocks.NewMockService(ctrl)
		telemetryMock.EXPECT().IsEnabled().AnyTimes().Return(true)
		sentinel := analytics.TrackEvent{Event: "test-refused-sentinel"}
		telemetryMock.EXPECT().
			NewCypherEstimateEvent("refused_over_estimate", int64(5_700_000), 0, false, 1_000_000, 1000).
			Return(sentinel)
		telemetryMock.EXPECT().EmitEvent(sentinel)

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       telemetryMock,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": q},
			},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for over-estimate, got: %+v", result)
		}
	})

	// Telemetry "estimate_error" outcome: the EXPLAIN-for-estimate call failed,
	// but the query still ran fail-open. The event must still be emitted so we
	// can track how often the estimate fetch itself is unreliable. The
	// estimatedRows argument must be 0 because the handler never updates the
	// local variable when EstimateRowCount returns an error — this pins the
	// "0 means no estimate" contract from the Mixpanel consumer's perspective.
	t.Run("estimate_error outcome emits accuracy event", func(t *testing.T) {
		const q = "MATCH (n) RETURN n LIMIT 10"
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), q, gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			EstimateRowCount(gomock.Any(), q, gomock.Nil()).
			Return(int64(0), errors.New("transient driver error"))
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), q, gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		telemetryMock := amocks.NewMockService(ctrl)
		telemetryMock.EXPECT().IsEnabled().AnyTimes().Return(true)
		sentinel := analytics.TrackEvent{Event: "test-estimate-error-sentinel"}
		telemetryMock.EXPECT().
			NewCypherEstimateEvent("estimate_error", int64(0), 0, false, 1_000_000, 1000).
			Return(sentinel)
		telemetryMock.EXPECT().EmitEvent(sentinel)

		deps := &tools.ToolDependencies{
			DBService:              mockDB,
			AnalyticsService:       telemetryMock,
			CypherMaxRows:          1000,
			CypherMaxEstimatedRows: 1_000_000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": q},
			},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result despite estimate error (fail-open), got: %+v", result)
		}
	})

	// Timeout classification: when ExecuteReadQueryStreaming surfaces
	// context.DeadlineExceeded (because the handler wrapped ctx in
	// context.WithTimeout and the underlying query outran that deadline), the
	// handler must return a user-facing message that names the timeout value
	// and gives concrete remediation steps. The previous pass-through classified
	// timeouts as "ConnectivityError: context deadline exceeded", which sent
	// operators hunting network issues; the new message mirrors the truncation-
	// hint pattern and keeps the three common remediations in view for the agent.
	t.Run("timeout surfaces friendly classified error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n)-[*]-(m) RETURN m", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n)-[*]-(m) RETURN m", gomock.Nil(), 1000, 0).
			Return(nil, context.DeadlineExceeded)
		// Crucial: no QueryResultToJSON expectation — the handler must short-circuit
		// into the classification branch before formatting.

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    5 * time.Second,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": "MATCH (n)-[*]-(m) RETURN m"},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for timeout, got: %+v", result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		// Pin the contract: tool name, timeout duration, and the remediation hint.
		// Drift on any of these degrades the agent's ability to retry sensibly.
		if !strings.Contains(text.Text, "read-cypher timed out") {
			t.Errorf("expected 'read-cypher timed out' prefix, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "5s") {
			t.Errorf("expected configured timeout duration in message, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "LIMIT") {
			t.Errorf("expected LIMIT remediation in message, got: %s", text.Text)
		}
		// Regression guard against the old raw passthrough behaviour.
		if strings.Contains(text.Text, "ConnectivityError") {
			t.Errorf("raw ConnectivityError leaked through; classification is broken: %s", text.Text)
		}
	})

	// Cancellation classification: context.Canceled is semantically distinct from
	// a timeout — the caller aborted rather than the server running long. No
	// remediation hint because there's no user error to fix. We still verify the
	// message is concise and carries the tool name so logs/traces stay grep-able.
	t.Run("cancellation surfaces concise classified error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(nil, context.Canceled)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": "MATCH (n) RETURN n"},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result for cancellation, got: %+v", result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "read-cypher cancelled") {
			t.Errorf("expected 'read-cypher cancelled' prefix, got: %s", text.Text)
		}
	})

	// Wrapped context errors: the Neo4j driver does not always surface context
	// errors directly — it sometimes wraps DeadlineExceeded inside its own
	// ConnectivityError (which is what produced the original misleading message
	// in the live stress test). errors.Is must unwrap through those layers for
	// the classification to hold. We synthesise a wrapped error with fmt.Errorf
	// and verify classification still fires; if this fails, investigate whether
	// the driver wrapper needs an explicit errors.Is/As check.
	t.Run("timeout classification handles wrapped errors", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			GetQueryType(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil()).
			Return(neo4j.QueryTypeReadOnly, nil)
		wrapped := fmt.Errorf("ConnectivityError: %w", context.DeadlineExceeded)
		mockDB.EXPECT().
			ExecuteReadQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(nil, wrapped)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    5 * time.Second,
		}

		handler := cypher.ReadCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": "MATCH (n) RETURN n"},
			},
		}
		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("expected error result, got: %+v", result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", result.Content[0])
		}
		if !strings.Contains(text.Text, "read-cypher timed out") {
			t.Errorf("wrapped DeadlineExceeded not classified; check errors.Is unwrap path: %s", text.Text)
		}
	})
}
