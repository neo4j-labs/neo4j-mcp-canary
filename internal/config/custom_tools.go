// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// customToolsKey is the config file's top-level key for operator-defined
// tools. Like neo4j_instances it is a list of objects, so it is file-only.
const customToolsKey = "custom_tools"

// CustomToolAccess declares whether a custom tool may modify the graph.
type CustomToolAccess string

const (
	CustomToolAccessRead      CustomToolAccess = "read"
	CustomToolAccessReadWrite CustomToolAccess = "readwrite"
)

// Custom tool parameter types, mapped to JSON schema types.
const (
	CustomParamString  = "string"
	CustomParamInteger = "integer"
	CustomParamNumber  = "number"
	CustomParamBoolean = "boolean"
)

var validCustomParamTypes = []string{CustomParamString, CustomParamInteger, CustomParamNumber, CustomParamBoolean}

// CustomToolParam is one input a custom tool accepts; its value is bound to
// the Cypher parameter of the same name.
type CustomToolParam struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description" yaml:"description"`
	// Required means the statement must receive a value. With a Default
	// the operator supplies it, so the caller may omit the argument.
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
	// Enum restricts the accepted values; each entry is compared after
	// type coercion, so numbers and booleans may be listed unquoted.
	Enum    []any `json:"enum,omitempty" yaml:"enum,omitempty"`
	Default any   `json:"default,omitempty" yaml:"default,omitempty"`
}

// CustomTool is an operator-defined tool that runs a fixed Cypher statement.
type CustomTool struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	// Category is a free-form operator label (e.g. "crm") that can be used
	// in NEO4J_MCP_ENABLED_TOOL_CATEGORIES like a built-in category.
	Category  string            `json:"category" yaml:"category"`
	Access    CustomToolAccess  `json:"access" yaml:"access"`
	Statement string            `json:"statement" yaml:"statement"`
	Params    []CustomToolParam `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

var (
	customToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	customCategoryPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	customParamPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	cypherParamRef        = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
)

const maxCustomToolNameLen = 64

// readRawConfigFile parses the config file at path into a generic map,
// choosing JSON or YAML by extension.
func readRawConfigFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) // #nosec G703 -- operator-supplied --config-file/NEO4J_CONFIG_FILE value, same trust as loadConfigFile
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}
	raw := map[string]any{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &raw)
	case ".json":
		err = json.Unmarshal(data, &raw)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
	}
	return raw, nil
}

// loadCustomTools reads the config file at path and decodes its custom_tools
// list (nil when the key is absent). The file is parsed a second time
// rather than threading a third return value through loadConfigFile.
func loadCustomTools(path string) ([]CustomTool, error) {
	raw, err := readRawConfigFile(path)
	if err != nil {
		return nil, err
	}
	rawTools, ok := popKeyCaseInsensitive(raw, customToolsKey)
	if !ok {
		return nil, nil
	}
	b, err := json.Marshal(rawTools)
	if err != nil {
		return nil, fmt.Errorf("config file %q: %s: failed to encode: %w", path, customToolsKey, err)
	}
	var customTools []CustomTool
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&customTools); err != nil {
		return nil, fmt.Errorf("config file %q: %s: failed to decode (expected a list of tool objects): %w", path, customToolsKey, err)
	}
	return customTools, nil
}

// ConfigFilePath returns the config file path selected by --config-file or
// NEO4J_CONFIG_FILE, or "" when none is set.
func ConfigFilePath(overrides CLIOverrides) string {
	if p := overrides[configFileOverrideKey]; p != "" {
		return p
	}
	return os.Getenv(configFileEnvVar)
}

// UnknownFileKeys returns the top-level keys in the config file that match
// no configuration parameter. They are otherwise ignored without a word, so
// a misspelt key silently leaves its setting at the default.
func UnknownFileKeys(path string) ([]string, error) {
	raw, err := readRawConfigFile(path)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{instancesKey: true, customToolsKey: true}
	for _, f := range Fields() {
		known[strings.ToLower(f.EnvVar)] = true
		for _, d := range f.DeprecatedEnvVars {
			known[strings.ToLower(d)] = true
		}
	}
	var unknown []string
	for k := range raw {
		if !known[strings.ToLower(k)] {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	return unknown, nil
}

// ValidateCustomTools checks every custom tool definition and returns all
// problems joined (nil if none). Collisions with built-in tool names and
// categories are checked by the server, which owns the built-in registry.
func ValidateCustomTools(customTools []CustomTool) error {
	var errs []error
	seen := map[string]bool{}
	for i, t := range customTools {
		for _, err := range validateCustomTool(t) {
			errs = append(errs, fmt.Errorf("%s[%d] (%s): %w", customToolsKey, i, t.Name, err))
		}
		if seen[t.Name] {
			errs = append(errs, fmt.Errorf("%s[%d]: duplicate tool name %q", customToolsKey, i, t.Name))
		}
		seen[t.Name] = true
	}
	return errors.Join(errs...)
}

// validateCustomTool returns every problem with one tool definition.
func validateCustomTool(t CustomTool) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(t.Name) > maxCustomToolNameLen || !customToolNamePattern.MatchString(t.Name) {
		add("name must be lower-case kebab-case (e.g. \"find-customer\") and at most %d characters", maxCustomToolNameLen)
	}
	if strings.TrimSpace(t.Description) == "" {
		add("description is required; say what the tool does and how to use it so the model can choose it correctly")
	}
	if !customCategoryPattern.MatchString(t.Category) {
		add("category is required and must be lower-case letters, digits, '-' or '_' (e.g. \"crm\"), at most 32 characters")
	}
	if t.Access != CustomToolAccessRead && t.Access != CustomToolAccessReadWrite {
		add("access must be %q or %q, got %q", CustomToolAccessRead, CustomToolAccessReadWrite, t.Access)
	}
	// Parameter references are looked for in the code only: a $name inside a
	// string literal, quoted identifier or comment is not a parameter.
	code := cypherCode(t.Statement)
	if strings.TrimSpace(code) == "" {
		add("statement is required (a statement that is only comments or whitespace is empty)")
	}

	declared := map[string]bool{}
	for j, p := range t.Params {
		if !customParamPattern.MatchString(p.Name) {
			add("parameters[%d]: name %q must be a valid Cypher parameter name (letters, digits, '_')", j, p.Name)
		}
		if declared[p.Name] {
			add("parameters[%d]: duplicate parameter %q", j, p.Name)
		}
		declared[p.Name] = true
		if !slices.Contains(validCustomParamTypes, p.Type) {
			add("parameters[%d] (%s): type must be one of %v, got %q", j, p.Name, validCustomParamTypes, p.Type)
			continue
		}
		if strings.TrimSpace(p.Description) == "" {
			add("parameters[%d] (%s): description is required", j, p.Name)
		}
		for _, e := range p.Enum {
			if _, err := CoerceCustomParam(p.Type, e); err != nil {
				add("parameters[%d] (%s): enum value %v: %v", j, p.Name, e, err)
			}
		}
		if p.Default != nil {
			if _, err := CoerceCustomParam(p.Type, p.Default); err != nil {
				add("parameters[%d] (%s): default: %v", j, p.Name, err)
			}
		}
	}

	// A parameter the statement never uses, or a $reference with no
	// declaration, would only surface as a runtime failure on first use.
	referenced := map[string]bool{}
	for _, m := range cypherParamRef.FindAllStringSubmatch(code, -1) {
		if referenced[m[1]] {
			continue
		}
		referenced[m[1]] = true
		if !declared[m[1]] {
			add("statement references $%s but no parameter with that name is declared", m[1])
		}
	}
	for _, p := range t.Params {
		if !referenced[p.Name] {
			add("parameter %q is declared but never referenced as $%s in the statement", p.Name, p.Name)
		}
	}
	return errs
}

// cypherCode returns statement with string literals, backtick-quoted
// identifiers and comments blanked out, so what remains is only the Cypher
// that is executed. It is a scanner, not a parser: it tracks just the
// lexical forms that can hide a '$'. An unterminated string or block comment
// blanks to the end of the statement, since Neo4j will reject it anyway.
func cypherCode(statement string) string {
	src := []rune(statement)
	out := make([]rune, len(src))
	for i := range src {
		out[i] = ' '
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(src, i)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i < len(src) && !(src[i] == '*' && i+1 < len(src) && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			out[i] = c
			i++
		}
	}
	return string(out)
}

// skipQuoted returns the index just past the quoted run starting at src[start]
// (a string, or a backtick identifier). A backslash escapes the next rune in
// strings; a doubled quote closes and reopens, which has the same effect here.
func skipQuoted(src []rune, start int) int {
	quote := src[start]
	for i := start + 1; i < len(src); i++ {
		switch {
		case src[i] == '\\' && quote != '`':
			i++
		case src[i] == quote:
			return i + 1
		}
	}
	return len(src)
}

// CoerceCustomParam converts a value decoded from JSON (or YAML) to the Go
// type bound as a Cypher parameter for the given custom parameter type.
func CoerceCustomParam(paramType string, v any) (any, error) {
	switch paramType {
	case CustomParamString:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case CustomParamBoolean:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case CustomParamInteger:
		switch n := v.(type) {
		case int:
			return int64(n), nil
		case int64:
			return n, nil
		case float64:
			if n == float64(int64(n)) {
				return int64(n), nil
			}
		}
	case CustomParamNumber:
		switch n := v.(type) {
		case int:
			return float64(n), nil
		case int64:
			return float64(n), nil
		case float64:
			return n, nil
		}
	}
	return nil, fmt.Errorf("expected a %s, got %v (%T)", paramType, v, v)
}
