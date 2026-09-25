// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk/mcpsdktest"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// TestMultiInstanceHTTP_RoutingAndAuth exercises the full multi-instance
// HTTP stack end to end — a real net/http.Server, a real mcpsdk.Server, and
// a real MCP client (mcpsdktest) — proving that a request to "/<name>/mcp"
// reaches the right instance's database.Service (via
// database.InstanceRegistry) and that each instance's own auth.Type is
// enforced independently of the others. The underlying Neo4j connections
// are mocked, matching the existing pattern in tool_access_http_test.go:
// real Bolt/JWKS wiring is covered separately by internal/database's and
// internal/oidc's own tests, and by the integration-tagged multi-instance
// test against a real container.
func TestMultiInstanceHTTP_RoutingAndAuth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}

	prodDB := db.NewMockService(ctrl)
	prodDB.EXPECT().VerifyConnectivity(gomock.Any()).AnyTimes()
	prodDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).AnyTimes().Return(gdsVersionRecord("2.22.0"), nil)
	prodDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).AnyTimes().Return(qualifyingSearchVersionRecord(), nil)

	stagingDB := db.NewMockService(ctrl)
	stagingDB.EXPECT().VerifyConnectivity(gomock.Any()).AnyTimes()
	stagingDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).AnyTimes().Return(gdsVersionRecord("2.22.0"), nil)
	stagingDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).AnyTimes().Return(qualifyingSearchVersionRecord(), nil)

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod":    prodDB,
		"staging": stagingDB,
	})

	cfg := &config.Config{
		TransportMode:                config.TransportModeHTTP,
		HTTPHost:                     "127.0.0.1",
		HTTPPort:                     strconv.Itoa(port),
		HTTPAPIKeyHeaderName:         "X-Neo4j-MCP-Api-Key",
		HTTPToolsHeaderName:          "X-MCP-Tools",
		HTTPToolCategoriesHeaderName: "X-MCP-Tool-Categories",
		Instances: []config.NeoInstance{
			{
				Name: "prod", URI: "neo4j+s://prod.example:7687", Database: "neo4j",
				Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, Username: "svc", Password: "s3cret", APIKeys: []string{"prod-key"}},
			},
			{
				Name: "staging", URI: "neo4j://staging.example:7687", Database: "neo4j",
				Auth: config.InstanceAuth{Type: config.InstanceAuthBasicPassthrough},
			},
		},
	}

	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(true)
	analyticsService.EXPECT().NewConnectionInitializedEvent(gomock.Any()).AnyTimes()
	// OnToolCallComplete (internal/eventing/tool_calls.go) fires this after
	// every successful tools/call — unlike tool_access_http_test.go's calls,
	// which are all rejected before reaching a real tool handler, this test
	// actually completes real CallTool round trips.
	analyticsService.EXPECT().NewToolEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

	s, errChan := createHTTPServer(t, cfg, registry, analyticsService)
	defer assertNoCloseOrStopError(t, s, errChan)

	prodURI := fmt.Sprintf("http://%s:%s/prod/mcp", cfg.HTTPHost, cfg.HTTPPort)
	stagingURI := fmt.Sprintf("http://%s:%s/staging/mcp", cfg.HTTPHost, cfg.HTTPPort)

	t.Run("prod route dispatches to the prod instance only", func(t *testing.T) {
		prodDB.EXPECT().GetQueryType(gomock.Any(), gomock.Any(), gomock.Any()).Return(neo4j.QueryTypeReadOnly, nil)
		prodDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&database.QueryResult{Records: []*neo4j.Record{{Keys: []string{"instance"}, Values: []any{"prod"}}}, RowCount: 1}, nil)

		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", prodURI, mcpsdktest.WithHTTPHeader("X-Neo4j-MCP-Api-Key", "prod-key"))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		res, err := client.CallTool(context.Background(), "read-cypher", map[string]any{"query": "RETURN 1 AS instance"})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool returned error: %+v", res)
		}
	})

	t.Run("staging route dispatches to the staging instance only", func(t *testing.T) {
		stagingDB.EXPECT().GetQueryType(gomock.Any(), gomock.Any(), gomock.Any()).Return(neo4j.QueryTypeReadOnly, nil)
		stagingDB.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&database.QueryResult{Records: []*neo4j.Record{{Keys: []string{"instance"}, Values: []any{"staging"}}}, RowCount: 1}, nil)

		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("someuser:somepass"))
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", stagingURI, mcpsdktest.WithHTTPHeader("Authorization", basicAuth))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		res, err := client.CallTool(context.Background(), "read-cypher", map[string]any{"query": "RETURN 1 AS instance"})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool returned error: %+v", res)
		}
	})

	t.Run("missing API key on the prod route is rejected", func(t *testing.T) {
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", prodURI)
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err == nil {
			t.Fatal("expected initialize to fail without an API key")
		}
	})

	t.Run("staging's lack of an API key requirement doesn't help against prod", func(t *testing.T) {
		// A key that isn't configured for ANY instance must not be treated as
		// implicitly valid just because staging doesn't use API keys at all.
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", prodURI, mcpsdktest.WithHTTPHeader("X-Neo4j-MCP-Api-Key", "not-a-real-key"))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err == nil {
			t.Fatal("expected initialize to fail with an unrecognized API key")
		}
	})

	t.Run("missing Basic credentials on the staging route is rejected", func(t *testing.T) {
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", stagingURI)
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err == nil {
			t.Fatal("expected initialize to fail without Basic credentials")
		}
	})

	t.Run("an unconfigured instance path is rejected", func(t *testing.T) {
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", fmt.Sprintf("http://%s:%s/does-not-exist/mcp", cfg.HTTPHost, cfg.HTTPPort))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err == nil {
			t.Fatal("expected initialize to fail for an unconfigured instance path")
		}
	})
}
