// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"fmt"
	"os"
	"strings"
)

// Source resolves a Field to a raw string value, if it has one.
type Source interface {
	Lookup(f Field) (value string, ok bool)
}

// overrideSource resolves values from CLI flags, keyed by Field.Name.
type overrideSource struct {
	values CLIOverrides
}

func (s overrideSource) Lookup(f Field) (string, bool) {
	v := s.values[f.Name]
	return v, v != ""
}

// envSource resolves values from the process environment. It also checks
// every deprecated alias unconditionally, printing DeprecatedVariableMessage
// whenever one is set — independent of whether the canonical variable (or a
// higher-precedence source such as a CLI flag) already won. This preserves
// the pre-refactor behaviour where the warning fires purely based on the
// deprecated variable being set, not on what ultimately gets used.
type envSource struct{}

func (envSource) Lookup(f Field) (string, bool) {
	value := os.Getenv(f.EnvVar)
	for _, deprecated := range f.DeprecatedEnvVars {
		if dv := os.Getenv(deprecated); dv != "" {
			fmt.Fprintf(os.Stderr, DeprecatedVariableMessage, deprecated, f.EnvVar)
			if value == "" {
				value = dv
			}
		}
	}
	return value, value != ""
}

// fileSource resolves values from an optional parsed config file, keyed by
// the lower-cased canonical env var name (e.g. "neo4j_uri").
type fileSource struct {
	values map[string]string
}

func (s fileSource) Lookup(f Field) (string, bool) {
	if s.values == nil {
		return "", false
	}
	v, ok := s.values[strings.ToLower(f.EnvVar)]
	return v, ok && v != ""
}

// resolveField queries every source — so side effects like the deprecated-env
// warning always run regardless of who wins — and returns the first
// non-empty value found, in source order. Returns "" if none have a value.
func resolveField(f Field, sources ...Source) string {
	result := ""
	found := false
	for _, src := range sources {
		v, ok := src.Lookup(f)
		if ok && !found {
			result = v
			found = true
		}
	}
	return result
}
