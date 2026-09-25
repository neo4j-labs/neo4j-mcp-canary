// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk/mcpsdktest"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"go.uber.org/mock/gomock"
)

// multiInstanceEnvelope mirrors the subset of the read-cypher response
// envelope (see internal/database/service.go's CypherResponse) this test
// needs — just enough to read back the row a real query returned.
type multiInstanceEnvelope struct {
	Rows []map[string]any `json:"rows"`
}

// freePort finds an available TCP port, mirroring internal/server's own
// getFreePort test helper (unexported there, so duplicated here rather than
// reaching into that package's test files from a different package).
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// TestMultiInstanceHTTP_RealNeo4j proves multi-instance HTTP mode's driver
// construction (cmd/neo4j-mcp/main.go's initMultiInstanceDatabaseService,
// duplicated here since that's unexported in package main) and per-instance
// auth genuinely round-trip against a real Neo4j server — the unit-level
// coverage in internal/server (TestMultiInstanceHTTP_RoutingAndAuth) proves
// routing/auth/dispatch correctness against a mocked database.Service, but
// can't prove the real Bolt driver + credential wiring actually works.
//
// Both instances point at the same shared container (only one is available
// here) with two different auth types: "prod" uses a static service-account
// credential (the container's real user/pass) gated by an API key; "staging"
// forwards the client's own Basic credentials, which the real Neo4j server
// then validates itself. bearer-type instances aren't covered here — Neo4j
// Community Edition (used by this shared container) has no OIDC/SSO support
// to validate against, so that path is covered by internal/oidc's own tests
// instead.
func TestMultiInstanceHTTP_RealNeo4j(t *testing.T) {
	t.Parallel()
	testCFG := dbs.GetDriverConf()

	prodDriver, err := neo4j.NewDriver(testCFG.URI, neo4j.BasicAuth(testCFG.Username, testCFG.Password, ""))
	if err != nil {
		t.Fatalf("failed to create prod driver: %v", err)
	}
	defer func() {
		if err := prodDriver.Close(context.Background()); err != nil {
			t.Errorf("error closing prod driver: %v", err)
		}
	}()
	prodService, err := database.NewNeo4jServiceWithAuthMode(prodDriver, "neo4j", false, "test-version")
	if err != nil {
		t.Fatalf("failed to create prod database service: %v", err)
	}

	var stagingAuthToken neo4j.AuthToken // zero value: no fixed credentials, matching basic_passthrough
	stagingDriver, err := neo4j.NewDriver(testCFG.URI, stagingAuthToken)
	if err != nil {
		t.Fatalf("failed to create staging driver: %v", err)
	}
	defer func() {
		if err := stagingDriver.Close(context.Background()); err != nil {
			t.Errorf("error closing staging driver: %v", err)
		}
	}()
	stagingService, err := database.NewNeo4jServiceWithAuthMode(stagingDriver, "neo4j", true, "test-version")
	if err != nil {
		t.Fatalf("failed to create staging database service: %v", err)
	}

	registry := database.NewInstanceRegistry(map[string]database.Service{
		"prod":    prodService,
		"staging": stagingService,
	})

	port := freePort(t)
	cfg := &config.Config{ // #nosec G101 -- test fixture credentials, not real secrets
		TransportMode:                config.TransportModeHTTP,
		HTTPHost:                     "127.0.0.1",
		HTTPPort:                     strconv.Itoa(port),
		HTTPAPIKeyHeaderName:         "X-Neo4j-MCP-Api-Key",
		HTTPToolsHeaderName:          "X-MCP-Tools",
		HTTPToolCategoriesHeaderName: "X-MCP-Tool-Categories",
		Instances: []config.NeoInstance{
			{
				Name: "prod", URI: testCFG.URI, Database: "neo4j",
				Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, Username: testCFG.Username, Password: testCFG.Password, APIKeys: []string{"prod-key"}},
			},
			{
				Name: "staging", URI: testCFG.URI, Database: "neo4j",
				Auth: config.InstanceAuth{Type: config.InstanceAuthBasicPassthrough},
			},
		},
	}

	ctrl := gomock.NewController(t)
	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(true)
	analyticsService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewConnectionInitializedEvent(gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewToolEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewUnauthenticatedJSONRPCEvent(gomock.Any()).AnyTimes()

	s := server.NewNeo4jMCPServer("test-version", cfg, registry, analyticsService)
	if s == nil {
		t.Fatal("NewNeo4jMCPServer() returned nil")
	}

	errChan := make(chan error, 1)
	go func() {
		if err := s.Start(); err != nil {
			errChan <- err
		}
	}()
	for range s.HTTPServerReady { //nolint:all // waiting for the channel to close
	}
	time.Sleep(100 * time.Millisecond)

	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Stop(stopCtx); err != nil {
			t.Errorf("Stop() unexpected error: %v", err)
		}
		select {
		case err := <-errChan:
			t.Errorf("Start() unexpected error: %v", err)
		default:
		}
	}()

	prodURI := fmt.Sprintf("http://%s:%s/prod/mcp", cfg.HTTPHost, cfg.HTTPPort)
	stagingURI := fmt.Sprintf("http://%s:%s/staging/mcp", cfg.HTTPHost, cfg.HTTPPort)

	callReadCypher := func(t *testing.T, client *mcpsdktest.Client, query string) (*mcpsdktest.CallToolResult, error) {
		t.Helper()
		return client.CallTool(context.Background(), "read-cypher", map[string]any{"query": query})
	}

	parseRows := func(t *testing.T, res *mcpsdktest.CallToolResult) []map[string]any {
		t.Helper()
		text, ok := mcpsdktest.AsTextContent(res.Content[0])
		if !ok {
			t.Fatalf("expected TextContent, got %T", res.Content[0])
		}
		var env multiInstanceEnvelope
		if err := json.Unmarshal([]byte(text.Text), &env); err != nil {
			t.Fatalf("failed to parse response: %v\nraw: %s", err, text.Text)
		}
		return env.Rows
	}

	t.Run("prod route reaches real Neo4j via the static service-account credential", func(t *testing.T) {
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", prodURI, mcpsdktest.WithHTTPHeader("X-Neo4j-MCP-Api-Key", "prod-key"))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		res, err := callReadCypher(t, client, "RETURN 'prod' AS instance")
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool returned error: %+v", res)
		}
		rows := parseRows(t, res)
		if len(rows) != 1 || rows[0]["instance"] != "prod" {
			t.Errorf("rows = %+v, want a single row with instance=prod", rows)
		}
	})

	t.Run("staging route forwards the client's real Basic credentials to Neo4j", func(t *testing.T) {
		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testCFG.Username+":"+testCFG.Password))
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", stagingURI, mcpsdktest.WithHTTPHeader("Authorization", basicAuth))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		res, err := callReadCypher(t, client, "RETURN 'staging' AS instance")
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool returned error: %+v", res)
		}
		rows := parseRows(t, res)
		if len(rows) != 1 || rows[0]["instance"] != "staging" {
			t.Errorf("rows = %+v, want a single row with instance=staging", rows)
		}
	})

	t.Run("wrong Basic credentials on the staging route are rejected by real Neo4j", func(t *testing.T) {
		basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testCFG.Username+":wrong-password"))
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", stagingURI, mcpsdktest.WithHTTPHeader("Authorization", basicAuth))
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize: %v", err)
		}

		// A rejected query surfaces as a tool-level error (IsError=true inside
		// a normal RPC response), not as a transport-level Go error from
		// CallTool — the MCP protocol's standard shape for a failed tool call.
		res, err := callReadCypher(t, client, "RETURN 1")
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Error("expected the tool call to report an error with wrong Neo4j credentials, got a successful result")
		}
	})

	t.Run("missing API key on the prod route is rejected before reaching Neo4j", func(t *testing.T) {
		client := mcpsdktest.NewHTTPClient("test-client", "1.0.0", prodURI)
		defer client.Close()
		if _, err := client.Initialize(context.Background()); err == nil {
			t.Fatal("expected initialize to fail without an API key")
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
