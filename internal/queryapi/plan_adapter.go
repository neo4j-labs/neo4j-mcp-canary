// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"time"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// queryAPIPlan adapts query.PlanOperator (the Query API's EXPLAIN plan node
// shape) to the neo4j.Plan interface, so ExplainQuery can return the same
// type regardless of which transport (Bolt or Query API) produced it.
type queryAPIPlan struct {
	op *query.PlanOperator
}

func (p *queryAPIPlan) Operator() string          { return p.op.OperatorType }
func (p *queryAPIPlan) Arguments() map[string]any { return p.op.Arguments }
func (p *queryAPIPlan) Identifiers() []string     { return p.op.Identifiers }

func (p *queryAPIPlan) Children() []neo4j.Plan {
	children := make([]neo4j.Plan, len(p.op.Children))
	for i, c := range p.op.Children {
		children[i] = &queryAPIPlan{op: c}
	}
	return children
}

// queryAPIProfile adapts query.PlanOperator to neo4j.QueryProfile for a
// PROFILE statement's plan node. The Query API has no dedicated
// DbHits/Rows/PageCache*/Time fields the way query.PlanOperator's shape
// might suggest — the server reports them as ordinary entries inside the
// same Arguments map EXPLAIN's EstimatedRows lives in, using the same
// lowerCamelCase wire keys the Bolt protocol uses for its PROFILE struct
// ("dbHits", "rows", "pageCacheHits", "pageCacheMisses",
// "pageCacheHitRatio", "time"). This has been verified against the
// query-go-sdk and neo4j-go-driver source (see ExtractEstimatedRows in
// internal/database/service.go for the equivalent EXPLAIN-side key), not
// just documentation; it is still exercised end-to-end by the
// profile-cypher integration test against a live server, since the Query
// API's actual wire JSON was not directly inspected.
type queryAPIProfile struct {
	op *query.PlanOperator
}

func (p *queryAPIProfile) Operator() string          { return p.op.OperatorType }
func (p *queryAPIProfile) Arguments() map[string]any { return p.op.Arguments }
func (p *queryAPIProfile) Identifiers() []string     { return p.op.Identifiers }

func (p *queryAPIProfile) Children() []neo4j.QueryProfile {
	children := make([]neo4j.QueryProfile, len(p.op.Children))
	for i, c := range p.op.Children {
		children[i] = &queryAPIProfile{op: c}
	}
	return children
}

func (p *queryAPIProfile) DbHits() (int64, bool) { //nolint:staticcheck // method name must match neo4j.QueryProfile's own DbHits (not DBHits)
	return extractProfileInt64(p.op.Arguments, "dbHits")
}

func (p *queryAPIProfile) Rows() (int64, bool) {
	return extractProfileInt64(p.op.Arguments, "rows")
}

func (p *queryAPIProfile) PageCacheHits() (int64, bool) {
	return extractProfileInt64(p.op.Arguments, "pageCacheHits")
}

func (p *queryAPIProfile) PageCacheMisses() (int64, bool) {
	return extractProfileInt64(p.op.Arguments, "pageCacheMisses")
}

func (p *queryAPIProfile) PageCacheHitRatio() (float64, bool) {
	raw, ok := p.op.Arguments["pageCacheHitRatio"]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func (p *queryAPIProfile) Time() (time.Duration, bool) {
	millis, ok := extractProfileInt64(p.op.Arguments, "time")
	if !ok {
		return 0, false
	}
	return time.Duration(millis) * time.Millisecond, true
}

// extractProfileInt64 pulls an int64-valued profile stat out of a plan
// operator's Arguments map, tolerating the same float64/int64/int variance
// database.ExtractEstimatedRows already handles for EstimatedRows — decoded
// JSON numbers surface as one of these three depending on the value and the
// decode path.
func extractProfileInt64(args map[string]any, key string) (int64, bool) {
	raw, ok := args[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	}
	return 0, false
}

var _ neo4j.Plan = (*queryAPIPlan)(nil)
var _ neo4j.QueryProfile = (*queryAPIProfile)(nil)
