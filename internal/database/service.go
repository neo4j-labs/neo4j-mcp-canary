// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const appName string = "MCP4NEO4J"

// Neo4jService is the concrete implementation of DatabaseService
type Neo4jService struct {
	driver          neo4j.Driver
	database        string
	transportMode   config.TransportMode // Transport mode (stdio or http)
	neo4jMCPVersion string
}

// NewNeo4jService creates a new Neo4jService instance
func NewNeo4jService(driver neo4j.Driver, database string, transportMode config.TransportMode, neo4jMCPVersion string) (*Neo4jService, error) {
	if driver == nil {
		return nil, fmt.Errorf("driver cannot be nil")
	}

	return &Neo4jService{
		driver:          driver,
		database:        database,
		transportMode:   transportMode,
		neo4jMCPVersion: neo4jMCPVersion,
	}, nil
}

// buildQueryOptions builds Neo4j query options based on transport mode.
// For HTTP mode: extracts credentials from context and uses impersonation.
// Supports both Bearer token auth (preferred for SSO/OAuth) and Basic Auth (fallback).
// Bearer tokens are passed directly to Neo4j for SSO/OAuth scenarios.
// If credentials are absent, they are not added to the query options (driver defaults apply).
// For STDIO mode: uses driver's built-in credentials (no auth token added).
// The baseOptions parameter allows adding routing-specific options (readers/writers).
// TxMetadata is added to recognize queries coming from Neo4j MCP.
func (s *Neo4jService) buildQueryOptions(ctx context.Context, baseOptions ...neo4j.ExecuteQueryConfigurationOption) []neo4j.ExecuteQueryConfigurationOption {
	txMetadata := neo4j.WithTxMetadata(map[string]any{"app": strings.Join([]string{appName, s.neo4jMCPVersion}, "/")})

	queryOptions := []neo4j.ExecuteQueryConfigurationOption{
		neo4j.ExecuteQueryWithDatabase(s.database),
		neo4j.ExecuteQueryWithTransactionConfig(txMetadata),
	}

	// Add any base options (routing, etc.)
	queryOptions = append(queryOptions, baseOptions...)

	// For HTTP mode, extract credentials from context and use impersonation
	if s.transportMode == config.TransportModeHTTP {
		authToken := s.getHTTPAuthToken(ctx)
		if authToken != nil {
			queryOptions = append(queryOptions, neo4j.ExecuteQueryWithAuthToken(*authToken))
		}
	}
	// For STDIO mode, driver's built-in credentials are used automatically (no auth token needed)
	return queryOptions
}

// VerifyConnectivity checks the driver can establish a valid connection with a Neo4j instance;
// This is done by running a harmless test query against whatever database has been specified ( if any )
func (s *Neo4jService) VerifyConnectivity(ctx context.Context) error {
	// Run a harmless test query
	records, err := s.ExecuteReadQuery(ctx, "RETURN 1 as first", map[string]any{})
	if err != nil {
		return fmt.Errorf("impossible to verify connectivity with the Neo4j instance: %w", err)
	}

	if len(records) != 1 || len(records[0].Values) != 1 {
		return fmt.Errorf("failed to verify connectivity with the Neo4j instance: unexpected response from test query")
	}
	one, ok := records[0].Values[0].(int64)

	if !ok || one != 1 {
		return fmt.Errorf("failed to verify connectivity with the Neo4j instance: unexpected response from test query")
	}

	return nil
}

// Collect HTTP Auth token from Context.
func (s *Neo4jService) getHTTPAuthToken(ctx context.Context) *neo4j.AuthToken {
	if token, hasBearerToken := auth.GetBearerToken(ctx); hasBearerToken {
		authToken := neo4j.BearerAuth(token)
		return &authToken
	}
	if username, password, hasBasicAuth := auth.GetBasicAuthCredentials(ctx); hasBasicAuth {
		// Fall back to basic auth
		authToken := neo4j.BasicAuth(username, password, "")
		return &authToken
	}
	return nil
}

// ExecuteReadQuery executes a read-only Cypher query and returns raw records
func (s *Neo4jService) ExecuteReadQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	queryOptions := s.buildQueryOptions(ctx, neo4j.ExecuteQueryWithReadersRouting())

	res, err := neo4j.ExecuteQuery(ctx, s.driver, cypher, params, neo4j.EagerResultTransformer, queryOptions...)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to execute read query: %w", err)
		slog.Error("Error in ExecuteReadQuery", "error", wrappedErr)

		return nil, wrappedErr
	}

	return res.Records, nil
}

// ExecuteWriteQuery executes a write-only Cypher query and returns raw records
func (s *Neo4jService) ExecuteWriteQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	queryOptions := s.buildQueryOptions(ctx, neo4j.ExecuteQueryWithWritersRouting())

	res, err := neo4j.ExecuteQuery(ctx, s.driver, cypher, params, neo4j.EagerResultTransformer, queryOptions...)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to execute write query: %w", err)
		slog.Error("Error in ExecuteWriteQuery", "error", wrappedErr)
		return nil, wrappedErr
	}

	return res.Records, nil
}

// GetQueryType prefixes the provided query with EXPLAIN and returns the query type (e.g. 'r' for read, 'w' for write, 'rw' etc.)
// This allows read-only tools to determine if a query is safe to run in read-only context.
func (s *Neo4jService) GetQueryType(ctx context.Context, cypher string, params map[string]any) (neo4j.QueryType, error) {
	// Pre-flight check for leading verbs that the EXPLAIN-wrap classifier below cannot
	// handle correctly:
	//
	//   - PROFILE: wrapping in EXPLAIN produces "EXPLAIN PROFILE <query>", which the
	//     server rejects with Neo.ClientError.Statement.ArgumentError ("Can't specify
	//     multiple conflicting values for execution mode"). Classifying as WriteOnly
	//     here surfaces the standard read-cypher policy message ("use write-cypher
	//     instead") — the same UX as CREATE/SET — rather than leaking the raw driver
	//     error. PROFILE also executes the query for real (it is not purely a planning
	//     operation), so routing it to write-cypher is semantically correct under the
	//     read/write split.
	//
	//   - EXPLAIN: classifies cleanly as read-only at the protocol level, so the
	//     write-cypher redirect that PROFILE gets would be wrong. But EXPLAIN on the
	//     execution path produces zero records (the plan lives on ResultSummary,
	//     not in the row stream), so letting it through would return an empty row
	//     envelope to the caller. Return ErrExplainUnsupported so the read-cypher
	//     handler can produce a targeted "remove the EXPLAIN prefix" message.
	//
	// Other leading verbs (CREATE, SET, etc.) are still classified via the EXPLAIN
	// wrap below, because the planner is the authoritative source of truth for
	// anything more complex — procedures, subqueries, CALL {...}, and so on.
	//
	// Known limitation: a "CYPHER <options> PROFILE ..." preamble bypasses this check
	// (the first keyword seen is "CYPHER"). That path still produces the raw driver
	// error, but the common case — a bare PROFILE — is now handled cleanly.
	switch firstKeyword(cypher) {
	case "PROFILE":
		return neo4j.QueryTypeWriteOnly, nil
	case "EXPLAIN":
		return neo4j.QueryTypeUnknown, ErrExplainUnsupported
	}

	explainedQuery := strings.Join([]string{"EXPLAIN", cypher}, " ")

	queryOptions := s.buildQueryOptions(ctx)

	res, err := neo4j.ExecuteQuery(ctx, s.driver, explainedQuery, params, neo4j.EagerResultTransformer, queryOptions...)
	if err != nil {
		// Scrub the "EXPLAIN " we prepended from the driver's error before bubbling
		// up: the driver's syntax errors echo the query we sent (including our
		// wrapper) and cite column/offset positions against it. Without the scrub,
		// callers see positions and a quoted query referencing a keyword they
		// never typed. sanitizeExplainPrefix is a no-op for errors that don't
		// reference our wrapped query, so it is safe to apply unconditionally.
		wrappedErr := fmt.Errorf("error during GetQueryType: %w", sanitizeExplainPrefix(err))
		slog.Error("Error during GetQueryType", "error", wrappedErr)
		return neo4j.QueryTypeUnknown, wrappedErr
	}

	if res.Summary == nil {
		err := fmt.Errorf("error during GetQueryType: no summary returned for explained query")
		slog.Error("Error during GetQueryType", "error", err)
		return neo4j.QueryTypeUnknown, err
	}

	return res.Summary.QueryType(), nil

}

// EstimateRowCount returns the planner's estimate for the row count of the query.
// See QueryExecutor.EstimateRowCount on the interface for the full contract.
func (s *Neo4jService) EstimateRowCount(ctx context.Context, cypher string, params map[string]any) (int64, error) {
	// EXPLAIN/PROFILE can't be re-wrapped with another EXPLAIN — the server rejects
	// the double prefix with the same "conflicting execution modes" error that
	// motivated the GetQueryType pre-flight. For these leading verbs we have no
	// meaningful estimate to produce, so we return 0 ("no estimate") and let the
	// caller skip the guard. Note that PROFILE would already have been short-
	// circuited by GetQueryType into QueryTypeWriteOnly, so read-cypher will not
	// reach this path for PROFILE. EXPLAIN queries classified as ReadOnly can
	// and do land here — they're legitimately cheap to run (no execution at all)
	// so skipping the guard is the right call anyway.
	switch firstKeyword(cypher) {
	case "EXPLAIN", "PROFILE":
		return 0, nil
	}

	explainedQuery := strings.Join([]string{"EXPLAIN", cypher}, " ")
	queryOptions := s.buildQueryOptions(ctx)

	res, err := neo4j.ExecuteQuery(ctx, s.driver, explainedQuery, params, neo4j.EagerResultTransformer, queryOptions...)
	if err != nil {
		// Same scrub as GetQueryType — in practice EstimateRowCount should rarely
		// surface a syntax error (the earlier GetQueryType call would have caught
		// it), but schema races and transient driver errors can still produce
		// messages that quote our wrapped query. Applying the sanitiser uniformly
		// keeps the user-facing error consistent across both EXPLAIN-wrapping
		// paths rather than cleaning one and leaking from the other.
		wrappedErr := fmt.Errorf("error during EstimateRowCount: %w", sanitizeExplainPrefix(err))
		slog.Error("Error during EstimateRowCount", "error", wrappedErr)
		return 0, wrappedErr
	}
	if res.Summary == nil {
		return 0, nil
	}
	plan := res.Summary.Plan()
	if plan == nil {
		// EXPLAIN did not produce a plan — unusual but possible for administrative
		// commands or edge cases the planner doesn't model. Treat as "no estimate"
		// rather than an error: the query is syntactically valid (EXPLAIN itself
		// didn't fail) so there's nothing to gate on.
		return 0, nil
	}
	return extractEstimatedRows(plan.Arguments()), nil
}

// extractEstimatedRows pulls the root operator's EstimatedRows out of the plan
// Arguments map. The Arguments map is populated by the driver from the server's
// Bolt-level plan representation, where numeric values may arrive as float64
// (JSON-style) or int64 (Bolt integer) depending on server version and plan type.
// We handle both, plus int for safety, and clamp negative values to 0 (they would
// indicate a protocol bug rather than a meaningful estimate).
//
// A missing EstimatedRows key returns 0 — which the caller interprets as "no
// estimate available, skip the guard". Same contract as plan == nil.
func extractEstimatedRows(args map[string]any) int64 {
	if args == nil {
		return 0
	}
	raw, ok := args["EstimatedRows"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case float64:
		if v < 0 {
			return 0
		}
		return int64(v)
	case int64:
		if v < 0 {
			return 0
		}
		return v
	case int:
		if v < 0 {
			return 0
		}
		return int64(v)
	}
	return 0
}

// firstKeyword returns the first Cypher keyword in the query, uppercased. Leading
// whitespace and comments (both /* ... */ block comments and // ... line comments)
// are stripped before the first token is taken. Returns the empty string for an
// input that is empty or entirely whitespace/comments.
//
// This is deliberately a byte-level string scan rather than a real parser — it's
// only used as a pre-flight check to detect the two leading verbs (PROFILE and
// EXPLAIN) that interact badly with the EXPLAIN-wrap classifier. Anything more
// sophisticated than "what's the first word" should go through the planner.
func firstKeyword(query string) string {
	s := stripLeadingWhitespaceAndComments(query)
	end := strings.IndexFunc(s, unicode.IsSpace)
	if end < 0 {
		end = len(s)
	}
	return strings.ToUpper(s[:end])
}

// stripLeadingWhitespaceAndComments removes leading whitespace and any number of
// leading /* ... */ or // ... comments from the query. It stops at the first
// non-whitespace, non-comment byte.
//
// A malformed block comment with no closing "*/" is returned as-is on the
// principle that the downstream parser will produce a better error message for
// it than this function could.
func stripLeadingWhitespaceAndComments(query string) string {
	for {
		query = strings.TrimLeftFunc(query, unicode.IsSpace)
		if strings.HasPrefix(query, "/*") {
			end := strings.Index(query, "*/")
			if end < 0 {
				return query
			}
			query = query[end+2:]
			continue
		}
		if strings.HasPrefix(query, "//") {
			end := strings.IndexByte(query, '\n')
			if end < 0 {
				return ""
			}
			query = query[end+1:]
			continue
		}
		return query
	}
}

// accessMode selects between session.ExecuteRead and session.ExecuteWrite inside
// executeStreaming. It's a private enum rather than reusing neo4j.AccessMode to
// keep the dispatch local to this file — the driver distinguishes via two named
// methods, not a mode argument.
type accessMode int

const (
	accessRead accessMode = iota
	accessWrite
)

// ExecuteReadQueryStreaming runs a read-only Cypher query using the session +
// ExecuteRead + manual-iteration pattern. See QueryExecutor.ExecuteReadQueryStreaming
// on the interface for the contract.
//
// Note: we deliberately do NOT use neo4j.ExecuteQuery here (as ExecuteReadQuery
// above does) because ExecuteQuery uses EagerResultTransformer, which buffers
// every record in driver memory before returning. The docs are explicit about
// this — "ExecuteQuery always retrieves all result records at once (it is what
// the Eager in EagerResult stands for)". For a multi-million-row unbounded
// query that behaviour hangs the MCP client for minutes while the driver
// serialises the full payload. The session API lets us iterate with an early break.
func (s *Neo4jService) ExecuteReadQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error) {
	return s.executeStreaming(ctx, cypher, params, maxRows, maxBytes, accessRead)
}

// ExecuteWriteQueryStreaming runs a Cypher query in a write transaction, with
// the same row and byte cap semantics as ExecuteReadQueryStreaming.
func (s *Neo4jService) ExecuteWriteQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error) {
	return s.executeStreaming(ctx, cypher, params, maxRows, maxBytes, accessWrite)
}

// executeStreaming is the shared implementation for the read and write streaming
// paths. It builds a session with the appropriate credentials (STDIO: driver
// defaults; HTTP: per-request token from ctx, same logic as buildQueryOptions),
// runs the query inside an ExecuteRead/Write managed transaction, iterates the
// result with an early break when either cap is reached, and on truncation calls
// Consume to ensure the server is told to stop streaming rather than continuing
// to push records into a discard buffer.
//
// Caps are additive: iteration stops on the first cap that trips. The
// TruncationReason on the returned QueryResult indicates which one fired so the
// MCP-facing hint can steer the caller toward the right remediation (smaller
// LIMIT for rows, narrower projection for bytes).
func (s *Neo4jService) executeStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int, mode accessMode) (*QueryResult, error) {
	sessionConfig := neo4j.SessionConfig{
		DatabaseName: s.database,
	}

	// HTTP mode: each request carries its own credentials on the context (Bearer or
	// Basic). SessionConfig.Auth is the v6 equivalent of ExecuteQueryWithAuthToken —
	// it scopes the auth token to this session only, leaving the driver-level
	// credentials untouched. STDIO mode falls through and uses the driver's
	// built-in credentials (no Auth override needed).
	if s.transportMode == config.TransportModeHTTP {
		if authToken := s.getHTTPAuthToken(ctx); authToken != nil {
			sessionConfig.Auth = authToken
		}
	}

	session := s.driver.NewSession(ctx, sessionConfig)
	defer func() {
		// Session.Close returns a connection to the pool. Failing to close is not
		// fatal for the query's result, so we log and carry on — pool starvation
		// will surface via ConnectionAcquisitionTimeout on subsequent calls if this
		// becomes a pattern.
		if closeErr := session.Close(ctx); closeErr != nil {
			slog.Warn("error closing session", "error", closeErr)
		}
	}()

	// Tag the transaction with the same metadata as the eager path so operators
	// can correlate MCP traffic in SHOW TRANSACTIONS / query.log.
	txMetadata := neo4j.WithTxMetadata(map[string]any{
		"app": strings.Join([]string{appName, s.neo4jMCPVersion}, "/"),
	})

	work := func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}

		records := make([]*neo4j.Record, 0)
		truncated := false
		truncationReason := TruncationReasonNone
		byteCount := 0

	streamLoop:
		for res.Next(ctx) {
			record := res.Record()

			// Byte cap: measure this record's JSON size before committing to include
			// it. We marshal record.AsMap() — which is exactly what QueryResultToJSON
			// will use downstream — so the measurement reflects the real on-wire cost
			// of returning this row. Overhead (array separators, envelope wrapping)
			// is small enough that we don't account for it explicitly; the cap default
			// already leaves headroom for it under the transport's 1 MB ceiling.
			//
			// If the record fails to marshal we log and skip the byte measurement,
			// still including the record in the result — a marshalling failure here
			// would be caught again in QueryResultToJSON with a clearer error.
			//
			// The check is "including this record WOULD exceed the cap", not "HAS
			// exceeded", so we can stop before adding rather than after. The edge
			// case where the very first record alone exceeds the cap is handled by
			// allowing one record through: without this, a single wide row would
			// return zero results, which is worse UX than returning one row with
			// truncated=true and a clear reason.
			if maxBytes > 0 && len(records) > 0 {
				recordBytes, mErr := json.Marshal(record.AsMap())
				// The bare `break` in the byte-cap case would only break the switch —
				// we want to exit the outer res.Next loop, hence the streamLoop label.
				switch {
				case mErr != nil:
					slog.Debug("failed to measure record size, including without byte accounting", "error", mErr)
				case byteCount+len(recordBytes) > maxBytes:
					truncated = true
					truncationReason = TruncationReasonBytes
					if _, discardErr := res.Consume(ctx); discardErr != nil {
						slog.Debug("discard after truncation returned an error (non-fatal)", "error", discardErr)
					}
					break streamLoop
				default:
					byteCount += len(recordBytes)
				}
			} else if maxBytes > 0 {
				// First record: always admit, but still count its bytes so that the
				// reported ByteCount reflects what was actually returned.
				if recordBytes, mErr := json.Marshal(record.AsMap()); mErr == nil {
					byteCount += len(recordBytes)
				}
			}

			// Row cap: stop after appending this record if we've hit the ceiling.
			// The check happens BEFORE appending so the row cap semantic ("at most
			// maxRows records returned") is preserved exactly.
			if maxRows > 0 && len(records) >= maxRows {
				truncated = true
				truncationReason = TruncationReasonRows
				// Drain the rest of the stream via Consume so the driver sends DISCARD
				// on the wire and the server stops pushing records. Errors here are
				// non-fatal — we already have the rows we want to return — so they
				// are logged at debug level and otherwise swallowed.
				if _, discardErr := res.Consume(ctx); discardErr != nil {
					slog.Debug("discard after truncation returned an error (non-fatal)", "error", discardErr)
				}
				break
			}
			records = append(records, record)
		}
		if err := res.Err(); err != nil {
			return nil, err
		}

		return &QueryResult{
			Records:          records,
			Truncated:        truncated,
			TruncationReason: truncationReason,
			RowCount:         len(records),
			MaxRows:          maxRows,
			ByteCount:        byteCount,
			MaxBytes:         maxBytes,
		}, nil
	}

	var raw any
	var err error
	if mode == accessRead {
		raw, err = session.ExecuteRead(ctx, work, txMetadata)
	} else {
		raw, err = session.ExecuteWrite(ctx, work, txMetadata)
	}
	if err != nil {
		// Context-cancellation errors (timeout or caller cancellation) need to
		// surface unwrapped so the handler's errors.Is checks work — otherwise we
		// bury context.DeadlineExceeded inside a fmt.Errorf chain and the handler
		// can't distinguish a runaway query from a real driver failure. The
		// previous passthrough classified timeouts as "ConnectivityError: context
		// deadline exceeded", which sent operators hunting network issues when the
		// actual cause was a query that never finished. Log at Info for cancellation
		// (expected under load shedding or user abort) and let the handler produce
		// the user-facing message with the configured timeout value.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			slog.Info("streaming query cancelled", "reason", err)
			return nil, err
		}
		wrapped := fmt.Errorf("failed to execute streaming query: %w", err)
		slog.Error("Error in executeStreaming", "error", wrapped)
		return nil, wrapped
	}

	result, ok := raw.(*QueryResult)
	if !ok {
		// Defensive — the work function above only ever returns *QueryResult, so
		// this branch should be unreachable. If we ever reach it we'd rather surface
		// a clear error than panic on a bad type assertion.
		return nil, fmt.Errorf("unexpected return type from transaction work: %T", raw)
	}
	return result, nil
}

// Neo4jRecordsToJSON converts Neo4j records to JSON string. Each record's
// AsMap() output is run through convertMapToTagged first so driver types
// (dbtype.Node, dbtype.Date, dbtype.Duration, etc.) emerge as the MCP-facing
// camelCase shapes rather than as PascalCase Go struct reflections. See
// json_tagged_values.go for the full conversion contract and the per-type
// rationale.
func (s *Neo4jService) Neo4jRecordsToJSON(records []*neo4j.Record) (string, error) {
	results := make([]map[string]any, 0)
	for _, record := range records {
		results = append(results, convertMapToTagged(record.AsMap()))
	}

	formattedResponse, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		wrappedErr := fmt.Errorf("failed to format records as JSON: %w", err)
		slog.Error("Error in Neo4jRecordsToJSON", "error", wrappedErr)
		return "", wrappedErr
	}

	return string(formattedResponse), nil
}

// cypherResponse is the wire shape produced by QueryResultToJSON. It's a JSON
// envelope around the rows so that truncation is self-describing in the MCP tool
// response. Compared to a bare array (the Neo4jRecordsToJSON output), it trades
// a small amount of parsing ceremony for the ability to tell an agent "your
// result is incomplete, here's why, and here's how to fix it" without having
// to stuff that information into a separate content block or out-of-band channel.
//
// The envelope is emitted even when the result is complete (truncated=false).
// Consistent shape is easier to reason about than a conditional contract, and the
// overhead of the wrapper keys is negligible next to the rows themselves.
type cypherResponse struct {
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	Truncated bool             `json:"truncated"`
	// TruncationReason is "rows" or "bytes" on truncation, absent otherwise. It
	// lets programmatic consumers branch on the remediation without having to
	// parse the hint text.
	TruncationReason string `json:"truncationReason,omitempty"`
	// MaxRows is the row cap that was applied. Omitted when not truncated so the
	// happy-path response stays tidy; on truncation it lets the hint cite a
	// concrete number and lets programmatic consumers branch on it.
	MaxRows int `json:"maxRows,omitempty"`
	// MaxBytes is the byte cap that was applied. Semantics parallel MaxRows:
	// omitted on the happy path, surfaced on truncation for hint construction
	// and programmatic handling.
	MaxBytes int `json:"maxBytes,omitempty"`
	// Hint is populated only on truncation. It tells the caller why the result
	// is incomplete and what to do next — intended for consumption by an LLM
	// agent that may be several tool calls removed from the original request.
	Hint string `json:"hint,omitempty"`
}

// QueryResultToJSON renders a streaming QueryResult as a cypherResponse JSON
// document. A nil input is treated as an error rather than an empty envelope,
// because the only way to reach this function with nil is a handler bug we want
// to catch in tests rather than paper over.
func (s *Neo4jService) QueryResultToJSON(result *QueryResult) (string, error) {
	if result == nil {
		err := fmt.Errorf("failed to format query result as JSON: result is nil")
		slog.Error("Error in QueryResultToJSON", "error", err)
		return "", err
	}

	rows := make([]map[string]any, 0, len(result.Records))
	for _, record := range result.Records {
		// convertMapToTagged walks each record's value map and replaces raw
		// driver types with their JSON-tagged wrappers (or ISO-8601 strings
		// for temporal types). This is where the PascalCase-→camelCase and
		// temporal-empty-object fixes take effect at the output layer; see
		// json_tagged_values.go for the full per-type contract.
		rows = append(rows, convertMapToTagged(record.AsMap()))
	}

	resp := cypherResponse{
		Rows:      rows,
		RowCount:  result.RowCount,
		Truncated: result.Truncated,
	}
	if result.Truncated {
		resp.TruncationReason = result.TruncationReason
		resp.Hint = truncationHint(result)
		// Only echo the cap that actually fired. The other value may be 0 (disabled)
		// or just irrelevant; surfacing both would invite confusion about which one
		// stopped the query.
		switch result.TruncationReason {
		case TruncationReasonBytes:
			resp.MaxBytes = result.MaxBytes
		default:
			// Default includes TruncationReasonRows and the legacy empty-string case
			// (which should only occur in tests that construct a QueryResult by hand).
			resp.MaxRows = result.MaxRows
		}
	}

	formatted, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		wrapped := fmt.Errorf("failed to format query result as JSON: %w", err)
		slog.Error("Error in QueryResultToJSON", "error", wrapped)
		return "", wrapped
	}
	return string(formatted), nil
}

// truncationHint produces the human/agent-facing remediation string for a
// truncated result. The two caps need different advice: a row-cap hit means the
// caller asked for too many rows and a smaller LIMIT will solve it, but a
// byte-cap hit usually means each individual row is too wide (typically from
// returning full node objects with many properties) — lowering the LIMIT
// proportionally is inefficient when what's really needed is projecting only
// the columns the caller cares about.
//
// The hint calls out the concrete threshold so the agent has a number to adjust
// against. It's kept to a single sentence so it plays well with agents that
// summarise tool results before reasoning over them.
func truncationHint(result *QueryResult) string {
	switch result.TruncationReason {
	case TruncationReasonBytes:
		return fmt.Sprintf(
			"Results were truncated because the response size would have exceeded %d bytes. "+
				"Each row is large — try projecting fewer properties (for example RETURN n.id, n.name "+
				"instead of RETURN n) or filter down to fewer rows, then retry.",
			result.MaxBytes,
		)
	default:
		// Covers TruncationReasonRows and any legacy empty-string case.
		return fmt.Sprintf(
			"Results were truncated at %d rows. Add a LIMIT clause or a more selective filter "+
				"and retry for a complete result.",
			result.MaxRows,
		)
	}
}
