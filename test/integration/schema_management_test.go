// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"
)

// TestConstraintLifecycle exercises create-constraint, list-constraints-and-indexes,
// and drop-constraint against a real server, validating that the SHOW
// CONSTRAINTS column names this package's ConstraintInfo mapping assumes
// (id, name, type, entityType, labelsOrTypes, properties, ownedIndex,
// propertyType) actually match the server's response shape.
func TestConstraintLifecycle(t *testing.T) {
	t.Parallel()

	tc := helpers.NewTestContext(t, dbs.GetDriver())
	personLabel := tc.GetUniqueLabel("Person")

	create := cypher.CreateConstraintHandler(tc.Deps)
	createRes := tc.CallTool(create, map[string]any{
		"entityType":     "NODE",
		"label":          personLabel.String(),
		"properties":     []any{"email"},
		"constraintType": "UNIQUENESS",
	})

	var createOut struct {
		Constraint cypher.ConstraintInfo `json:"constraint"`
	}
	tc.ParseJSONResponse(createRes, &createOut)
	if createOut.Constraint.Name == "" {
		t.Fatalf("expected a generated constraint name, got: %+v", createOut.Constraint)
	}
	if createOut.Constraint.EntityType != "NODE" {
		t.Errorf("expected entityType NODE, got %q", createOut.Constraint.EntityType)
	}
	name := createOut.Constraint.Name

	list := cypher.ListConstraintsAndIndexesHandler(tc.Deps)
	listRes := tc.CallTool(list, map[string]any{})
	var listOut struct {
		Constraints []cypher.ConstraintInfo `json:"constraints"`
	}
	tc.ParseJSONResponse(listRes, &listOut)
	found := false
	for _, c := range listOut.Constraints {
		if c.Name == name {
			found = true
			if len(c.LabelsOrTypes) != 1 || c.LabelsOrTypes[0] != personLabel.String() {
				t.Errorf("expected labelsOrTypes [%q], got %v", personLabel.String(), c.LabelsOrTypes)
			}
			if len(c.Properties) != 1 || c.Properties[0] != "email" {
				t.Errorf("expected properties [\"email\"], got %v", c.Properties)
			}
		}
	}
	if !found {
		t.Fatalf("created constraint %q not found in list-constraints-and-indexes output", name)
	}

	drop := cypher.DropConstraintHandler(tc.Deps)
	dropRes := tc.CallTool(drop, map[string]any{"name": name})
	var dropOut struct {
		Name    string `json:"name"`
		Existed bool   `json:"existed"`
	}
	tc.ParseJSONResponse(dropRes, &dropOut)
	if !dropOut.Existed {
		t.Errorf("expected existed=true for a constraint that was just created")
	}

	// Second drop of the same (now-gone) name should report existed=false,
	// not error — DROP CONSTRAINT ... IF EXISTS is idempotent.
	dropAgainRes := tc.CallTool(drop, map[string]any{"name": name})
	var dropAgainOut struct {
		Existed bool `json:"existed"`
	}
	tc.ParseJSONResponse(dropAgainRes, &dropAgainOut)
	if dropAgainOut.Existed {
		t.Errorf("expected existed=false on second drop, constraint should already be gone")
	}
}

// TestIndexLifecycle mirrors TestConstraintLifecycle for create-index,
// list-constraints-and-indexes, and drop-index, validating the SHOW INDEXES
// column names this package's IndexInfo mapping assumes.
func TestIndexLifecycle(t *testing.T) {
	t.Parallel()

	tc := helpers.NewTestContext(t, dbs.GetDriver())
	personLabel := tc.GetUniqueLabel("Person")

	create := cypher.CreateIndexHandler(tc.Deps)
	createRes := tc.CallTool(create, map[string]any{
		"indexType":  "RANGE",
		"entityType": "NODE",
		"label":      personLabel.String(),
		"properties": []any{"age"},
	})

	var createOut struct {
		Index cypher.IndexInfo `json:"index"`
	}
	tc.ParseJSONResponse(createRes, &createOut)
	if createOut.Index.Name == "" {
		t.Fatalf("expected a generated index name, got: %+v", createOut.Index)
	}
	if createOut.Index.Type != "RANGE" {
		t.Errorf("expected type RANGE, got %q", createOut.Index.Type)
	}
	name := createOut.Index.Name

	list := cypher.ListConstraintsAndIndexesHandler(tc.Deps)
	listRes := tc.CallTool(list, map[string]any{})
	var listOut struct {
		Indexes []cypher.IndexInfo `json:"indexes"`
	}
	tc.ParseJSONResponse(listRes, &listOut)
	found := false
	for _, idx := range listOut.Indexes {
		if idx.Name == name {
			found = true
			if len(idx.Properties) != 1 || idx.Properties[0] != "age" {
				t.Errorf("expected properties [\"age\"], got %v", idx.Properties)
			}
		}
	}
	if !found {
		t.Fatalf("created index %q not found in list-constraints-and-indexes output", name)
	}

	drop := cypher.DropIndexHandler(tc.Deps)
	dropRes := tc.CallTool(drop, map[string]any{"name": name})
	var dropOut struct {
		Existed bool `json:"existed"`
	}
	tc.ParseJSONResponse(dropRes, &dropOut)
	if !dropOut.Existed {
		t.Errorf("expected existed=true for an index that was just created")
	}
}
