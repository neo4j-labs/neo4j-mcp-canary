// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfigFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}
	return path
}

func TestLoadConfigFile_YAML(t *testing.T) {
	path := writeTempConfigFile(t, "config.yaml", `
neo4j_uri: bolt://from-yaml:7687
neo4j_username: yaml-user
neo4j_http_tls_enabled: true
neo4j_cypher_max_rows: 250
`)

	values, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() unexpected error: %v", err)
	}

	want := map[string]string{
		"neo4j_uri":              "bolt://from-yaml:7687",
		"neo4j_username":         "yaml-user",
		"neo4j_http_tls_enabled": "true",
		"neo4j_cypher_max_rows":  "250",
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("values[%q] = %q, want %q", k, values[k], v)
		}
	}
}

func TestLoadConfigFile_JSON(t *testing.T) {
	path := writeTempConfigFile(t, "config.json", `{
		"neo4j_uri": "bolt://from-json:7687",
		"neo4j_read_only": false,
		"neo4j_schema_sample_size": 500
	}`)

	values, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() unexpected error: %v", err)
	}

	want := map[string]string{
		"neo4j_uri":                "bolt://from-json:7687",
		"neo4j_read_only":          "false",
		"neo4j_schema_sample_size": "500",
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("values[%q] = %q, want %q", k, values[k], v)
		}
	}
}

func TestLoadConfigFile_CaseInsensitiveKeys(t *testing.T) {
	path := writeTempConfigFile(t, "config.yaml", `NEO4J_URI: bolt://mixed-case:7687`)

	values, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() unexpected error: %v", err)
	}
	if values["neo4j_uri"] != "bolt://mixed-case:7687" {
		t.Errorf("values[\"neo4j_uri\"] = %q, want bolt://mixed-case:7687", values["neo4j_uri"])
	}
}

func TestLoadConfigFile_MissingFile(t *testing.T) {
	_, err := loadConfigFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("loadConfigFile() expected error for missing file, got nil")
	}
}

func TestLoadConfigFile_UnsupportedExtension(t *testing.T) {
	path := writeTempConfigFile(t, "config.toml", `neo4j_uri = "bolt://localhost:7687"`)

	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported config file extension") {
		t.Errorf("loadConfigFile() error = %v, want an unsupported-extension error", err)
	}
}

func TestLoadConfigFile_NestedValueRejected(t *testing.T) {
	path := writeTempConfigFile(t, "config.yaml", `
neo4j_uri: bolt://localhost:7687
nested:
  key: value
`)

	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported value type") {
		t.Errorf("loadConfigFile() error = %v, want an unsupported-value-type error for a nested map", err)
	}
}

// TestLoadConfig_ConfigFilePrecedence exercises the full CLI > env > file >
// default precedence chain end to end through the public LoadConfig entry
// point, for the newly introduced config-file source.
func TestLoadConfig_ConfigFilePrecedence(t *testing.T) {
	path := writeTempConfigFile(t, "config.yaml", `
neo4j_uri: bolt://from-file:7687
neo4j_username: file-user
neo4j_password: file-pass
neo4j_database: file-db
neo4j_transport_mode: stdio
`)

	t.Run("file used when neither CLI nor env set", func(t *testing.T) {
		overrides := CLIOverrides{"ConfigFile": path}
		cfg, err := LoadConfig(overrides)
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.URI != "bolt://from-file:7687" {
			t.Errorf("URI = %q, want value from file", cfg.URI)
		}
		if cfg.Database != "file-db" {
			t.Errorf("Database = %q, want file-db", cfg.Database)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		t.Setenv("NEO4J_TRANSPORT_MODE", "stdio")
		t.Setenv("NEO4J_URI", "bolt://from-env:7687")
		t.Setenv("NEO4J_USERNAME", "env-user")
		t.Setenv("NEO4J_PASSWORD", "env-pass")

		overrides := CLIOverrides{"ConfigFile": path}
		cfg, err := LoadConfig(overrides)
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.URI != "bolt://from-env:7687" {
			t.Errorf("URI = %q, want value from env, not file", cfg.URI)
		}
		if cfg.Database != "file-db" {
			t.Errorf("Database = %q, want file-db (no env override set for it)", cfg.Database)
		}
	})

	t.Run("CLI overrides both env and file", func(t *testing.T) {
		t.Setenv("NEO4J_TRANSPORT_MODE", "stdio")
		t.Setenv("NEO4J_URI", "bolt://from-env:7687")
		t.Setenv("NEO4J_USERNAME", "env-user")
		t.Setenv("NEO4J_PASSWORD", "env-pass")

		overrides := CLIOverrides{
			"ConfigFile": path,
			"URI":        "bolt://from-cli:7687",
		}
		cfg, err := LoadConfig(overrides)
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.URI != "bolt://from-cli:7687" {
			t.Errorf("URI = %q, want value from CLI override", cfg.URI)
		}
	})

	t.Run("NEO4J_CONFIG_FILE env var also works", func(t *testing.T) {
		t.Setenv("NEO4J_CONFIG_FILE", path)
		t.Setenv("NEO4J_TRANSPORT_MODE", "stdio")

		cfg, err := LoadConfig(nil)
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.URI != "bolt://from-file:7687" {
			t.Errorf("URI = %q, want value from file via NEO4J_CONFIG_FILE", cfg.URI)
		}
	})

	t.Run("broken config file is a hard error", func(t *testing.T) {
		badPath := writeTempConfigFile(t, "bad.yaml", "not: valid: yaml: [")
		overrides := CLIOverrides{"ConfigFile": badPath}

		cfg, err := LoadConfig(overrides)
		if err == nil {
			t.Fatal("LoadConfig() expected error for broken config file, got nil")
		}
		if cfg != nil {
			t.Error("LoadConfig() expected nil config when config file fails to parse")
		}
	})
}
