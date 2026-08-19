// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"fmt"
	"os"
	"strings"
)

// LoadConfig loads configuration by resolving each field in Fields() in
// order of precedence — CLI flags, then environment variables, then an
// optional config file, then built-in defaults — and validates the result.
//
// overrides may be nil, matching a run with no CLI flags supplied.
func LoadConfig(overrides CLIOverrides) (*Config, error) {
	configFilePath := overrides[configFileOverrideKey]
	if configFilePath == "" {
		configFilePath = os.Getenv(configFileEnvVar)
	}

	var fileValues map[string]string
	if configFilePath != "" {
		values, err := loadConfigFile(configFilePath)
		if err != nil {
			return nil, err
		}
		fileValues = values
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

	// Normalize and validate the auth header name.
	headName := strings.TrimSpace(cfg.AuthHeaderName)
	if headName == "" {
		return nil, fmt.Errorf("invalid auth header name: explicitly configured header name cannot be empty; unset NEO4J_HTTP_AUTH_HEADER_NAME or provide a valid header name")
	}
	cfg.AuthHeaderName = headName

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
