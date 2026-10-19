// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"fmt"
	"strings"
)

// LoadConfig loads configuration by resolving each field in Fields() in
// order of precedence — CLI flags, then environment variables, then an
// optional config file, then built-in defaults — and validates the result.
//
// overrides may be nil, matching a run with no CLI flags supplied.
func LoadConfig(overrides CLIOverrides) (*Config, error) {
	cfg, errs := LoadConfigAll(overrides)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return cfg, nil
}

// LoadConfigAll is LoadConfig reporting every problem it finds rather than
// stopping at the first. The returned Config is nil only when the config
// file itself could not be read or parsed; otherwise it is the best-effort
// resolved configuration, even when errs is non-empty.
func LoadConfigAll(overrides CLIOverrides) (*Config, []error) {
	configFilePath := ConfigFilePath(overrides)

	var fileValues map[string]string
	var instances []NeoInstance
	if configFilePath != "" {
		values, insts, err := loadConfigFile(configFilePath)
		if err != nil {
			return nil, []error{err}
		}
		fileValues = values
		instances = insts
	}

	sources := []Source{
		overrideSource{values: overrides},
		envSource{},
		fileSource{values: fileValues},
	}

	cfg := &Config{}
	for _, f := range Fields() {
		f.Setter(cfg, resolveField(f, sources...))
	}
	// Instances has no Field/Setter (see the comment on Config.Instances) —
	// it's file-only by construction, so it's set directly here rather than
	// through the CLI/env/file precedence chain the loop above resolves.
	cfg.Instances = instances
	var errs []error
	if configFilePath != "" {
		customTools, err := loadCustomTools(configFilePath)
		if err != nil {
			errs = append(errs, err)
		}
		cfg.CustomTools = customTools
	}

	// HTTPPort's default depends on HTTPTLSEnabled, which is only known once
	// the loop above has resolved it — this can't be expressed as a per-field
	// default in the schema.
	if cfg.HTTPPort == "" {
		if cfg.HTTPTLSEnabled {
			cfg.HTTPPort = "443"
		} else {
			cfg.HTTPPort = "80"
		}
	}

	// Normalize and validate the header names. Each is trimmed in place; an
	// empty one is an error since there is no sensible default to fall back
	// to once it was explicitly configured.
	headers := []struct {
		label, envVar string
		value         *string
	}{
		{"auth header name", "NEO4J_HTTP_AUTH_HEADER_NAME", &cfg.AuthHeaderName},
		{"tools header name", "NEO4J_MCP_HTTP_TOOLS_HEADER_NAME", &cfg.HTTPToolsHeaderName},
		{"tool categories header name", "NEO4J_MCP_HTTP_TOOL_CATEGORIES_HEADER_NAME", &cfg.HTTPToolCategoriesHeaderName},
		{"API key header name", "NEO4J_MCP_HTTP_API_KEY_HEADER_NAME", &cfg.HTTPAPIKeyHeaderName},
	}
	for _, h := range headers {
		trimmed := strings.TrimSpace(*h.value)
		if trimmed == "" {
			errs = append(errs, fmt.Errorf("invalid %s: explicitly configured header name cannot be empty; unset %s or provide a valid header name", h.label, h.envVar))
			continue
		}
		*h.value = trimmed
	}

	errs = append(errs, cfg.ValidateAll()...)
	return cfg, errs
}
