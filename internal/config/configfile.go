// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// loadConfigFile reads a JSON or YAML config file (chosen by its extension)
// into a flat, lower-cased map[string]string. Keys are expected to be the
// lower-cased canonical environment variable name for a Field (e.g.
// "neo4j_uri", "neo4j_http_tls_enabled") — the same convention fileSource
// looks values up by. Only scalar values (string, number, boolean, null) are
// supported; a nested map or list is a hard error rather than being silently
// mis-stringified.
func loadConfigFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) // #nosec G703 -- path is an operator-supplied --config-file/NEO4J_CONFIG_FILE value, the same trust level as any other CLI flag or env var in this program
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	raw := map[string]any{}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config file %q: %w", path, err)
		}
	case ".json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse JSON config file %q: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported config file extension %q for %q (expected .yaml, .yml, or .json)", ext, path)
	}

	values := make(map[string]string, len(raw))
	for key, v := range raw {
		s, err := stringifyConfigValue(v)
		if err != nil {
			return nil, fmt.Errorf("config file %q: key %q: %w", path, key, err)
		}
		values[strings.ToLower(key)] = s
	}
	return values, nil
}

// stringifyConfigValue converts a decoded YAML/JSON scalar into the raw
// string form the rest of the resolution pipeline expects (the same string
// form a CLI flag or env var would carry).
func stringifyConfigValue(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		// YAML/JSON numbers decode as float64; format integral values without
		// a trailing ".0" and avoid scientific notation either way.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported value type %T; config file values must be scalars (string, number, or boolean)", v)
	}
}
