// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/integration/helpers"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func TestServerLifecycle(t *testing.T) {
	t.Parallel()
	testCFG := dbs.GetDriverConf()
	testCases := []struct {
		name        string
		config      *config.Config
		expectError bool
	}{
		{
			name: "Neo4jMCPServer should correctly start",
			config: &config.Config{
				URI:           testCFG.URI,
				Username:      testCFG.Username,
				Password:      testCFG.Password,
				Database:      testCFG.Database,
				TransportMode: config.TransportModeStdio,
			},
			expectError: false,
		},
		{
			name: "Neo4jMCPServer should fail to start: invalid host",
			config: &config.Config{
				URI:           "bolt://not-a-valid-host:7687",
				Username:      testCFG.Username,
				Password:      testCFG.Password,
				Database:      testCFG.Database,
				TransportMode: config.TransportModeStdio,
			},
			expectError: true,
		},
		{
			name: "Neo4jMCPServer should fail to start: invalid database name",
			config: &config.Config{
				URI:           testCFG.URI,
				Username:      testCFG.Username,
				Password:      testCFG.Password,
				Database:      "not-a-valid-db-name",
				TransportMode: config.TransportModeStdio,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			driver, err := neo4j.NewDriver(tc.config.URI, neo4j.BasicAuth(tc.config.Username, tc.config.Password, ""))
			if err != nil {
				t.Fatalf("failed to create Neo4j driver: %s", err.Error())
			}
			testContext := helpers.NewTestContext(t, &driver)

			ctx := context.Background()
			defer func() {
				if err := driver.Close(ctx); err != nil {
					t.Fatalf("error closing driver: %s", err.Error())
				}
			}()

			dbService, err := database.NewNeo4jService(driver, tc.config.Database, tc.config.TransportMode, "test-version")
			if err != nil {
				t.Fatalf("failed to create database service: %v", err)
				return
			}

			s := server.NewNeo4jMCPServer("test-version", tc.config, dbService, testContext.AnalyticsService)

			if s == nil {
				t.Fatal("the NewNeo4jMCPServer() returned nil")
			}

			startErrCh := make(chan error, 1)
			go func() {
				startErrCh <- s.Start()
			}()

			// Start() blocks on verifyRequirements before ever reaching the stdio
			// serve loop, so an error case resolves as soon as the driver gives up
			// on the bad host/database — but that failure only surfaces once the
			// driver's own connection/DNS-resolution timeout elapses, which can
			// take noticeably longer in a CI network sandbox than on a developer
			// machine. The happy-path case never returns on its own (it blocks
			// serving stdio), so its window only needs to be long enough to rule
			// out an immediate, unexpected failure. Waiting on startErrCh (rather
			// than polling on a fixed wall-clock deadline) also means the error
			// cases resolve as soon as Start() actually returns, instead of
			// always waiting out the full window.
			wait := 4 * time.Second
			if tc.expectError {
				wait = 30 * time.Second
			}

			select {
			case startErr := <-startErrCh:
				if tc.expectError && startErr == nil {
					t.Fatal("expected an error but got nil")
				}
				if !tc.expectError && startErr != nil {
					t.Fatalf("Start returned an unexpected error: %s", startErr.Error())
				}
			case <-time.After(wait):
				if tc.expectError {
					t.Fatalf("expected Start() to fail within %s, but it did not return", wait)
				}
				// Happy path: Start() is still blocking on the stdio serve loop, as expected.
			}
		})
	}

	t.Run("server stop should return no errors", func(t *testing.T) {
		driver, err := neo4j.NewDriverWithContext(testCFG.URI, neo4j.BasicAuth(testCFG.Username, testCFG.Password, ""))
		if err != nil {
			t.Fatalf("failed to create Neo4j driver: %s", err.Error())
		}
		testContext := helpers.NewTestContext(t, &driver)
		ctx := context.Background()
		defer func() {
			if err := driver.Close(ctx); err != nil {
				t.Fatalf("error closing driver: %s", err.Error())
			}
		}()

		dbService, err := database.NewNeo4jService(driver, testCFG.Database, testCFG.TransportMode, "test-version")
		if err != nil {
			t.Fatalf("failed to create database service: %v", err)
		}

		testCFGWithTransport := &config.Config{
			URI:           testCFG.URI,
			Username:      testCFG.Username,
			Password:      testCFG.Password,
			Database:      testCFG.Database,
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", testCFGWithTransport, dbService, testContext.AnalyticsService)
		if s == nil {
			t.Fatal("NewNeo4jMCPServer() returned nil")
		}

		var wg sync.WaitGroup
		wg.Add(1)

		var startErr error
		go func() {
			defer wg.Done()
			startErr = s.Start()
		}()

		// Give the server a moment to start
		time.Sleep(4 * time.Second)

		if startErr != nil {
			t.Fatalf("Start() returned an unexpected error after stop: %v", startErr)
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() returned an unexpected error: %v", err)
		}
	})
}
