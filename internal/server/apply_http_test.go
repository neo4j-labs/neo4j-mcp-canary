// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"

	"go.uber.org/mock/gomock"
)

// TestApply_TierHTTPBounce_PortChangeTakesEffect verifies the real,
// end-to-end mechanics of a Tier 2 Apply: the server starts on one port,
// Apply is called with a different NEO4J_MCP_HTTP_PORT, and afterward the
// old port is unreachable while the new one serves the MCP endpoint.
func TestApply_TierHTTPBounce_PortChangeTakesEffect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	oldPort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	newPort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}

	cfg := &config.Config{
		URI:           "bolt://test-host:7687",
		Database:      "neo4j",
		TransportMode: config.TransportModeHTTP,
		HTTPHost:      "127.0.0.1",
		HTTPPort:      strconv.Itoa(oldPort),
	}

	analyticsService := analytics.NewMockService(ctrl)
	analyticsService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	analyticsService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	analyticsService.EXPECT().IsEnabled().AnyTimes().Return(false)

	mockDB := db.NewMockService(ctrl)

	s, errChan := createHTTPServer(t, cfg, mockDB, analyticsService)
	defer assertNoCloseOrStopError(t, s, errChan)

	oldURL := "http://127.0.0.1:" + strconv.Itoa(oldPort) + "/mcp"
	if err := postAndClose(oldURL); err != nil {
		t.Fatalf("expected the old port to be reachable before Apply, got: %v", err)
	}

	newCfg := *cfg
	newCfg.HTTPPort = strconv.Itoa(newPort)

	result, err := s.Apply(context.Background(), &newCfg)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Bounced {
		t.Error("ApplyResult.Bounced = false, want true for an HTTPPort change")
	}

	// The old listener should be gone.
	if err := postAndClose(oldURL); err == nil {
		t.Error("expected the old port to be unreachable after Apply bounced the HTTP listener")
	}

	// The new listener should be up. Poll briefly since the bounce completes
	// asynchronously from Apply's perspective of "the goroutine started".
	newURL := "http://127.0.0.1:" + strconv.Itoa(newPort) + "/mcp"
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = postAndClose(newURL)
		if lastErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Errorf("expected the new port to be reachable after Apply, got: %v", lastErr)
	}
}

// postAndClose issues an empty POST to url and closes the response body,
// satisfying the bodyclose linter — the response content itself is never
// needed here, only whether the request succeeded at all.
func postAndClose(url string) error {
	resp, err := http.Post(url, "application/json", nil) //nolint:gosec // localhost test server on a dynamically-chosen free port, not user input
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
