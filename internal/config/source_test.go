// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"os"
	"strings"
	"testing"
)

func TestEnvSource_DeprecatedWarningAlwaysFires(t *testing.T) {
	// Regression guard: the deprecation warning for a legacy env var must
	// fire whenever the legacy var is set, even when the canonical var (or a
	// higher-precedence source) already wins the resolution. A naive
	// first-source-wins-and-stops resolver would only call envSource.Lookup
	// when nothing higher-precedence matched, silently dropping this warning.
	f := Field{Name: "TransportMode", EnvVar: "NEO4J_TRANSPORT_MODE", DeprecatedEnvVars: []string{"NEO4J_MCP_TRANSPORT"}}

	t.Setenv("NEO4J_TRANSPORT_MODE", "http")
	t.Setenv("NEO4J_MCP_TRANSPORT", "stdio")

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	value, ok := envSource{}.Lookup(f)
	w.Close()
	os.Stderr = oldStderr

	var buf strings.Builder
	buf.WriteString(readAll(r))

	if !ok || value != "http" {
		t.Errorf("Lookup() = (%q, %v), want (\"http\", true) — canonical var should win", value, ok)
	}
	if !strings.Contains(buf.String(), "NEO4J_MCP_TRANSPORT") {
		t.Errorf("expected deprecation warning mentioning NEO4J_MCP_TRANSPORT, got stderr: %q", buf.String())
	}
}

func TestEnvSource_DeprecatedFallbackWhenCanonicalEmpty(t *testing.T) {
	f := Field{Name: "TransportMode", EnvVar: "NEO4J_TRANSPORT_MODE", DeprecatedEnvVars: []string{"NEO4J_MCP_TRANSPORT"}}

	t.Setenv("NEO4J_MCP_TRANSPORT", "stdio")

	value, ok := envSource{}.Lookup(f)
	if !ok || value != "stdio" {
		t.Errorf("Lookup() = (%q, %v), want (\"stdio\", true) — deprecated var should be used as fallback", value, ok)
	}
}

func TestFileSource_CaseInsensitiveKeys(t *testing.T) {
	src := fileSource{values: map[string]string{"neo4j_uri": "bolt://from-file:7687"}}
	f := Field{Name: "URI", EnvVar: "NEO4J_URI"}

	value, ok := src.Lookup(f)
	if !ok || value != "bolt://from-file:7687" {
		t.Errorf("Lookup() = (%q, %v), want (\"bolt://from-file:7687\", true)", value, ok)
	}
}

func TestFileSource_NilValues(t *testing.T) {
	src := fileSource{}
	f := Field{Name: "URI", EnvVar: "NEO4J_URI"}

	if _, ok := src.Lookup(f); ok {
		t.Error("Lookup() on a nil fileSource should never report ok")
	}
}

func TestResolveField_Precedence(t *testing.T) {
	f := Field{Name: "URI", EnvVar: "NEO4J_URI"}

	t.Run("override wins over env and file", func(t *testing.T) {
		t.Setenv("NEO4J_URI", "bolt://env:7687")
		sources := []Source{
			overrideSource{values: CLIOverrides{"URI": "bolt://cli:7687"}},
			envSource{},
			fileSource{values: map[string]string{"neo4j_uri": "bolt://file:7687"}},
		}
		if got := resolveField(f, sources...); got != "bolt://cli:7687" {
			t.Errorf("resolveField() = %q, want bolt://cli:7687", got)
		}
	})

	t.Run("env wins over file when override empty", func(t *testing.T) {
		t.Setenv("NEO4J_URI", "bolt://env:7687")
		sources := []Source{
			overrideSource{values: nil},
			envSource{},
			fileSource{values: map[string]string{"neo4j_uri": "bolt://file:7687"}},
		}
		if got := resolveField(f, sources...); got != "bolt://env:7687" {
			t.Errorf("resolveField() = %q, want bolt://env:7687", got)
		}
	})

	t.Run("file used when override and env empty", func(t *testing.T) {
		sources := []Source{
			overrideSource{values: nil},
			envSource{},
			fileSource{values: map[string]string{"neo4j_uri": "bolt://file:7687"}},
		}
		if got := resolveField(f, sources...); got != "bolt://file:7687" {
			t.Errorf("resolveField() = %q, want bolt://file:7687", got)
		}
	})

	t.Run("empty when nothing set", func(t *testing.T) {
		sources := []Source{overrideSource{}, envSource{}, fileSource{}}
		if got := resolveField(f, sources...); got != "" {
			t.Errorf("resolveField() = %q, want empty string", got)
		}
	})
}

func readAll(r *os.File) string {
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}
