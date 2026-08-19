// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

import (
	"encoding/json"
	"fmt"

	toon "github.com/toon-format/toon-go"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

// EncodeOutput renders an already JSON-encoded tool response in the
// requested output format. JSON payloads pass through unchanged; TOON
// payloads are produced by decoding the JSON back into a generic value and
// re-encoding it with the TOON encoder. This keeps every JSON-producing call
// site (database.FormatQueryResultAsJSON, database.FormatRecordsAsJSON, and
// the get-schema handler's own json.Marshal) completely unaware of the
// output format — they always produce JSON, and this is the single place
// that optionally re-renders it — at the cost of a redundant decode pass.
// Tool payloads are already capped well under 1MB (see CypherMaxBytes), so
// that cost is negligible next to the token savings TOON provides on the
// tabular row shapes these tools return.
func EncodeOutput(jsonPayload string, format config.OutputFormat) (string, error) {
	if format != config.OutputFormatTOON {
		return jsonPayload, nil
	}

	var v any
	if err := json.Unmarshal([]byte(jsonPayload), &v); err != nil {
		return "", fmt.Errorf("failed to decode JSON for TOON conversion: %w", err)
	}

	toonPayload, err := toon.MarshalString(v)
	if err != nil {
		return "", fmt.Errorf("failed to encode TOON output: %w", err)
	}
	return toonPayload, nil
}
