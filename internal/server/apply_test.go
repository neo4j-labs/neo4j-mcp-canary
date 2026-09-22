// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"slices"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

func baseConfig() *config.Config {
	return &config.Config{
		URI:                    "bolt://localhost:7687",
		Database:               "neo4j",
		TransportMode:          config.TransportModeHTTP,
		HTTPHost:               "127.0.0.1",
		HTTPPort:               "80",
		HTTPAllowedOrigins:     "",
		AuthHeaderName:         "Authorization",
		OutputFormat:           config.OutputFormatJSON,
		CypherMaxRows:          1000,
		CypherMaxBytes:         900_000,
		CypherTimeoutSeconds:   30,
		CypherMaxEstimatedRows: 1_000_000,
	}
}

func TestDiffTiers(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.Config)
		want   []ApplyTier
	}{
		{
			name:   "no change",
			modify: func(_ *config.Config) {},
			want:   nil,
		},
		{
			name:   "ReadOnly toggled",
			modify: func(c *config.Config) { c.ReadOnly = true },
			want:   []ApplyTier{ApplyTierInstant},
		},
		{
			name:   "OutputFormat changed",
			modify: func(c *config.Config) { c.OutputFormat = config.OutputFormatTOON },
			want:   []ApplyTier{ApplyTierInstant},
		},
		{
			name:   "EnabledTools changed",
			modify: func(c *config.Config) { c.EnabledTools = "read-cypher" },
			want:   []ApplyTier{ApplyTierInstant},
		},
		{
			name:   "HTTPPort changed",
			modify: func(c *config.Config) { c.HTTPPort = "9090" },
			want:   []ApplyTier{ApplyTierHTTPBounce},
		},
		{
			name:   "HTTPTLSEnabled changed",
			modify: func(c *config.Config) { c.HTTPTLSEnabled = true },
			want:   []ApplyTier{ApplyTierHTTPBounce},
		},
		{
			name:   "URI changed",
			modify: func(c *config.Config) { c.URI = "bolt://other-host:7687" },
			want:   []ApplyTier{ApplyTierDBRebuild},
		},
		{
			name:   "Database changed",
			modify: func(c *config.Config) { c.Database = "otherdb" },
			want:   []ApplyTier{ApplyTierDBRebuild},
		},
		{
			name: "multiple tiers at once",
			modify: func(c *config.Config) {
				c.ReadOnly = true
				c.HTTPPort = "9090"
				c.URI = "bolt://other-host:7687"
			},
			want: []ApplyTier{ApplyTierInstant, ApplyTierHTTPBounce, ApplyTierDBRebuild},
		},
		{
			name:   "unrelated field (Telemetry) never appears in any tier",
			modify: func(c *config.Config) { c.Telemetry = !c.Telemetry },
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := baseConfig()
			next := baseConfig()
			tt.modify(next)

			got := diffTiers(old, next)
			if !slices.Equal(got, tt.want) {
				t.Errorf("diffTiers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApply_TierInstant_ReadOnlyRemovesWriteCypher(t *testing.T) {
	s := &Neo4jMCPServer{
		mcpServer: mcpsdk.NewServer("test-server", "1.0.0"),
		config:    newLiveConfig(baseConfig()),
		dbService: newLiveDBService(nil),
	}
	// Seed the initial (read-write) tool set, matching what Start() would do.
	s.reRegisterTools()
	if !hasToolNamed(s.mcpServer.ListTools(), "write-cypher") {
		t.Fatal("expected write-cypher to be registered before Apply")
	}

	newCfg := baseConfig()
	newCfg.ReadOnly = true

	result, err := s.Apply(t.Context(), newCfg)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !slices.Contains(result.Tiers, ApplyTierInstant) {
		t.Errorf("ApplyResult.Tiers = %v, want to contain %v", result.Tiers, ApplyTierInstant)
	}
	if result.Bounced {
		t.Error("ApplyResult.Bounced = true, want false for a Tier 1-only change")
	}
	if hasToolNamed(s.mcpServer.ListTools(), "write-cypher") {
		t.Error("expected write-cypher to be removed after enabling ReadOnly via Apply")
	}
	if !hasToolNamed(s.mcpServer.ListTools(), "read-cypher") {
		t.Error("expected read-cypher to remain registered after enabling ReadOnly via Apply")
	}

	// And the reverse: disabling ReadOnly should bring write-cypher back.
	back := baseConfig()
	if _, err := s.Apply(t.Context(), back); err != nil {
		t.Fatalf("Apply() (revert) error = %v", err)
	}
	if !hasToolNamed(s.mcpServer.ListTools(), "write-cypher") {
		t.Error("expected write-cypher to be re-registered after disabling ReadOnly via Apply")
	}
}

func TestApply_NoOpWhenNothingChanged(t *testing.T) {
	s := &Neo4jMCPServer{
		mcpServer: mcpsdk.NewServer("test-server", "1.0.0"),
		config:    newLiveConfig(baseConfig()),
		dbService: newLiveDBService(nil),
	}
	s.reRegisterTools()

	result, err := s.Apply(t.Context(), baseConfig())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(result.Tiers) != 0 {
		t.Errorf("ApplyResult.Tiers = %v, want empty for a no-op Apply", result.Tiers)
	}
}

func TestApply_InvalidConfigIsRejected(t *testing.T) {
	s := &Neo4jMCPServer{
		mcpServer: mcpsdk.NewServer("test-server", "1.0.0"),
		config:    newLiveConfig(baseConfig()),
		dbService: newLiveDBService(nil),
	}

	invalid := baseConfig()
	invalid.URI = "" // Config.Validate requires a non-empty URI

	if _, err := s.Apply(t.Context(), invalid); err == nil {
		t.Fatal("Apply() error = nil, want an error for an invalid config")
	}
	if s.config.Get().URI == "" {
		t.Error("live config was mutated despite Apply rejecting the candidate as invalid")
	}
}

func hasToolNamed(tools []mcpsdk.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
