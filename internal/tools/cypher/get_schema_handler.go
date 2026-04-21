// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// schemaQuery is the APOC query used to retrieve comprehensive schema information.
// apoc.meta.schema samples the graph and returns, for each label and relationship
// type, the properties observed on that entity along with outgoing relationship
// information. The $sampleSize parameter bounds how many nodes per label are
// examined — the upstream/reference MCP server uses exactly this approach.
const schemaQuery = `
	CALL apoc.meta.schema({sample: $sampleSize})
	YIELD value
	UNWIND keys(value) as key
	WITH key, value[key] as value
	RETURN key, value { .properties, .type, .relationships } as value
`

// GetSchemaHandler returns a handler function for the get-schema tool.
// The schemaSampleSize is forwarded to apoc.meta.schema's `sample` parameter
// and controls how many records per label APOC will examine when inferring
// the schema. Wiring is preserved from upstream config so NEO4J_SCHEMA_SAMPLE_SIZE
// still drives this knob.
func GetSchemaHandler(deps *tools.ToolDependencies, schemaSampleSize int32) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetSchema(ctx, deps, schemaSampleSize)
	}
}

// handleGetSchema retrieves Neo4j schema information using APOC's apoc.meta.schema
// procedure. On an empty database the response is a human-readable message rather
// than an empty schema object — matching the upstream MCP server's behaviour.
func handleGetSchema(ctx context.Context, deps *tools.ToolDependencies, schemaSampleSize int32) (*mcp.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	slog.Info("retrieving schema from the database")

	records, err := deps.DBService.ExecuteReadQuery(ctx, schemaQuery, map[string]any{
		"sampleSize": schemaSampleSize,
	})
	if err != nil {
		slog.Error("failed to execute schema query", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(records) == 0 {
		slog.Warn("schema is empty, no data in the database")
		return mcp.NewToolResultText("The get-schema tool executed successfully; however, since the Neo4j instance contains no data, no schema information was returned."), nil
	}

	structuredOutput, err := processCypherSchema(records)
	if err != nil {
		slog.Error("failed to process get-schema Cypher query", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize structured schema", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(jsonData)), nil
}

// --- Output types ---

// SchemaItem is one entry in the schema list — either a node label or a
// relationship type, distinguished by Value.Type ("node" vs "relationship").
type SchemaItem struct {
	Key   string       `json:"key"`
	Value SchemaDetail `json:"value"`
}

// SchemaDetail describes a single schema entry. Properties map property name
// to a simplified type string; Relationships (present only for nodes) maps
// outgoing relationship type to its direction, target labels, and properties.
type SchemaDetail struct {
	Type          string                  `json:"type"`
	Properties    map[string]string       `json:"properties,omitempty"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
}

// Relationship describes an outgoing relationship from a node in the schema:
// which types it is, which target labels it connects to, and what properties
// it carries.
type Relationship struct {
	Direction  string            `json:"direction"`
	Labels     []string          `json:"labels"` // Target node labels
	Properties map[string]string `json:"properties,omitempty"`
}

// processCypherSchema transforms the raw APOC records into the JSON-tagged struct
// shape returned to the caller. Two simplifications are applied versus the raw
// APOC output:
//
// Properties are collapsed from the full APOC shape to just propName -> type.
// From:
//
//	title: {
//	  unique: false,
//	  indexed: false,
//	  type: "STRING",
//	  existence: false,
//	}
//
// To:
//
//	title: "STRING"
//
// Relationships are collapsed similarly, keeping only direction, target labels,
// and simplified property types. The `count` field from APOC is dropped.
//
// Null values are stripped throughout. The same APOC query works across multiple
// supported Neo4j versions; only the shape of the response is normalised here.
func processCypherSchema(records []*neo4j.Record) ([]SchemaItem, error) {
	simplifiedSchema := make([]SchemaItem, 0, len(records))

	for _, record := range records {
		// Extract "key" (e.g. "Movie", "ACTED_IN")
		keyRaw, ok := record.Get("key")
		if !ok {
			return nil, fmt.Errorf("missing 'key' column in record")
		}
		keyStr, ok := keyRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid key returned")
		}

		// Extract "value" — the map containing properties, type, relationships
		valRaw, ok := record.Get("value")
		if !ok {
			return nil, fmt.Errorf("missing 'value' column in record")
		}
		data, ok := valRaw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid value returned")
		}

		// Extract type ("node" or "relationship")
		itemType, ok := data["type"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid type returned")
		}

		// Simplify properties
		cleanProps, ok := simplifyProperties(data["properties"])
		if !ok {
			return nil, fmt.Errorf("invalid properties returned")
		}

		// Simplify relationships (nodes only — relationships entries don't have this field populated)
		var cleanRels map[string]Relationship
		if rawRels, relsExist := data["relationships"]; relsExist && rawRels != nil {
			if relsMap, ok := rawRels.(map[string]interface{}); ok && len(relsMap) > 0 {
				cleanRels = make(map[string]Relationship)
				for relName, rawRelDetails := range relsMap {
					relDetails, ok := rawRelDetails.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid relationship returned")
					}

					direction, ok := relDetails["direction"].(string)
					if !ok {
						return nil, fmt.Errorf("invalid direction returned")
					}

					rawLabels, ok := relDetails["labels"].([]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid relationship labels returned")
					}
					var labels []string
					for _, l := range rawLabels {
						if lStr, ok := l.(string); ok {
							labels = append(labels, lStr)
						}
					}

					relProps, ok := simplifyProperties(relDetails["properties"])
					if !ok {
						return nil, fmt.Errorf("invalid relationship properties returned")
					}

					cleanRels[relName] = Relationship{
						Direction:  direction,
						Labels:     labels,
						Properties: relProps,
					}
				}
			}
		}

		simplifiedSchema = append(simplifiedSchema, SchemaItem{
			Key: keyStr,
			Value: SchemaDetail{
				Type:          itemType,
				Properties:    cleanProps,
				Relationships: cleanRels,
			},
		})
	}

	return simplifiedSchema, nil
}

// simplifyProperties strips the auxiliary APOC fields (unique, indexed, existence)
// and keeps only the type name, returning a flat propName -> type map.
// Returns (nil, true) for a nil or empty input — both are valid ("this entity has
// no properties") rather than error conditions. Returns (nil, false) only when
// the input shape is unexpectedly non-map.
func simplifyProperties(rawProps interface{}) (map[string]string, bool) {
	if rawProps == nil {
		return nil, true
	}
	props, ok := rawProps.(map[string]interface{})
	if !ok {
		return nil, false
	}
	if len(props) == 0 {
		return nil, true
	}
	cleanProps := make(map[string]string, len(props))
	for propName, rawPropDetails := range props {
		propDetails, ok := rawPropDetails.(map[string]interface{})
		if !ok {
			continue
		}
		if typeName, ok := propDetails["type"].(string); ok {
			cleanProps[propName] = typeName
		} else {
			return nil, false
		}
	}
	if len(cleanProps) == 0 {
		return nil, true
	}
	return cleanProps, true
}
