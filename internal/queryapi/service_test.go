// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// --- Wire-format helpers -----------------------------------------------
//
// query.QueryService.ExecuteStream returns a concrete *query.StreamResult
// with no exported constructor, so it cannot be faked in-process. These
// helpers build a real query-go-sdk client pointed at an httptest.Server
// that speaks the documented JSON-Lines streaming wire format directly
// (https://neo4j.com/docs/query-api/current/streaming/), which is the only
// way to exercise Service's real ExecuteStream call path in a unit
// test.

// wireEvent is one line of a streamed response.
type wireEvent struct {
	Event string `json:"$event"`
	Body  any    `json:"_body"`
}

func typedInt(n int64) map[string]any {
	return map[string]any{"$type": "Integer", "_value": strconv.FormatInt(n, 10)}
}

func typedString(s string) map[string]any {
	return map[string]any{"$type": "String", "_value": s}
}

// writeStream marshals events as JSON-Lines onto w, exactly as the Query API
// streaming endpoint would.
func writeStream(w http.ResponseWriter, events ...wireEvent) {
	w.Header().Set("Content-Type", "application/vnd.neo4j.query.v1.1+jsonl")
	enc := json.NewEncoder(w)
	for _, e := range events {
		_ = enc.Encode(e)
	}
}

// newTestService spins up an httptest.Server driven by handler and returns a
// Service backed by a real query-go-sdk client pointed at it. The
// server is closed automatically via t.Cleanup.
func newTestService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := query.NewClient(
		query.WithBasicAuth("neo4j", "password"),
		query.WithBaseURL(server.URL),
		query.WithStreamingSupport(true),
	)
	if err != nil {
		t.Fatalf("query.NewClient: %v", err)
	}
	t.Cleanup(client.Close)

	svc, err := NewService(func(context.Context) (query.QueryService, error) {
		return client.Query, nil
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// --- GetQueryType --------------------------------------------------------

func TestService_GetQueryType(t *testing.T) {
	tests := []struct {
		name          string
		wireQueryType string
		want          neo4j.QueryType
	}{
		{"read-only", "r", neo4j.QueryTypeReadOnly},
		{"write-only", "w", neo4j.QueryTypeWriteOnly},
		{"read-write", "rw", neo4j.QueryTypeReadWrite},
		{"schema-write", "s", neo4j.QueryTypeSchemaWrite},
		{"unrecognized value fails closed to write", "something-else", neo4j.QueryTypeWriteOnly},
		{"empty value fails closed to write", "", neo4j.QueryTypeWriteOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
				writeStream(w,
					wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
					wireEvent{Event: "Summary", Body: map[string]any{"queryType": tt.wireQueryType}},
				)
			})

			got, err := svc.GetQueryType(context.Background(), "MATCH (n) RETURN n", nil)
			if err != nil {
				t.Fatalf("GetQueryType: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetQueryType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_GetQueryType_ExplainRejected(t *testing.T) {
	svc := newTestService(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for an EXPLAIN-prefixed query")
	})

	_, err := svc.GetQueryType(context.Background(), "EXPLAIN MATCH (n) RETURN n", nil)
	if !errors.Is(err, database.ErrExplainUnsupported) {
		t.Errorf("expected ErrExplainUnsupported, got: %v", err)
	}
}

func TestService_GetQueryType_ProfileIsWriteOnly(t *testing.T) {
	svc := newTestService(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for a PROFILE-prefixed query")
	})

	got, err := svc.GetQueryType(context.Background(), "PROFILE MATCH (n) RETURN n", nil)
	if err != nil {
		t.Fatalf("GetQueryType: %v", err)
	}
	if got != neo4j.QueryTypeWriteOnly {
		t.Errorf("GetQueryType(PROFILE ...) = %v, want QueryTypeWriteOnly", got)
	}
}

// --- EstimateRowCount -----------------------------------------------------

func TestService_EstimateRowCount(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
			wireEvent{Event: "Summary", Body: map[string]any{
				"queryType": "r",
				"queryPlan": map[string]any{
					"operatorType": "ProduceResults",
					"arguments":    map[string]any{"EstimatedRows": typedInt(4200)},
					"identifiers":  []string{},
					"children":     []any{},
				},
			}},
		)
	})

	got, err := svc.EstimateRowCount(context.Background(), "MATCH (n) RETURN n", nil)
	if err != nil {
		t.Fatalf("EstimateRowCount: %v", err)
	}
	if got != 4200 {
		t.Errorf("EstimateRowCount() = %d, want 4200", got)
	}
}

func TestService_EstimateRowCount_NoPlan(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	})

	got, err := svc.EstimateRowCount(context.Background(), "MATCH (n) RETURN n", nil)
	if err != nil {
		t.Fatalf("EstimateRowCount: %v", err)
	}
	if got != 0 {
		t.Errorf("EstimateRowCount() = %d, want 0 (no estimate available)", got)
	}
}

func TestService_EstimateRowCount_ExplainPreflightSkipped(t *testing.T) {
	svc := newTestService(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for an EXPLAIN/PROFILE-prefixed query")
	})

	got, err := svc.EstimateRowCount(context.Background(), "EXPLAIN MATCH (n) RETURN n", nil)
	if err != nil {
		t.Fatalf("EstimateRowCount: %v", err)
	}
	if got != 0 {
		t.Errorf("EstimateRowCount() = %d, want 0", got)
	}
}

// --- ExecuteReadQuery / ExecuteWriteQuery (buffered) -----------------------

func TestService_ExecuteReadQuery(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{"name"}}},
			wireEvent{Event: "Record", Body: []any{typedString("Alice")}},
			wireEvent{Event: "Record", Body: []any{typedString("Bob")}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	})

	records, err := svc.ExecuteReadQuery(context.Background(), "MATCH (n:Person) RETURN n.name AS name", nil)
	if err != nil {
		t.Fatalf("ExecuteReadQuery: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	name0, _ := records[0].Get("name")
	name1, _ := records[1].Get("name")
	if name0 != "Alice" || name1 != "Bob" {
		t.Errorf("got names %v, %v; want Alice, Bob", name0, name1)
	}
}

// --- ExecuteReadQueryStreaming truncation ----------------------------------

func TestService_ExecuteReadQueryStreaming_RowCap(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		events := []wireEvent{
			{Event: "Header", Body: map[string]any{"fields": []string{"n"}}},
		}
		for i := int64(0); i < 5; i++ {
			events = append(events, wireEvent{Event: "Record", Body: []any{typedInt(i)}})
		}
		events = append(events, wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}})
		writeStream(w, events...)
	})

	result, err := svc.ExecuteReadQueryStreaming(context.Background(), "MATCH (n) RETURN n", nil, 3, 0)
	if err != nil {
		t.Fatalf("ExecuteReadQueryStreaming: %v", err)
	}
	if result.RowCount != 3 {
		t.Errorf("RowCount = %d, want 3", result.RowCount)
	}
	if !result.Truncated || result.TruncationReason != database.TruncationReasonRows {
		t.Errorf("Truncated = %v, TruncationReason = %q, want true/%q", result.Truncated, result.TruncationReason, database.TruncationReasonRows)
	}
}

func TestService_ExecuteReadQueryStreaming_NoCap(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{"n"}}},
			wireEvent{Event: "Record", Body: []any{typedInt(1)}},
			wireEvent{Event: "Record", Body: []any{typedInt(2)}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	})

	result, err := svc.ExecuteReadQueryStreaming(context.Background(), "MATCH (n) RETURN n", nil, 0, 0)
	if err != nil {
		t.Fatalf("ExecuteReadQueryStreaming: %v", err)
	}
	if result.RowCount != 2 || result.Truncated {
		t.Errorf("RowCount = %d, Truncated = %v, want 2/false", result.RowCount, result.Truncated)
	}
}

// --- QueryErrors -> Neo4jError mapping --------------------------------------

func TestService_ExecuteReadQuery_AccessModeErrorMapping(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{"n"}}},
			wireEvent{Event: "Error", Body: []map[string]any{{
				"code":    "Neo.ClientError.Statement.AccessMode",
				"message": "Writing in read access mode not allowed. Attempted write to neo4j",
			}}},
		)
	})

	_, err := svc.ExecuteReadQuery(context.Background(), "MATCH (n) SET n.x = 1 RETURN n", nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var neo4jErr *neo4j.Neo4jError
	if !errors.As(err, &neo4jErr) {
		t.Fatalf("expected errors.As to find a *neo4j.Neo4jError, got: %v (%T)", err, err)
	}
	if neo4jErr.Code != "Neo.ClientError.Statement.AccessMode" {
		t.Errorf("neo4jErr.Code = %q, want %q", neo4jErr.Code, "Neo.ClientError.Statement.AccessMode")
	}
}

// --- VerifyConnectivity / JSON formatting delegate to shared helpers -------

func TestService_VerifyConnectivity(t *testing.T) {
	svc := newTestService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{"first"}}},
			wireEvent{Event: "Record", Body: []any{typedInt(1)}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	})

	if err := svc.VerifyConnectivity(context.Background()); err != nil {
		t.Errorf("VerifyConnectivity: %v", err)
	}
}

func TestService_Neo4jRecordsToJSON(t *testing.T) {
	svc := newTestService(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("Neo4jRecordsToJSON should not need to contact the server")
	})

	got, err := svc.Neo4jRecordsToJSON(nil)
	if err != nil {
		t.Fatalf("Neo4jRecordsToJSON: %v", err)
	}
	if got != "[]" {
		t.Errorf("Neo4jRecordsToJSON(nil) = %q, want %q", got, "[]")
	}
}
