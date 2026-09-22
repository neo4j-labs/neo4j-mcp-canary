// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"context"
	"errors"
	"testing"

	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func indexInfoRecord(name string, labelsOrTypes, properties []string, owningConstraint any) *neo4j.Record {
	toAny := func(ss []string) []any {
		out := make([]any, len(ss))
		for i, s := range ss {
			out[i] = s
		}
		return out
	}
	return &neo4j.Record{
		Keys: []string{"id", "name", "state", "populationPercent", "type", "entityType", "labelsOrTypes", "properties", "indexProvider", "owningConstraint"},
		Values: []any{
			int64(7), name, "ONLINE", float64(100), "VECTOR", "NODE",
			toAny(labelsOrTypes), toAny(properties), "vector-2.0", owningConstraint,
		},
	}
}

func TestFetchIndexByName(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	t.Run("maps a full row including a null owningConstraint", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, map[string]any{"name": "doc_embeddings"}).
			Return([]*neo4j.Record{indexInfoRecord("doc_embeddings", []string{"Document"}, []string{"embedding"}, nil)}, nil)

		deps := &tools.ToolDependencies{DBService: mockDB}
		info, err := fetchIndexByName(context.Background(), deps, "doc_embeddings")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Name != "doc_embeddings" || info.State != "ONLINE" || info.Type != "VECTOR" {
			t.Errorf("unexpected IndexInfo: %+v", info)
		}
		if len(info.LabelsOrTypes) != 1 || info.LabelsOrTypes[0] != "Document" {
			t.Errorf("unexpected LabelsOrTypes: %v", info.LabelsOrTypes)
		}
		if info.OwningConstraint != "" {
			t.Errorf("expected empty OwningConstraint for a null column, got %q", info.OwningConstraint)
		}
	})

	t.Run("propagates a query error", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, gomock.Any()).
			Return(nil, errors.New("boom"))

		deps := &tools.ToolDependencies{DBService: mockDB}
		_, err := fetchIndexByName(context.Background(), deps, "doc_embeddings")
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("errors when the index cannot be found afterward", func(t *testing.T) {
		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().
			ExecuteReadQuery(gomock.Any(), showIndexByNameQuery, gomock.Any()).
			Return([]*neo4j.Record{}, nil)

		deps := &tools.ToolDependencies{DBService: mockDB}
		_, err := fetchIndexByName(context.Background(), deps, "doc_embeddings")
		if err == nil {
			t.Fatal("expected an error for a missing row")
		}
	})
}
