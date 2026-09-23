// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"

	"go.uber.org/mock/gomock"
)

func TestNewInstanceRegistry_PanicsOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected NewInstanceRegistry to panic on an empty map")
		}
	}()
	database.NewInstanceRegistry(map[string]database.Service{})
}

func TestInstanceRegistry_DispatchesToSelectedInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prod := db.NewMockService(ctrl)
	staging := db.NewMockService(ctrl)

	ctx := context.Background()
	params := map[string]any{}

	prod.EXPECT().ExecuteReadQuery(gomock.Any(), "RETURN 1", params).Return(nil, nil)
	staging.EXPECT().ExecuteReadQuery(gomock.Any(), "RETURN 1", params).Times(0)

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod":    prod,
		"staging": staging,
	})

	ctxProd := auth.WithInstanceSelection(ctx, "prod")
	if _, err := registry.ExecuteReadQuery(ctxProd, "RETURN 1", params); err != nil {
		t.Fatalf("ExecuteReadQuery() unexpected error: %v", err)
	}
}

func TestInstanceRegistry_NoInstanceSelected(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod": db.NewMockService(ctrl),
	})

	_, err := registry.ExecuteReadQuery(context.Background(), "RETURN 1", nil)
	if err == nil || !strings.Contains(err.Error(), "no Neo4j instance selected") {
		t.Fatalf("ExecuteReadQuery() error = %v, want 'no Neo4j instance selected'", err)
	}
}

func TestInstanceRegistry_UnknownInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod": db.NewMockService(ctrl),
	})

	ctx := auth.WithInstanceSelection(context.Background(), "does-not-exist")
	_, err := registry.ExecuteReadQuery(ctx, "RETURN 1", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown Neo4j instance "does-not-exist"`) {
		t.Fatalf("ExecuteReadQuery() error = %v, want unknown-instance error", err)
	}
}

func TestInstanceRegistry_FormattingMethodsNeedNoInstanceSelection(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod": db.NewMockService(ctrl),
	})

	// No auth.WithInstanceSelection on this context at all — formatting is
	// pure and must not require (or fail on) instance resolution.
	if _, err := registry.Neo4jRecordsToJSON(nil); err != nil {
		t.Errorf("Neo4jRecordsToJSON() unexpected error: %v", err)
	}
	if _, err := registry.QueryResultToJSON(&database.QueryResult{}); err != nil {
		t.Errorf("QueryResultToJSON() unexpected error: %v", err)
	}
}
