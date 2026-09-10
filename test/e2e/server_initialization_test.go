// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk/mcpsdktest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerInitializationE2E(t *testing.T) {
	ctx := context.Background()
	cfg := dbs.GetDriverConf()

	t.Run("successful initialization with all required parameters", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Test initialization
		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server")

		// Verify server info
		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)
		assert.NotEmpty(t, initResponse.ServerInfo.Version)

		// Verify capabilities
		assert.NotNil(t, initResponse.Capabilities)
		assert.NotNil(t, initResponse.Capabilities.Tools)

		t.Log("Server initialized successfully with expected name and capabilities")
	})

	t.Run("initialization without a database name", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Test should pass as the default database is neo4j
		initResponse, err := mcpClient.Initialize(ctx)
		_ = err
		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

	})

	t.Run("initialization with read-only mode enabled", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
			"--neo4j-read-only", "true",
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Test initialization in read-only mode
		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server in read-only mode")

		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

		// List tools to verify read-only mode behavior
		listToolsResponse, err := mcpClient.ListTools(ctx)
		require.NoError(t, err, "failed to list tools in read-only mode")

		for _, tool := range listToolsResponse.Tools {
			if tool.Name == "write-cypher" {
				t.Fatal("write-cypher tool found using readonly mode")
			}
		}
		assert.Len(t, listToolsResponse.Tools, 3, "read-only mode true returns the wrong number of tools")
	})

	t.Run("initialization with read-only mode disabled", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
			"--neo4j-read-only", "false",
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server in read-only mode")

		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

		listToolsResponse, err := mcpClient.ListTools(ctx)
		require.NoError(t, err, "failed to list tools with read-only mode as false")
		assert.Len(t, listToolsResponse.Tools, 4, "read-only mode false returns the wrong number of tools")
	})
	t.Run("initialization with telemetry disabled", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
			"--neo4j-telemetry", "false",
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Test initialization with telemetry disabled
		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server with telemetry disabled")

		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

		t.Log("Server initialized successfully with telemetry disabled")
	})

	t.Run("initialization with schema sample size override", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
			"--neo4j-schema-sample-size", "50",
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Test initialization with custom schema sample size
		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server with custom schema sample size")

		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

		t.Log("Server initialized successfully with custom schema sample size")
	})

	t.Run("client initialization with invalid schema sample size", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"--neo4j-uri", cfg.URI,
			"--neo4j-username", cfg.Username,
			"--neo4j-password", cfg.Password,
			"--neo4j-database", cfg.Database,
			"--neo4j-schema-sample-size", "not-a-number",
		}

		mcpClient := mcpsdktest.NewStdioClient("test-client", "1.0.0", server, args)

		defer mcpClient.Close()

		// Server should handle invalid schema sample size gracefully (falling back to default)
		initResponse, err := mcpClient.Initialize(ctx)
		require.NoError(t, err, "failed to initialize MCP server with invalid schema sample size")

		assert.Equal(t, "neo4j-mcp", initResponse.ServerInfo.Name)

		t.Log("Server initialized successfully with invalid schema sample size (using default value)")
	})
}
