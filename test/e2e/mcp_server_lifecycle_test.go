// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk/mcpsdktest"
	"github.com/neo4j-labs/neo4j-mcp-canary/test/e2e/helpers"
)

func TestSeverLifecycleMCPE2E(t *testing.T) {
	t.Parallel()

	t.Run("lifecycle test (MCPServer -> MCP Client -> Initialize Req -> List Tools -> Call Tool -> Stop)", func(t *testing.T) {
		// Create MCP client
		ctx := context.Background()

		cfg := dbs.GetDriverConf()
		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)
		helpers.NewE2ETestContext(t, dbs.GetDriver())

		// Test server initialization
		initializeResponse, err := mcpClient.Initialize(ctx)
		if err != nil {
			t.Fatalf("failed to initialize MCP server: %v", err)
		}

		expectedServerInfoName := "neo4j-mcp"
		if initializeResponse.ServerInfo.Name != expectedServerInfoName {
			t.Fatalf("expected server name returned from initialize request to be: %s, but found: %s", expectedServerInfoName, initializeResponse.ServerInfo.Name)
		}

		// Test basic functionality - list tools
		listToolsResponse, err := mcpClient.ListTools(ctx)
		if err != nil {
			t.Fatalf("failed to list tools: %v", err)
		}

		// Verify we have the expected tools
		if len(listToolsResponse.Tools) == 0 {
			t.Fatal("expected tools to be available, but got none")
		}

		// Test calling a tool, get-schema for simplicity.
		callToolResponse, err := mcpClient.CallTool(ctx, "get-schema", nil)
		if err != nil {
			t.Fatalf("failed to call get-schema tool: %v", err)
		}

		// Verify the tool call was successful
		if callToolResponse.IsError {
			textContent, ok := mcpsdktest.AsTextContent(callToolResponse.Content[0])
			if !ok {
				t.Fatalf("expected error as TextContent, got %T", callToolResponse.Content[0])
			}
			t.Fatalf("get-schema tool call returned an error: %s", textContent.Text)
		}

		if len(callToolResponse.Content) == 0 {
			t.Fatal("expected get-schema tool to return content, but got none")
		}
		defer mcpClient.Close()
		t.Logf("Server started successfully with %d tools available", len(listToolsResponse.Tools))
		t.Logf("Successfully called get-schema tool and received %d content items", len(callToolResponse.Content))

	})
}
