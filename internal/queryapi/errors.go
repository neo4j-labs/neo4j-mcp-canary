// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"errors"
	"fmt"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// wrapQueryAPIError translates errors surfaced by the query-go-sdk into the
// shapes internal/tools/cypher/read_cypher_handler.go already knows how to
// classify, so that handler needs no Query-API-specific branches:
//
//   - context.DeadlineExceeded / context.Canceled already satisfy errors.Is
//     through the SDK (per its README) and are returned unwrapped so the
//     handler's `errors.Is(err, context.DeadlineExceeded)` checks keep
//     working exactly as they do for the Bolt driver.
//   - *query.QueryErrors (one or more Cypher-level errors from Neo4j) is
//     translated into a *neo4j.Neo4jError built from the first error's Code
//     and Message. This specifically preserves the read-cypher handler's
//     `neo4jErr.Code == "Neo.ClientError.Statement.AccessMode"` check, which
//     catches a write clause that slipped past the leading-verb/EXPLAIN
//     classification (SET/REMOVE after an opening MATCH is the known case
//     for the Bolt path; the same Neo4j error code is returned by the Query
//     API for the same situation).
//   - Anything else (notably *query.Error, the HTTP-level failure type) is
//     wrapped with %w so it's still inspectable via errors.As/errors.Is, but
//     isn't given special handling since it isn't policy-relevant the way
//     QueryErrors is.
func wrapQueryAPIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}

	var queryErrs *query.QueryErrors
	if errors.As(err, &queryErrs) && len(queryErrs.Errors) > 0 {
		first := queryErrs.Errors[0]
		return &neo4j.Neo4jError{Code: first.Code, Msg: first.Message}
	}

	return fmt.Errorf("query api request failed: %w", err)
}
