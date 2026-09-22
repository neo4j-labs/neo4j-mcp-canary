// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk/mcpsdktest"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// TestHTTPPerRequestToolSelection verifies that the X-MCP-Tools and
// X-MCP-Tool-Categories headers narrow the tool set for a single HTTP
// request, without affecting other requests, and that a tool excluded by
// either header is rejected as "unknown" rather than executed.
func TestHTTPPerRequestToolSelection(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	cfg := &config.Config{
		URI:                          "bolt://test-host:7687",
		Database:                     "neo4j",
		TransportMode:                config.TransportModeHTTP,
		HTTPHost:                     "127.0.0.1",
		HTTPPort:                     strconv.Itoa(port),
		HTTPToolsHeaderName:          "X-MCP-Tools",
		HTTPToolCategoriesHeaderName: "X-MCP-Tool-Categories",
	}
	uri := fmt.Sprintf("http://%s:%s/mcp", cfg.HTTPHost, cfg.HTTPPort)

	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(true)
	analyticsService.EXPECT().NewConnectionInitializedEvent(gomock.Any()).AnyTimes()

	mockDB := db.NewMockService(ctrl)
	mockDB.EXPECT().VerifyConnectivity(gomock.Any()).AnyTimes()
	mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).AnyTimes().Return(gdsVersionRecord("2.22.0"), nil)
	mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).AnyTimes()

	s, errChan := createHTTPServer(t, cfg, mockDB, analyticsService)
	defer assertNoCloseOrStopError(t, s, errChan)

	authedClient := func(extraHeaders ...mcpsdktest.HTTPClientOption) *mcpsdktest.Client {
		opts := append([]mcpsdktest.HTTPClientOption{
			mcpsdktest.WithHTTPHeader("Authorization", "Basic bmVvNGo6cGFzc3dvcmQ="),
		}, extraHeaders...)
		return mcpsdktest.NewHTTPClient("test-client", "1.0.0", uri, opts...)
	}

	t.Run("no selection headers returns every registered tool", func(t *testing.T) {
		client := authedClient()
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		res, err := client.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		assert.Len(t, res.Tools, 12)
	})

	t.Run("X-MCP-Tools narrows tools/list and rejects excluded tools/call", func(t *testing.T) {
		client := authedClient(mcpsdktest.WithHTTPHeader("X-MCP-Tools", "read-cypher"))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		res, err := client.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if assert.Len(t, res.Tools, 1) {
			assert.Equal(t, "read-cypher", res.Tools[0].Name)
		}

		_, err = client.CallTool(context.Background(), "get-schema", map[string]any{})
		assert.Error(t, err)
	})

	t.Run("X-MCP-Tool-Categories narrows to a single category", func(t *testing.T) {
		client := authedClient(mcpsdktest.WithHTTPHeader("X-MCP-Tool-Categories", "gds"))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}
		res, err := client.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if assert.Len(t, res.Tools, 1) {
			assert.Equal(t, "list-gds-procedures", res.Tools[0].Name)
		}
	})
}
