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

	values, instances, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() unexpected error: %v", err)
	}
	if instances != nil {
		t.Errorf("instances = %v, want nil (no neo4j_instances key)", instances)
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

	values, _, err := loadConfigFile(path)
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

	values, _, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() unexpected error: %v", err)
	}
	if values["neo4j_uri"] != "bolt://mixed-case:7687" {
		t.Errorf("values[\"neo4j_uri\"] = %q, want bolt://mixed-case:7687", values["neo4j_uri"])
	}
}

func TestLoadConfigFile_MissingFile(t *testing.T) {
	_, _, err := loadConfigFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("loadConfigFile() expected error for missing file, got nil")
	}
}

func TestLoadConfigFile_UnsupportedExtension(t *testing.T) {
	path := writeTempConfigFile(t, "config.toml", `neo4j_uri = "bolt://localhost:7687"`)

	_, _, err := loadConfigFile(path)
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

	_, _, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported value type") {
		t.Errorf("loadConfigFile() error = %v, want an unsupported-value-type error for a nested map", err)
	}
}

func TestLoadConfigFile_Instances(t *testing.T) {
	t.Run("basic auth with api key, plaintext and interpolated fields", func(t *testing.T) {
		t.Setenv("TEST_PROD_PASSWORD", "s3cret")
		path := writeTempConfigFile(t, "config.yaml", `
neo4j_instances:
  - name: prod
    uri: neo4j+s://prod.databases.neo4j.io
    database: neo4j
    auth:
      type: basic
      username: mcp_service
      password: ${TEST_PROD_PASSWORD}
      api_keys:
        - plain-key-1
`)
		_, instances, err := loadConfigFile(path)
		if err != nil {
			t.Fatalf("loadConfigFile() unexpected error: %v", err)
		}
		if len(instances) != 1 {
			t.Fatalf("len(instances) = %d, want 1", len(instances))
		}
		got := instances[0]
		if got.Name != "prod" || got.URI != "neo4j+s://prod.databases.neo4j.io" || got.Database != "neo4j" {
			t.Errorf("instance = %+v, want name=prod uri=neo4j+s://prod.databases.neo4j.io database=neo4j", got)
		}
		if got.Auth.Type != InstanceAuthBasic || got.Auth.Username != "mcp_service" {
			t.Errorf("auth = %+v, want type=basic username=mcp_service", got.Auth)
		}
		if got.Auth.Password != "s3cret" {
			t.Errorf("password = %q, want interpolated value s3cret", got.Auth.Password)
		}
		if len(got.Auth.APIKeys) != 1 || got.Auth.APIKeys[0] != "plain-key-1" {
			t.Errorf("api_keys = %v, want [plain-key-1] unchanged", got.Auth.APIKeys)
		}
	})

	t.Run("embedding config, plaintext and interpolated fields", func(t *testing.T) {
		t.Setenv("TEST_OPENAI_TOKEN", "sk-test-token")
		path := writeTempConfigFile(t, "config.yaml", `
neo4j_instances:
  - name: prod
    uri: neo4j+s://prod.databases.neo4j.io
    auth:
      type: basic_passthrough
    embedding:
      provider: openai
      configuration:
        token: ${TEST_OPENAI_TOKEN}
        model: text-embedding-3-small
`)
		_, instances, err := loadConfigFile(path)
		if err != nil {
			t.Fatalf("loadConfigFile() unexpected error: %v", err)
		}
		got := instances[0].Embedding
		if got == nil {
			t.Fatal("Embedding = nil, want a configured EmbeddingConfig")
		}
		if got.Provider != EmbeddingProviderOpenAI {
			t.Errorf("Provider = %q, want openai", got.Provider)
		}
		if got.Configuration["token"] != "sk-test-token" {
			t.Errorf("token = %q, want interpolated value sk-test-token", got.Configuration["token"])
		}
		if got.Configuration["model"] != "text-embedding-3-small" {
			t.Errorf("model = %q, want text-embedding-3-small unchanged", got.Configuration["model"])
		}
	})

	t.Run("database defaults to neo4j when omitted", func(t *testing.T) {
		path := writeTempConfigFile(t, "config.yaml", `
neo4j_instances:
  - name: staging
    uri: neo4j://staging.internal:7687
    auth:
      type: basic_passthrough
`)
		_, instances, err := loadConfigFile(path)
		if err != nil {
			t.Fatalf("loadConfigFile() unexpected error: %v", err)
		}
		if instances[0].Database != "neo4j" {
			t.Errorf("Database = %q, want default neo4j", instances[0].Database)
		}
	})

	t.Run("missing interpolated env var is a hard error", func(t *testing.T) {
		path := writeTempConfigFile(t, "config.yaml", `
neo4j_instances:
  - name: prod
    uri: neo4j+s://prod.databases.neo4j.io
    auth:
      type: basic
      username: mcp_service
      password: ${TEST_DOES_NOT_EXIST_VAR}
      api_keys: [k]
`)
		_, _, err := loadConfigFile(path)
		if err == nil || !strings.Contains(err.Error(), "TEST_DOES_NOT_EXIST_VAR") {
			t.Errorf("loadConfigFile() error = %v, want error naming the unset env var", err)
		}
	})

	t.Run("neo4j_instances key is case-insensitive and doesn't leak into scalar values", func(t *testing.T) {
		path := writeTempConfigFile(t, "config.yaml", `
NEO4J_INSTANCES:
  - name: staging
    uri: neo4j://staging.internal:7687
    auth:
      type: basic_passthrough
neo4j_transport_mode: http
`)
		values, instances, err := loadConfigFile(path)
		if err != nil {
			t.Fatalf("loadConfigFile() unexpected error: %v", err)
		}
		if len(instances) != 1 {
			t.Fatalf("len(instances) = %d, want 1", len(instances))
		}
		if _, ok := values["neo4j_instances"]; ok {
			t.Errorf("values still contains neo4j_instances key: %v", values)
		}
		if values["neo4j_transport_mode"] != "http" {
			t.Errorf("neo4j_transport_mode = %q, want http", values["neo4j_transport_mode"])
		}
	})

	t.Run("malformed instances list is a hard error", func(t *testing.T) {
		path := writeTempConfigFile(t, "config.yaml", `
neo4j_instances:
  not_a_list: true
`)
		_, _, err := loadConfigFile(path)
		if err == nil {
			t.Fatal("loadConfigFile() expected error for malformed neo4j_instances, got nil")
		}
	})
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
