// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server_test

import (
	"fmt"
	"testing"

	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"

	"go.uber.org/mock/gomock"
)

func TestToolRegister(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	aService := analytics.NewMockService(ctrl)
	aService.EXPECT().IsEnabled().AnyTimes().Return(true)
	aService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	aService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	aService.EXPECT().NewConnectionInitializedEvent(gomock.Any()).AnyTimes()

	t.Run("verifies expected tools are registered", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// update this number when a tool is added or removed.
		// Current tools: get-schema, read-cypher, write-cypher, list-gds-procedures
		expectedTotalToolsCount := 4

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register only readonly tools when readonly", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			ReadOnly:      true,
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// update this number when a tool is added or removed.
		// Readonly tools: get-schema, read-cypher, list-gds-procedures
		expectedTotalToolsCount := 3

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register also write tools when readonly is set to false", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			ReadOnly:      false,
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// update this number when a tool is added or removed.
		// All tools: get-schema, read-cypher, write-cypher, list-gds-procedures
		expectedTotalToolsCount := 4

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should remove GDS tools if GDS is not present", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, false)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			ReadOnly:      false,
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// update this number when a tool is added or removed.
		// Non-GDS tools: get-schema, read-cypher, write-cypher
		expectedTotalToolsCount := 3

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register vector-search tool when vector indexes are found", func(t *testing.T) {
		mockDB := getMockedDBServiceFull(ctrl, true, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// All tools + vector: get-schema, read-cypher, write-cypher, list-gds-procedures, vector-search
		expectedTotalToolsCount := 5

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should not register vector-search tool when no vector indexes are found", func(t *testing.T) {
		mockDB := getMockedDBServiceFull(ctrl, true, false)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// All tools minus vector: get-schema, read-cypher, write-cypher, list-gds-procedures
		expectedTotalToolsCount := 4

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register vector-search tool in readonly mode when vector indexes exist", func(t *testing.T) {
		mockDB := getMockedDBServiceFull(ctrl, true, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(1)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			ReadOnly:      true,
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		// Expected tools that should be registered
		// Readonly + vector: get-schema, read-cypher, list-gds-procedures, vector-search
		expectedTotalToolsCount := 4

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.MCPServer.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})
}

// getMockedDBService returns a mock DB service with the standard verifyRequirements expectations set up.
func getMockedDBService(ctrl *gomock.Controller, withGDS bool) *db.MockService {
	return getMockedDBServiceFull(ctrl, withGDS, false)
}

// getMockedDBServiceFull returns a mock DB service with full control over GDS and vector index availability.
func getMockedDBServiceFull(ctrl *gomock.Controller, withGDS bool, withVectorIndexes bool) *db.MockService {
	mockDB := db.NewMockService(ctrl)
	mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
	mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), checkApocMetaSchemaQuery, gomock.Any()).Times(1).Return(apocAvailableRecord(true), nil)

	if withGDS {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).Times(1).Return(gdsVersionRecord("2.22.0"), nil)
	} else {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).Times(1).Return(nil, fmt.Errorf("Unknown function 'gds.version'"))
	}

	if withVectorIndexes {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), vectorIndexCountQuery, gomock.Any()).Times(1).Return(vectorIndexCountRecord(2), nil)
	} else {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), vectorIndexCountQuery, gomock.Any()).Times(1).Return(vectorIndexCountRecord(0), nil)
	}

	return mockDB
}
