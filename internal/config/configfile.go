// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// instancesKey is the config file's top-level key for the neo4j_instances
// list (case-insensitive, matching the rest of this file's key handling).
// Unlike every other config-file key, its value is a list of objects rather
// than a scalar, so it's popped out of raw and decoded separately — see
// decodeInstances — before the scalar-only loop below runs.
const instancesKey = "neo4j_instances"

// loadConfigFile reads a JSON or YAML config file (chosen by its extension)
// into a flat, lower-cased map[string]string, plus the decoded
// neo4j_instances list (nil if the key is absent). Scalar keys are expected
// to be the lower-cased canonical environment variable name for a Field
// (e.g. "neo4j_uri", "neo4j_http_tls_enabled") — the same convention
// fileSource looks values up by. Only scalar values (string, number,
// boolean, null) are supported there; a nested map or list under any other
// key is a hard error rather than being silently mis-stringified.
func loadConfigFile(path string) (map[string]string, []NeoInstance, error) {
	data, err := os.ReadFile(path) // #nosec G703 -- path is an operator-supplied --config-file/NEO4J_CONFIG_FILE value, the same trust level as any other CLI flag or env var in this program
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	raw := map[string]any{}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("failed to parse YAML config file %q: %w", path, err)
		}
	case ".json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("failed to parse JSON config file %q: %w", path, err)
		}
	default:
		return nil, nil, fmt.Errorf("unsupported config file extension %q for %q (expected .yaml, .yml, or .json)", ext, path)
	}

	var instances []NeoInstance
	if rawInstances, ok := popKeyCaseInsensitive(raw, instancesKey); ok {
		instances, err = decodeInstances(rawInstances)
		if err != nil {
			return nil, nil, fmt.Errorf("config file %q: %s: %w", path, instancesKey, err)
		}
	}

	values := make(map[string]string, len(raw))
	for key, v := range raw {
		s, err := stringifyConfigValue(v)
		if err != nil {
			return nil, nil, fmt.Errorf("config file %q: key %q: %w", path, key, err)
		}
		values[strings.ToLower(key)] = s
	}
	return values, instances, nil
}

// popKeyCaseInsensitive removes and returns the value of the first key in
// raw that case-insensitively matches key, mirroring the case-insensitivity
// the scalar path below already gets from lower-casing every key.
func popKeyCaseInsensitive(raw map[string]any, key string) (any, bool) {
	for k, v := range raw {
		if strings.EqualFold(k, key) {
			delete(raw, k)
			return v, true
		}
	}
	return nil, false
}

// decodeInstances converts the decoded neo4j_instances value (already
// unmarshalled generically by yaml.Unmarshal or json.Unmarshal into `any`,
// so a JSON round-trip works uniformly regardless of source format) into
// []NeoInstance, applies the Database default, and expands ${VAR}
// references in every secret-bearing field.
func decodeInstances(raw any) ([]NeoInstance, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to encode: %w", err)
	}

	var instances []NeoInstance
	if err := json.Unmarshal(b, &instances); err != nil {
		return nil, fmt.Errorf("failed to decode (expected a list of instance objects): %w", err)
	}

	for i := range instances {
		if instances[i].Database == "" {
			instances[i].Database = "neo4j"
		}
		if err := expandInstanceEnvVars(&instances[i]); err != nil {
			return nil, fmt.Errorf("[%d] (%s): %w", i, instances[i].Name, err)
		}
	}
	return instances, nil
}

// envVarPattern matches a ${VAR_NAME} reference for expandEnvVars.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)}`)

// expandEnvVars replaces every ${VAR} reference in s with that environment
// variable's value. A literal string with no ${...} is returned unchanged,
// so plaintext and interpolated values can coexist field by field. Unlike
// os.Expand, a referenced variable that isn't set is a hard error rather
// than silently expanding to "" — a config file that names a secret's env
// var should never fall back to connecting with an empty password.
func expandEnvVars(s string) (string, error) {
	var missing string
	result := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := envVarPattern.FindStringSubmatch(match)[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			if missing == "" {
				missing = name
			}
			return match
		}
		return val
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %q referenced in config file is not set", missing)
	}
	return result, nil
}

// expandInstanceEnvVars applies expandEnvVars to every string field of inst
// that may carry a ${VAR}-interpolated secret. Name is deliberately excluded
// — it's a routing path segment, not a secret, and interpolating it would be
// more surprising than useful.
func expandInstanceEnvVars(inst *NeoInstance) error {
	fields := []*string{
		&inst.URI, &inst.Database,
		&inst.Auth.Username, &inst.Auth.Password,
		&inst.Auth.Issuer, &inst.Auth.JWKSURI, &inst.Auth.Audience,
	}
	for _, f := range fields {
		expanded, err := expandEnvVars(*f)
		if err != nil {
			return err
		}
		*f = expanded
	}
	if inst.Embedding != nil {
		for k, v := range inst.Embedding.Configuration {
			expanded, err := expandEnvVars(v)
			if err != nil {
				return err
			}
			inst.Embedding.Configuration[k] = expanded
		}
	}
	for i, k := range inst.Auth.APIKeys {
		expanded, err := expandEnvVars(k)
		if err != nil {
			return err
		}
		inst.Auth.APIKeys[i] = expanded
	}
	return nil
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
