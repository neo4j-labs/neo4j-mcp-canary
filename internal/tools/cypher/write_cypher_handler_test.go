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
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestWriteCypherHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	// Note: Handlers no longer emit events directly - events are emitted via hooks in server.go
	defer ctrl.Finish()

	t.Run("successful cypher execution with parameters", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "MATCH (n:Person {name: $name}) RETURN n", map[string]any{"name": "Alice"}, 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[{"n":{"name":"Alice"}}],"rowCount":1,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
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
			ExecuteWriteQueryStreaming(gomock.Any(), "MATCH (n) RETURN count(n)", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[{"count(n)":42}],"rowCount":1,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
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

		handler := cypher.WriteCypherHandler(deps)
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
		// The handler should NOT call ExecuteWriteQuery when query is empty
		// No expectations set for mockDB since it shouldn't be called

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.WriteCypherHandler(deps)
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
		// The handler should NOT call ExecuteWriteQuery when query is empty
		// No expectations set for mockDB since it shouldn't be called

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
		}

		handler := cypher.WriteCypherHandler(deps)
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

		handler := cypher.WriteCypherHandler(deps)
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

		handler := cypher.WriteCypherHandler(deps)
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
			ExecuteWriteQueryStreaming(gomock.Any(), "INVALID CYPHER", gomock.Nil(), 1000, 0).
			Return(nil, errors.New("syntax error"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
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
			ExecuteWriteQueryStreaming(gomock.Any(), "MATCH (n) RETURN n", gomock.Nil(), 1000, 0).
			Return(okResult(), nil)
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return("", errors.New("JSON marshaling failed"))

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
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

	// Truncation surfacing on the write side: a CREATE ... RETURN n that produces
	// more rows than the cap must still flow through QueryResultToJSON with its
	// truncation metadata intact. The handler is the same shape as read-cypher's
	// truncation path minus the GetQueryType hop.
	t.Run("truncated result surfaces hint", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		truncated := &database.QueryResult{
			Records:          []*neo4j.Record{},
			RowCount:         1000,
			Truncated:        true,
			TruncationReason: database.TruncationReasonRows,
			MaxRows:          1000,
		}
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "UNWIND range(1, 100000) AS i CREATE (n:Tmp {i: i}) RETURN n", gomock.Nil(), 1000, 0).
			Return(truncated, nil)
		mockDB.EXPECT().
			QueryResultToJSON(truncated).
			Return(`{"rows":[],"rowCount":1000,"truncated":true,"truncationReason":"rows","maxRows":1000,"hint":"Results were truncated at 1000 rows. Add a LIMIT clause or a more selective filter and retry for a complete result."}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "UNWIND range(1, 100000) AS i CREATE (n:Tmp {i: i}) RETURN n",
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

	// Byte-cap plumbing on the write side: a CREATE ... RETURN n on wide nodes
	// must surface through the same envelope as read-cypher. The byte cap is
	// particularly relevant for writes that RETURN full nodes after mutation,
	// which is a common "create and confirm" pattern.
	t.Run("byte cap propagates through write handler", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		truncated := &database.QueryResult{
			Records:          []*neo4j.Record{},
			RowCount:         425,
			Truncated:        true,
			TruncationReason: database.TruncationReasonBytes,
			MaxRows:          1000,
			MaxBytes:         900_000,
		}
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "UNWIND range(1, 10000) AS i CREATE (n:Wide {big: 'x'}) RETURN n", gomock.Nil(), 1000, 900_000).
			Return(truncated, nil)
		mockDB.EXPECT().
			QueryResultToJSON(truncated).
			Return(`{"rows":[],"rowCount":425,"truncated":true,"truncationReason":"bytes","maxBytes":900000,"hint":"Results were truncated because the response size would have exceeded 900000 bytes."}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherMaxBytes:   900_000,
		}

		handler := cypher.WriteCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "UNWIND range(1, 10000) AS i CREATE (n:Wide {big: 'x'}) RETURN n",
				},
			},
		}

		result, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("expected success result, got %+v", result)
		}
		text, _ := result.Content[0].(mcp.TextContent)
		if !strings.Contains(text.Text, `"truncationReason":"bytes"`) {
			t.Errorf("expected truncationReason=bytes, got: %s", text.Text)
		}
	})

	// Timeout plumbing: CypherTimeout must cause the handler to wrap the caller's
	// context in context.WithTimeout before dispatching to ExecuteWriteQueryStreaming.
	// Same setup as the read-cypher variant, minus the GetQueryType hop — we capture
	// the ctx the mock receives and assert it carries a deadline in the right window.
	t.Run("cypher timeout propagates to service via context", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		timeout := 5 * time.Second
		before := time.Now()

		var seenCtx context.Context
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "CREATE (n:Test) RETURN n", gomock.Nil(), 1000, 0).
			DoAndReturn(func(ctx context.Context, _ string, _ map[string]any, _, _ int) (*database.QueryResult, error) {
				seenCtx = ctx
				return okResult(), nil
			})
		mockDB.EXPECT().
			QueryResultToJSON(gomock.Any()).
			Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    timeout,
		}

		handler := cypher.WriteCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query": "CREATE (n:Test) RETURN n",
				},
			},
		}

		_, err := handler(context.Background(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seenCtx == nil {
			t.Fatal("expected ExecuteWriteQueryStreaming to have been called with a non-nil context")
		}
		deadline, ok := seenCtx.Deadline()
		if !ok {
			t.Fatal("expected ctx to carry a deadline when CypherTimeout is set")
		}
		if deadline.Before(before.Add(timeout - time.Second)) || deadline.After(before.Add(timeout+2*time.Second)) {
			t.Errorf("deadline %v not within expected window around %v+%v", deadline, before, timeout)
		}
	})

	// Timeout classification on the write side mirrors the read-cypher behaviour
	// but with a write-specific remediation hint: reduce batch size, narrow the
	// MATCH, or apoc.periodic.iterate. The read-side "bound variable-length
	// patterns" guidance is deliberately absent here because var-length patterns
	// are rarely the cause of write-cypher timeouts in practice — large batches
	// and unbounded SET/DELETE are.
	t.Run("timeout surfaces friendly classified error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "UNWIND range(1, 10000000) AS i CREATE (n:Tmp {i: i})", gomock.Nil(), 1000, 0).
			Return(nil, context.DeadlineExceeded)
		// No QueryResultToJSON expectation — the handler must short-circuit into
		// the classification branch.

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
			CypherTimeout:    5 * time.Second,
		}

		handler := cypher.WriteCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": "UNWIND range(1, 10000000) AS i CREATE (n:Tmp {i: i})"},
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
		// Pin the write-specific message contract. Each assertion protects one
		// facet of the agent-facing behaviour:
		//   - "write-cypher timed out" — tool identity so logs/traces stay grep-able
		//   - "5s" — concrete timeout number so the agent can reason about scale
		//   - "apoc.periodic.iterate" — the write-specific remediation
		//   - absence of "ConnectivityError" — regression guard
		if !strings.Contains(text.Text, "write-cypher timed out") {
			t.Errorf("expected 'write-cypher timed out' prefix, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "5s") {
			t.Errorf("expected configured timeout duration in message, got: %s", text.Text)
		}
		if !strings.Contains(text.Text, "apoc.periodic.iterate") {
			t.Errorf("expected apoc.periodic.iterate remediation in message, got: %s", text.Text)
		}
		if strings.Contains(text.Text, "ConnectivityError") {
			t.Errorf("raw ConnectivityError leaked through; classification is broken: %s", text.Text)
		}
	})

	// Cancellation classification: caller aborted, no remediation hint because
	// there's no user error to fix. Mirrors the read-cypher behaviour with
	// write-cypher-specific logging and message wording.
	t.Run("cancellation surfaces concise classified error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteWriteQueryStreaming(gomock.Any(), "CREATE (n:Test) RETURN n", gomock.Nil(), 1000, 0).
			Return(nil, context.Canceled)

		deps := &tools.ToolDependencies{
			DBService:        mockDB,
			AnalyticsService: analyticsService,
			CypherMaxRows:    1000,
		}

		handler := cypher.WriteCypherHandler(deps)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{"query": "CREATE (n:Test) RETURN n"},
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
		if !strings.Contains(text.Text, "write-cypher cancelled") {
			t.Errorf("expected 'write-cypher cancelled' prefix, got: %s", text.Text)
		}
	})
}
