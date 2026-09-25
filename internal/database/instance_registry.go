// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"context"
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

var _ Service = (*InstanceRegistry)(nil)

// InstanceRegistry implements Service by dispatching every call to the
// underlying Service for whichever Neo4j instance the current request
// selected (see auth.WithInstanceSelection — set once per HTTP route at
// server startup, not parsed per request). This is the only place
// multi-instance HTTP mode needs to exist: every tool handler in
// internal/tools/** keeps calling deps.DBService.ExecuteReadQueryStreaming
// etc. exactly as it does today, unaware that DBService might be routing to
// one of several instances.
type InstanceRegistry struct {
	services map[string]Service
}

// NewInstanceRegistry builds a registry from a name->Service map, one entry
// per configured neo4j_instances entry. Panics on an empty map: a registry
// with no instances can never serve a request, which is a startup-time
// wiring bug in the caller, not a runtime condition to handle gracefully.
func NewInstanceRegistry(services map[string]Service) *InstanceRegistry {
	if len(services) == 0 {
		panic("database: NewInstanceRegistry requires at least one instance")
	}
	return &InstanceRegistry{services: services}
}

// resolve looks up the Service for the instance the current request
// selected. Both error cases below are defensive: the instance-selection
// context value and the registry are built from the same config.Instances
// list, so an HTTP request that reaches a tool handler at all has already
// passed through that instance's own dedicated route middleware.
func (r *InstanceRegistry) resolve(ctx context.Context) (Service, error) {
	name, ok := auth.GetInstanceSelection(ctx)
	if !ok {
		return nil, fmt.Errorf("no Neo4j instance selected for this request")
	}
	svc, ok := r.services[name]
	if !ok {
		return nil, fmt.Errorf("unknown Neo4j instance %q", name)
	}
	return svc, nil
}

func (r *InstanceRegistry) ExecuteReadQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return svc.ExecuteReadQuery(ctx, cypher, params)
}

func (r *InstanceRegistry) ExecuteWriteQuery(ctx context.Context, cypher string, params map[string]any) ([]*neo4j.Record, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return svc.ExecuteWriteQuery(ctx, cypher, params)
}

func (r *InstanceRegistry) ExecuteReadQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return svc.ExecuteReadQueryStreaming(ctx, cypher, params, maxRows, maxBytes)
}

func (r *InstanceRegistry) ExecuteWriteQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return svc.ExecuteWriteQueryStreaming(ctx, cypher, params, maxRows, maxBytes)
}

func (r *InstanceRegistry) GetQueryType(ctx context.Context, cypher string, params map[string]any) (neo4j.QueryType, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return neo4j.QueryTypeUnknown, err
	}
	return svc.GetQueryType(ctx, cypher, params)
}

func (r *InstanceRegistry) EstimateRowCount(ctx context.Context, cypher string, params map[string]any) (int64, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return 0, err
	}
	return svc.EstimateRowCount(ctx, cypher, params)
}

func (r *InstanceRegistry) ExplainQuery(ctx context.Context, cypher string, params map[string]any) (neo4j.Plan, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return svc.ExplainQuery(ctx, cypher, params)
}

func (r *InstanceRegistry) ExecuteProfileQueryStreaming(ctx context.Context, cypher string, params map[string]any, maxRows, maxBytes int) (*QueryResult, neo4j.QueryProfile, error) {
	svc, err := r.resolve(ctx)
	if err != nil {
		return nil, nil, err
	}
	return svc.ExecuteProfileQueryStreaming(ctx, cypher, params, maxRows, maxBytes)
}

// Neo4jRecordsToJSON and QueryResultToJSON are pure formatting functions —
// they don't depend on which Neo4j instance produced the records/result —
// so they delegate directly to the free functions every Service
// implementation already shares, without needing instance resolution.

func (r *InstanceRegistry) Neo4jRecordsToJSON(records []*neo4j.Record) (string, error) {
	return FormatRecordsAsJSON(records)
}

func (r *InstanceRegistry) QueryResultToJSON(result *QueryResult) (string, error) {
	return FormatQueryResultAsJSON(result)
}

func (r *InstanceRegistry) VerifyConnectivity(ctx context.Context) error {
	svc, err := r.resolve(ctx)
	if err != nil {
		return err
	}
	return svc.VerifyConnectivity(ctx)
}
