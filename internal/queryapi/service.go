// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// ClientFactory returns the query.QueryService to use for a single call.
//
// This is the seam that lets Service support both transport modes
// with one implementation: a STDIO-mode factory closes over one long-lived
// client and ignores ctx; an HTTP-mode factory builds a fresh, lightweight
// client per call using the caller's credentials from ctx (see client.go).
// It also makes Service trivially unit-testable against a fake
// query.QueryService.
type ClientFactory func(ctx context.Context) (query.QueryService, error)

// Service implements database.Service (QueryExecutor +
// RecordFormatter + Helpers) on top of the Neo4j Query API instead of the
// Bolt driver. It is a drop-in for database.Service: internal/tools and
// internal/server depend only on that interface, never on neo4j.Driver
// directly, so nothing above this package needs to change to support it.
//
// Every executor method is built on ExecuteStream rather than the SDK's
// buffered Execute, because only the streaming Summary event exposes
// queryType (needed by GetQueryType) on the query-go-sdk version this was
// built against — see the project plan for the full rationale.
type Service struct {
	newClient ClientFactory
}

// NewService constructs a Service. newClient is called once
// per executor call to obtain the query.QueryService to use — see
// ClientFactory.
func NewService(newClient ClientFactory) (*Service, error) {
	if newClient == nil {
		return nil, fmt.Errorf("newClient cannot be nil")
	}
	return &Service{newClient: newClient}, nil
}

// ExecuteReadQuery executes a read-only Cypher query and returns raw records.
func (s *Service) ExecuteReadQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	return s.executeBuffered(ctx, cypher, params)
}

// ExecuteWriteQuery executes a write-only Cypher query and returns raw records.
func (s *Service) ExecuteWriteQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	return s.executeBuffered(ctx, cypher, params)
}

// executeBuffered runs cypher via ExecuteStream and drains every record into
// a []*neo4j.Record, matching the eager semantics ExecuteReadQuery/
// ExecuteWriteQuery already have on the Bolt path (which uses
// neo4j.ExecuteQuery + EagerResultTransformer). The Query API's client-wide
// (not per-call) AccessMode setting means there is no read-vs-write routing
// hint to set here, unlike the Bolt driver's
// ExecuteQueryWithReadersRouting/WritersRouting — routing decisions are left
// to the server (AccessModeUnset), and the actual read/write policy
// enforcement happens in the read-cypher handler via GetQueryType, exactly
// as it does for the Bolt path.
func (s *Service) executeBuffered(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	svc, err := s.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build query api client: %w", err)
	}

	stream, err := svc.ExecuteStream(ctx, cypher, params)
	if err != nil {
		return nil, wrapQueryAPIError(err)
	}

	records := make([]*neo4j.Record, 0)
	for rec, recErr := range stream.Records() {
		if recErr != nil {
			return nil, wrapQueryAPIError(recErr)
		}
		records = append(records, convertRecord(rec))
	}
	return records, nil
}

// ExecuteReadQueryStreaming runs a read-only Cypher query with the same row
// and byte cap semantics as database.Neo4jService.ExecuteReadQueryStreaming.
// See QueryExecutor.ExecuteReadQueryStreaming on the interface for the full
// contract.
func (s *Service) ExecuteReadQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*database.QueryResult, error) {
	return s.executeStreaming(ctx, cypher, params, maxRows, maxBytes)
}

// ExecuteWriteQueryStreaming mirrors ExecuteReadQueryStreaming for writes.
func (s *Service) ExecuteWriteQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*database.QueryResult, error) {
	return s.executeStreaming(ctx, cypher, params, maxRows, maxBytes)
}

// executeStreaming is the shared implementation for the read and write
// streaming paths. It mirrors Neo4jService.executeStreaming in
// internal/database/service.go: iterate with an early break when either cap
// is reached, and the byte cap is measured against the adapted record's
// AsMap() directly (not through the JSON-tagged wrapper types) — the same
// approximation the Bolt path makes, kept here for consistency between the
// two transports' truncation behavior.
//
// Breaking out of the range early over stream.Records() already releases
// the underlying HTTP connection (the SDK's iterator closes the stream on
// early return — see query-go-sdk's StreamResult.Records() doc comment), so
// unlike the Bolt path's explicit res.Consume, no explicit Close call is
// needed here on truncation.
func (s *Service) executeStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*database.QueryResult, error) {
	svc, err := s.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build query api client: %w", err)
	}

	stream, err := svc.ExecuteStream(ctx, cypher, params)
	if err != nil {
		return nil, wrapQueryAPIError(err)
	}

	records := make([]*neo4j.Record, 0)
	truncated := false
	truncationReason := database.TruncationReasonNone
	byteCount := 0

	for rec, recErr := range stream.Records() {
		if recErr != nil {
			return nil, wrapQueryAPIError(recErr)
		}
		record := convertRecord(rec)

		// Byte cap: same "admit one record even if it alone exceeds the cap"
		// rule as the Bolt path, for the same reason — returning zero rows
		// on a single wide record is worse UX than one row flagged truncated.
		if maxBytes > 0 && len(records) > 0 {
			recordBytes, mErr := json.Marshal(record.AsMap())
			switch {
			case mErr != nil:
				slog.Debug("failed to measure record size, including without byte accounting", "error", mErr)
			case byteCount+len(recordBytes) > maxBytes:
				truncated = true
				truncationReason = database.TruncationReasonBytes
			default:
				byteCount += len(recordBytes)
			}
		} else if maxBytes > 0 {
			if recordBytes, mErr := json.Marshal(record.AsMap()); mErr == nil {
				byteCount += len(recordBytes)
			}
		}
		if truncated {
			break
		}

		if maxRows > 0 && len(records) >= maxRows {
			truncated = true
			truncationReason = database.TruncationReasonRows
			break
		}
		records = append(records, record)
	}

	return &database.QueryResult{
		Records:          records,
		Truncated:        truncated,
		TruncationReason: truncationReason,
		RowCount:         len(records),
		MaxRows:          maxRows,
		ByteCount:        byteCount,
		MaxBytes:         maxBytes,
	}, nil
}

// GetQueryType prefixes cypher with EXPLAIN and reports whether it is
// read-only, matching the contract of Neo4jService.GetQueryType.
//
// The Query API's EXPLAIN response carries a queryType field; "r" is the
// only read-only value — any other value (or one this package doesn't
// recognize) is treated as a potential write and mapped to
// neo4j.QueryTypeWriteOnly rather than neo4j.QueryTypeUnknown, so an
// unrecognized value fails closed against the read-cypher handler's
// `!= neo4j.QueryTypeReadOnly` rejection check instead of accidentally
// passing it.
func (s *Service) GetQueryType(ctx context.Context, cypher string, params map[string]any) (neo4j.QueryType, error) {
	switch database.FirstKeyword(cypher) {
	case "PROFILE":
		return neo4j.QueryTypeWriteOnly, nil
	case "EXPLAIN":
		return neo4j.QueryTypeUnknown, database.ErrExplainUnsupported
	}

	explainedQuery := strings.Join([]string{"EXPLAIN", cypher}, " ")

	summary, err := s.explainSummary(ctx, explainedQuery, params)
	if err != nil {
		return neo4j.QueryTypeUnknown, fmt.Errorf("error during GetQueryType: %w", err)
	}

	return mapQueryType(summary.QueryType), nil
}

// EstimateRowCount returns the planner's estimate for the row count of the
// query, matching the contract of Neo4jService.EstimateRowCount.
func (s *Service) EstimateRowCount(ctx context.Context, cypher string, params map[string]any) (int64, error) {
	switch database.FirstKeyword(cypher) {
	case "EXPLAIN", "PROFILE":
		return 0, nil
	}

	explainedQuery := strings.Join([]string{"EXPLAIN", cypher}, " ")

	summary, err := s.explainSummary(ctx, explainedQuery, params)
	if err != nil {
		return 0, fmt.Errorf("error during EstimateRowCount: %w", err)
	}
	if summary.QueryPlan == nil {
		return 0, nil
	}
	return database.ExtractEstimatedRows(summary.QueryPlan.Arguments), nil
}

// explainSummary runs an already-EXPLAIN-prefixed statement via
// ExecuteStream and returns its StreamSummary — the only place in the
// query-go-sdk version this was built against that exposes queryType and
// the query plan together. Draining stream.Records() to completion (an
// EXPLAIN produces zero rows, but the Summary event only arrives after the
// stream is fully consumed) is required before Summary() is non-nil.
func (s *Service) explainSummary(ctx context.Context, explainedQuery string, params map[string]any) (*query.StreamSummary, error) {
	svc, err := s.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build query api client: %w", err)
	}

	stream, err := svc.ExecuteStream(ctx, explainedQuery, params)
	if err != nil {
		return nil, wrapQueryAPIError(err)
	}
	for _, recErr := range stream.Records() {
		if recErr != nil {
			return nil, wrapQueryAPIError(recErr)
		}
	}

	summary := stream.Summary()
	if summary == nil {
		return nil, fmt.Errorf("no summary returned for explained query")
	}
	return summary, nil
}

// mapQueryType maps the Query API's wire queryType string onto
// neo4j.QueryType. Only "r" is read-only; every other recognized value maps
// to its Bolt-protocol equivalent, and anything unrecognized fails closed to
// QueryTypeWriteOnly rather than QueryTypeUnknown — see GetQueryType's doc
// comment for why.
func mapQueryType(wireQueryType string) neo4j.QueryType {
	switch wireQueryType {
	case "r":
		return neo4j.QueryTypeReadOnly
	case "rw":
		return neo4j.QueryTypeReadWrite
	case "s":
		return neo4j.QueryTypeSchemaWrite
	case "w":
		return neo4j.QueryTypeWriteOnly
	default:
		return neo4j.QueryTypeWriteOnly
	}
}

// VerifyConnectivity checks that the Query API client can reach and query
// the configured Neo4j database. Delegates to database.VerifyConnectivityWith,
// which holds no transport-specific state.
func (s *Service) VerifyConnectivity(ctx context.Context) error {
	return database.VerifyConnectivityWith(ctx, s)
}

// Neo4jRecordsToJSON converts Neo4j records to a JSON string. Delegates to
// database.FormatRecordsAsJSON, which the adapted records in convert.go are
// designed to be indistinguishable from Bolt-driver output to.
func (s *Service) Neo4jRecordsToJSON(records []*neo4j.Record) (string, error) {
	return database.FormatRecordsAsJSON(records)
}

// QueryResultToJSON formats a streaming QueryResult as the same JSON
// envelope the Bolt path produces. Delegates to database.FormatQueryResultAsJSON.
func (s *Service) QueryResultToJSON(result *database.QueryResult) (string, error) {
	return database.FormatQueryResultAsJSON(result)
}

var _ database.Service = (*Service)(nil)
