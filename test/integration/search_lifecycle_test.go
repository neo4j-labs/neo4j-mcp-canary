// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

// waitForIndexOnline polls SHOW INDEXES until name reaches state ONLINE or
// the timeout elapses. CREATE VECTOR/FULLTEXT INDEX populates
// asynchronously — create-vector-index/create-fulltext-index deliberately
// don't block on this themselves (their IndexInfo output already surfaces
// state/populationPercent so a real caller can decide whether to wait), but
// a test running many parallel subtests against one shared container can
// see population take noticeably longer under contention than in isolation,
// so the test needs to wait rather than assume "population percent was
// already 100% at create time" holds under load.
func waitForIndexOnline(t *testing.T, tc *helpers.TestContext, indexName string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		records, err := tc.Service.ExecuteReadQuery(context.Background(),
			"SHOW INDEXES YIELD name, state WHERE name = $name",
			map[string]any{"name": indexName})
		if err != nil {
			t.Fatalf("failed to poll index state: %v", err)
		}
		if len(records) == 1 {
			if state, ok := records[0].Get("state"); ok && state == "ONLINE" {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("index %q did not reach ONLINE state within 30s", indexName)
}

// TestVectorSearchLifecycle exercises create-vector-index, set-vector-property,
// and vector-search end to end against a real server — the SEARCH clause,
// the WITH [...] filterable-properties clause, and db.create.setNodeVectorProperty
// have not been run against a live server anywhere else in this codebase, so
// this is where a real syntax or type-binding error would surface (the same
// way the SHOW ... YIELD/WHERE ordering bug in create-constraint/create-index
// was only caught by an integration test, never by mocked unit tests).
//
// Note: SeedNode's returned UniqueLabel is already test-ID-suffixed — pass
// it straight through to create-vector-index's "label" field. Re-deriving a
// label via GetUniqueLabel and then also passing it through SeedNode double-
// suffixes it, silently creating the index against a label no seeded node
// actually carries (a real mistake made and caught while writing this test).
func TestVectorSearchLifecycle(t *testing.T) {
	t.Parallel()

	tc := helpers.NewTestContext(t, dbs.GetDriver())

	closeLabel, err := tc.SeedNode("Doc", map[string]any{"tenantId": "acme", "name": "close"})
	if err != nil {
		t.Fatalf("failed to seed 'close' node: %v", err)
	}
	// Same base label ("Doc") from the same TestContext resolves to the same
	// unique label every time (it's derived from base+TestID, not random),
	// so these both land under closeLabel too.
	if _, err := tc.SeedNode("Doc", map[string]any{"tenantId": "acme", "name": "far"}); err != nil {
		t.Fatalf("failed to seed 'far' node: %v", err)
	}
	if _, err := tc.SeedNode("Doc", map[string]any{"tenantId": "other", "name": "other-tenant"}); err != nil {
		t.Fatalf("failed to seed other-tenant node: %v", err)
	}

	create := search.CreateVectorIndexHandler(tc.Deps)
	createRes := tc.CallTool(create, map[string]any{
		"entityType":           "NODE",
		"label":                closeLabel.String(),
		"property":             "embedding",
		"dimensions":           4,
		"filterableProperties": []any{"tenantId"},
	})

	var createOut struct {
		Index  search.IndexInfo         `json:"index"`
		Config search.VectorIndexConfig `json:"config"`
	}
	tc.ParseJSONResponse(createRes, &createOut)
	if createOut.Index.Name == "" {
		t.Fatalf("expected a generated index name, got: %+v", createOut.Index)
	}
	if createOut.Config.Dimensions != 4 || createOut.Config.SimilarityFunction != "cosine" {
		t.Errorf("unexpected resolved config: %+v", createOut.Config)
	}
	indexName := createOut.Index.Name
	waitForIndexOnline(t, tc, indexName)

	setVec := search.SetVectorPropertyHandler(tc.Deps)
	tc.CallTool(setVec, map[string]any{
		"entityType":     "NODE",
		"label":          closeLabel.String(),
		"filters":        []any{map[string]any{"property": "name", "operator": "=", "value": "close"}},
		"vectorProperty": "embedding",
		"vector":         []any{1.0, 0.0, 0.0, 0.0},
	})
	tc.CallTool(setVec, map[string]any{
		"entityType":     "NODE",
		"label":          closeLabel.String(),
		"filters":        []any{map[string]any{"property": "name", "operator": "=", "value": "far"}},
		"vectorProperty": "embedding",
		"vector":         []any{0.0, 0.0, 0.0, 1.0},
	})
	tc.CallTool(setVec, map[string]any{
		"entityType":     "NODE",
		"label":          closeLabel.String(),
		"filters":        []any{map[string]any{"property": "name", "operator": "=", "value": "other-tenant"}},
		"vectorProperty": "embedding",
		"vector":         []any{1.0, 0.0, 0.0, 0.0},
	})

	vectorSearch := search.VectorSearchHandler(tc.Deps)
	searchRes := tc.CallTool(vectorSearch, map[string]any{
		"indexName":   indexName,
		"queryVector": []any{1.0, 0.0, 0.0, 0.0},
		"topK":        5,
		"filters":     []any{map[string]any{"property": "tenantId", "operator": "=", "value": "acme"}},
	})

	var searchOut struct {
		Rows []map[string]any `json:"rows"`
	}
	tc.ParseJSONResponse(searchRes, &searchOut)
	if len(searchOut.Rows) != 2 {
		t.Fatalf("expected exactly 2 rows (tenant filter should exclude the other-tenant node), got %d: %+v", len(searchOut.Rows), searchOut.Rows)
	}

	entity, ok := searchOut.Rows[0]["entity"].(map[string]any)
	if !ok {
		t.Fatalf("expected rows[0].entity to be a map, got %T", searchOut.Rows[0]["entity"])
	}
	props, ok := entity["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected entity.properties to be a map, got %T", entity["properties"])
	}
	if props["name"] != "close" {
		t.Errorf("expected the closest match to be 'close', got %v (full rows: %+v)", props["name"], searchOut.Rows)
	}
	if _, hasScore := searchOut.Rows[0]["score"]; !hasScore {
		t.Errorf("expected a score field on the result row, got: %+v", searchOut.Rows[0])
	}
}

// TestSetVectorPropertyDimensionCheck exercises set-vector-property's
// dimension check against a real vector index — a vector of the wrong size
// must be rejected before any write, using the real SHOW INDEXES options
// column (internal/tools/search/embedding.go's vectorIndexDimensionsByLabelOrType)
// rather than a mocked one. It doesn't exercise the `text` field or
// check-embedding-dimensions' embedding-generation path, since those need a
// real GenAI provider credential this test suite doesn't have.
func TestSetVectorPropertyDimensionCheck(t *testing.T) {
	t.Parallel()

	tc := helpers.NewTestContext(t, dbs.GetDriver())

	label, err := tc.SeedNode("Doc", map[string]any{"name": "only"})
	if err != nil {
		t.Fatalf("failed to seed node: %v", err)
	}

	create := search.CreateVectorIndexHandler(tc.Deps)
	createRes := tc.CallTool(create, map[string]any{
		"entityType": "NODE",
		"label":      label.String(),
		"property":   "embedding",
		"dimensions": 4,
	})
	var createOut struct {
		Index search.IndexInfo `json:"index"`
	}
	tc.ParseJSONResponse(createRes, &createOut)
	waitForIndexOnline(t, tc, createOut.Index.Name)

	setVec := search.SetVectorPropertyHandler(tc.Deps)
	errMsg := tc.GetToolError(setVec, map[string]any{
		"entityType":     "NODE",
		"label":          label.String(),
		"filters":        []any{map[string]any{"property": "name", "operator": "=", "value": "only"}},
		"vectorProperty": "embedding",
		"vector":         []any{1.0, 0.0, 0.0}, // 3 dims, index expects 4
	})
	if errMsg == "" {
		t.Fatal("expected a dimension-mismatch error message")
	}

	records, err := tc.Service.ExecuteReadQuery(context.Background(),
		"MATCH (n) WHERE $label IN labels(n) AND n.name = 'only' RETURN n.embedding AS embedding",
		map[string]any{"label": label.String()})
	if err != nil {
		t.Fatalf("failed to verify node state: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 matching node, got %d", len(records))
	}
	if embedding, _ := records[0].Get("embedding"); embedding != nil {
		t.Errorf("expected no embedding to have been written after a rejected mismatch, got: %v", embedding)
	}

	check := search.CheckEmbeddingDimensionsHandler(tc.Deps)
	t.Run("no embedding provider configured on this test instance", func(t *testing.T) {
		errMsg := tc.GetToolError(check, map[string]any{"indexName": createOut.Index.Name})
		if errMsg == "" {
			t.Fatal("expected an error naming the missing embedding provider")
		}
	})
}

// TestFullTextSearchLifecycle exercises create-fulltext-index and
// fulltext-search end to end — the SEARCH-clause full-text support this
// depends on only shipped in Neo4j 2026.09, so this is the first live-server
// exercise of that specific syntax in this codebase.
func TestFullTextSearchLifecycle(t *testing.T) {
	t.Parallel()

	tc := helpers.NewTestContext(t, dbs.GetDriver())

	matchLabel, err := tc.SeedNode("Article", map[string]any{
		"title": "Graph databases explained",
		"body":  "Neo4j is a native graph database with a powerful query language.",
	})
	if err != nil {
		t.Fatalf("failed to seed matching node: %v", err)
	}
	if _, err := tc.SeedNode("Article", map[string]any{
		"title": "Unrelated topic",
		"body":  "This article is about something else entirely.",
	}); err != nil {
		t.Fatalf("failed to seed non-matching node: %v", err)
	}

	create := search.CreateFullTextIndexHandler(tc.Deps)
	createRes := tc.CallTool(create, map[string]any{
		"entityType": "NODE",
		"labels":     []any{matchLabel.String()},
		"properties": []any{"title", "body"},
	})

	var createOut struct {
		Index search.IndexInfo `json:"index"`
	}
	tc.ParseJSONResponse(createRes, &createOut)
	if createOut.Index.Name == "" {
		t.Fatalf("expected a generated index name, got: %+v", createOut.Index)
	}
	indexName := createOut.Index.Name
	waitForIndexOnline(t, tc, indexName)

	fulltextSearch := search.FullTextSearchHandler(tc.Deps)
	searchRes := tc.CallTool(fulltextSearch, map[string]any{
		"indexName":   indexName,
		"queryString": "graph",
		"topK":        5,
	})

	var searchOut struct {
		Rows []map[string]any `json:"rows"`
	}
	tc.ParseJSONResponse(searchRes, &searchOut)
	if len(searchOut.Rows) != 1 {
		t.Fatalf("expected exactly 1 matching row, got %d: %+v", len(searchOut.Rows), searchOut.Rows)
	}
	entity, ok := searchOut.Rows[0]["entity"].(map[string]any)
	if !ok {
		t.Fatalf("expected rows[0].entity to be a map, got %T", searchOut.Rows[0]["entity"])
	}
	props, ok := entity["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected entity.properties to be a map, got %T", entity["properties"])
	}
	if props["title"] != "Graph databases explained" {
		t.Errorf("expected the matching article, got %v", props["title"])
	}
}
