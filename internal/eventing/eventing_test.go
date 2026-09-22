// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package eventing_test

import (
	"context"
	"errors"
	"testing"

	analyticsReal "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/eventing"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

func TestEmitter_EmitServerStartup(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantMode   string
		tlsEnabled bool
	}{
		{name: "bolt URI reports bolt mode", uri: "bolt://test-host:7687", wantMode: "bolt"},
		{name: "http URI reports query_api mode", uri: "https://test-host:7473", wantMode: "query_api", tlsEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cfg := &config.Config{URI: tt.uri, TransportMode: config.TransportModeStdio, HTTPTLSEnabled: tt.tlsEnabled}
			analyticsService := analytics.NewMockService(ctrl)
			analyticsService.EXPECT().NewStartupEvent(config.TransportModeStdio, tt.tlsEnabled, "test-version", tt.wantMode).Times(1)
			analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

			eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version").EmitServerStartup()
		})
	}
}

func TestEmitter_EmitConnectionInitialized(t *testing.T) {
	cfg := &config.Config{URI: "bolt://test-host:7687"}

	t.Run("skips entirely when analytics is disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(false)
		// No ExecuteReadQuery/EmitEvent expectations: a call would fail the
		// mock controller since none were recorded.

		eventing.NewEmitter(analyticsService, mockDB, cfg, "test-version").EmitConnectionInitialized(context.Background())
	})

	t.Run("skips emitting when the dbms.components query fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)

		eventing.NewEmitter(analyticsService, mockDB, cfg, "test-version").EmitConnectionInitialized(context.Background())
	})

	t.Run("emits connection info parsed from dbms.components", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).
			Times(1).Return([]*neo4j.Record{
			{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", "enterprise", []any{"5.18.0"}}},
			{Keys: []string{"name", "edition", "versions"}, Values: []any{"Cypher", "enterprise", []any{"5"}}},
		}, nil)

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewConnectionInitializedEvent(analyticsReal.ConnectionEventInfo{
			Neo4jVersion:  "5.18.0",
			Edition:       "enterprise",
			CypherVersion: []string{"5"},
		}).Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		eventing.NewEmitter(analyticsService, mockDB, cfg, "test-version").EmitConnectionInitialized(context.Background())
	})
}
