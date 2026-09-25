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

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// qualifyingSearchVersionRecord is a CALL dbms.components() result reporting
// a "Neo4j Kernel" version that clears tools.CategorySearch's own version
// floor (calendar >= 2026.09.0) — used by the count-based subtests below so
// the search tools register and are included in the expected totals.
func qualifyingSearchVersionRecord() []*neo4j.Record {
	return []*neo4j.Record{
		{
			Keys:   []string{"name", "edition", "versions"},
			Values: []any{"Neo4j Kernel", "enterprise", []any{"2026.09.0"}},
		},
	}
}

func TestToolRegister(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	aService := analytics.NewMockService(ctrl)
	aService.EXPECT().IsEnabled().AnyTimes().Return(true)
	aService.EXPECT().EmitEvent(gomock.Any()).AnyTimes()
	aService.EXPECT().NewStartupEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	aService.EXPECT().NewConnectionInitializedEvent(gomock.Any()).AnyTimes()

	t.Run("verifies expected tools are registered", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2).Return(qualifyingSearchVersionRecord(), nil)
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
		// Current tools: get-schema, read-cypher, write-cypher, explain-cypher,
		// profile-cypher, list-constraints-and-indexes, create-constraint,
		// drop-constraint, create-index, drop-index, list-gds-procedures,
		// give-feedback, vector-search, fulltext-search, create-vector-index,
		// create-fulltext-index, set-vector-property, check-embedding-dimensions
		expectedTotalToolsCount := 18

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register only readonly tools when readonly", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2).Return(qualifyingSearchVersionRecord(), nil)
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
		// Readonly tools: get-schema, read-cypher, explain-cypher,
		// list-constraints-and-indexes, list-gds-procedures, give-feedback,
		// vector-search, fulltext-search, check-embedding-dimensions
		expectedTotalToolsCount := 9

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should register also write tools when readonly is set to false", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2).Return(qualifyingSearchVersionRecord(), nil)
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
		// All tools: get-schema, read-cypher, write-cypher, explain-cypher,
		// profile-cypher, list-constraints-and-indexes, create-constraint,
		// drop-constraint, create-index, drop-index, list-gds-procedures,
		// give-feedback, vector-search, fulltext-search, create-vector-index,
		// create-fulltext-index, set-vector-property, check-embedding-dimensions
		expectedTotalToolsCount := 18

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should remove GDS tools if GDS is not present", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, false)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2).Return(qualifyingSearchVersionRecord(), nil)
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
		// Non-GDS tools: get-schema, read-cypher, write-cypher, explain-cypher,
		// profile-cypher, list-constraints-and-indexes, create-constraint,
		// drop-constraint, create-index, drop-index, give-feedback,
		// vector-search, fulltext-search, create-vector-index,
		// create-fulltext-index, set-vector-property, check-embedding-dimensions
		expectedTotalToolsCount := 17

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}
		registeredTools := len(s.ListTools())

		if expectedTotalToolsCount != registeredTools {
			t.Errorf("Expected %d tools, but test configuration shows %d", expectedTotalToolsCount, registeredTools)
		}
	})

	t.Run("should remove search tools if the server is below the search version floor", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		// 2026.07.0 clears the Query API's own floor but not the search
		// category's higher one (>= 2026.09.0) — proving the two gates are
		// independent, not just that "some" floor was applied.
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2).Return([]*neo4j.Record{
			{Keys: []string{"name", "edition", "versions"}, Values: []any{"Neo4j Kernel", "enterprise", []any{"2026.07.0"}}},
		}, nil)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		toolNames := make([]string, 0, len(s.ListTools()))
		for _, tool := range s.ListTools() {
			toolNames = append(toolNames, tool.Name)
		}
		for _, searchTool := range []string{"vector-search", "fulltext-search", "create-vector-index", "create-fulltext-index", "set-vector-property", "check-embedding-dimensions"} {
			assert.NotContains(t, toolNames, searchTool)
		}
		// Baseline count from before the search category existed — proves
		// nothing else regressed, only the search tools were excluded.
		assert.Len(t, toolNames, 12)
	})

	t.Run("should narrow registered tools by name via EnabledTools", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2)
		cfg := &config.Config{
			URI:           "bolt://test-host:7687",
			Username:      "neo4j",
			Password:      "password",
			Database:      "neo4j",
			EnabledTools:  "read-cypher, get-schema",
			TransportMode: config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		toolNames := make([]string, 0, len(s.ListTools()))
		for _, tool := range s.ListTools() {
			toolNames = append(toolNames, tool.Name)
		}
		assert.ElementsMatch(t, []string{"read-cypher", "get-schema"}, toolNames)
	})

	t.Run("should narrow registered tools by category via EnabledToolCategories", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2)
		cfg := &config.Config{
			URI:                   "bolt://test-host:7687",
			Username:              "neo4j",
			Password:              "password",
			Database:              "neo4j",
			EnabledToolCategories: "gds",
			TransportMode:         config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		toolNames := make([]string, 0, len(s.ListTools()))
		for _, tool := range s.ListTools() {
			toolNames = append(toolNames, tool.Name)
		}
		assert.ElementsMatch(t, []string{"list-gds-procedures"}, toolNames)
	})

	t.Run("EnabledTools and EnabledToolCategories combine as a union", func(t *testing.T) {
		mockDB := getMockedDBService(ctrl, true)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), "CALL dbms.components()", gomock.Any()).Times(2)
		cfg := &config.Config{
			URI:                   "bolt://test-host:7687",
			Username:              "neo4j",
			Password:              "password",
			Database:              "neo4j",
			EnabledTools:          "give-feedback",
			EnabledToolCategories: "gds",
			TransportMode:         config.TransportModeStdio,
		}
		s := server.NewNeo4jMCPServer("test-version", cfg, mockDB, aService)

		err := s.Start()
		if err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		toolNames := make([]string, 0, len(s.ListTools()))
		for _, tool := range s.ListTools() {
			toolNames = append(toolNames, tool.Name)
		}
		assert.ElementsMatch(t, []string{"give-feedback", "list-gds-procedures"}, toolNames)
	})
}

// getMockedDBService returns a mock DB service with the standard verifyRequirements expectations set up.
func getMockedDBService(ctrl *gomock.Controller, withGDS bool) *db.MockService {
	mockDB := db.NewMockService(ctrl)
	mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)

	if withGDS {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).Times(1).Return(gdsVersionRecord("2.22.0"), nil)
	} else {
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).Times(1).Return(nil, fmt.Errorf("Unknown function 'gds.version'"))
	}

	return mockDB
}
