// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Params is a map of Cypher query parameters with custom JSON unmarshaling
// that preserves numeric types correctly for Neo4j.
//
// When unmarshaling from JSON:
//   - Whole numbers (e.g., 1, 42, -10) become int64
//   - Numbers with fractional parts (e.g., 1.5, 3.14) become float64
//   - Numbers with decimal notation but no fraction (e.g., 10.0) become float64
//   - Other types (strings, booleans, null) are preserved as-is
//
// The unmarshaler also tolerates a common MCP-client mistake: sending the
// params value as a JSON-encoded string (e.g. "params": "{\"x\": 1}") rather
// than as a JSON object. When it detects this double-encoding, it re-parses
// the string contents as JSON and uses the resulting object, so clients that
// stringify structured tool arguments don't fail with a cryptic Go type error.
type Params map[string]any

func (cp *Params) UnmarshalJSON(data []byte) error {
	// Fast path: params arrived as a JSON object (or null), which is the normal case.
	if params, err := decodeParamsObject(data); err == nil {
		*cp = params
		return nil
	}

	// Recovery path: some MCP clients — notably some LLM-driven ones — serialise
	// structured tool arguments as JSON strings by mistake, sending
	//   "params": "{\"x\": 1}"
	// instead of
	//   "params": {"x": 1}.
	// If the raw payload is a JSON string, re-parse its contents as an object so
	// we recover transparently instead of surfacing an opaque unmarshal error.
	var encoded string
	if strErr := json.Unmarshal(data, &encoded); strErr == nil {
		if params, innerErr := decodeParamsObject([]byte(encoded)); innerErr == nil {
			*cp = params
			return nil
		}
	}

	return fmt.Errorf("params must be a JSON object (for example {\"name\": \"Alice\"}); got: %s", paramsPreview(data))
}

// decodeParamsObject parses data as a JSON object and runs ConvertNumbers over
// the result so that whole-number values are surfaced as int64 rather than
// float64. Returns (nil, nil) for a JSON null payload, matching the zero-value
// semantics the handler already relies on.
func decodeParamsObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var temp map[string]any
	if err := decoder.Decode(&temp); err != nil {
		return nil, err
	}

	converted, ok := ConvertNumbers(temp).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected shape after type conversion")
	}
	return converted, nil
}

// paramsPreview returns a short, UTF-8-safe excerpt of raw JSON data suitable
// for embedding in an error message. Kept small so error logs stay readable
// even when a client sends a huge blob.
func paramsPreview(data []byte) string {
	const max = 80
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + "…"
}

func ConvertNumbers(input any) any {
	switch v := input.(type) {
	case json.Number:
		// Try to parse as Int64 first
		if i, err := v.Int64(); err == nil {
			return i
		}
		// If it fails (because of decimal point), parse as Float64
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String() // Fallback

	case map[string]any:
		for k, val := range v {
			v[k] = ConvertNumbers(val)
		}
		return v

	case []any:
		for i, val := range v {
			v[i] = ConvertNumbers(val)
		}
		return v
	}
	return input
}
