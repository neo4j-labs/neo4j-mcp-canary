// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// showConstraintsQuery and showIndexesQuery use explicit YIELD lists rather
// than YIELD * — the column set SHOW CONSTRAINTS/SHOW INDEXES returns has
// shifted across Neo4j versions, so pinning the exact columns this handler
// depends on keeps it stable regardless of what else a given server version adds.
const showConstraintsQuery = `SHOW CONSTRAINTS YIELD id, name, type, entityType, labelsOrTypes, properties, ownedIndex, propertyType`

const showIndexesQuery = `SHOW INDEXES YIELD id, name, state, populationPercent, type, entityType, labelsOrTypes, properties, indexProvider, owningConstraint`

func ListConstraintsAndIndexesHandler(deps *tools.ToolDependencies) func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleListConstraintsAndIndexes(ctx, deps)
	}
}

func handleListConstraintsAndIndexes(ctx context.Context, deps *tools.ToolDependencies) (*mcpsdk.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcpsdk.NewToolResultError(errMessage), nil
	}

	constraintRecords, err := deps.DBService.ExecuteReadQuery(ctx, showConstraintsQuery, nil)
	if err != nil {
		slog.Error("failed to execute show constraints query", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	indexRecords, err := deps.DBService.ExecuteReadQuery(ctx, showIndexesQuery, nil)
	if err != nil {
		slog.Error("failed to execute show indexes query", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	constraints, err := processConstraints(constraintRecords)
	if err != nil {
		slog.Error("failed to process show constraints results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	indexes, err := processIndexes(indexRecords)
	if err != nil {
		slog.Error("failed to process show indexes results", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	structuredOutput := ConstraintsAndIndexes{
		Constraints: constraints,
		Indexes:     indexes,
	}

	jsonData, err := json.Marshal(structuredOutput)
	if err != nil {
		slog.Error("failed to serialize constraints and indexes", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	response, err := tools.EncodeOutput(string(jsonData), deps.OutputFormat)
	if err != nil {
		slog.Error("failed to encode constraints and indexes", "error", err)
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultTextAndStructured(response, json.RawMessage(jsonData)), nil
}

// --- Output types ---

// ConstraintInfo describes a single row returned by SHOW CONSTRAINTS.
type ConstraintInfo struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	EntityType    string   `json:"entityType"`
	LabelsOrTypes []string `json:"labelsOrTypes"`
	Properties    []string `json:"properties"`
	OwnedIndex    string   `json:"ownedIndex,omitempty"`
	PropertyType  string   `json:"propertyType,omitempty"`
}

// IndexInfo describes a single row returned by SHOW INDEXES.
type IndexInfo struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	State             string   `json:"state"`
	PopulationPercent float64  `json:"populationPercent"`
	Type              string   `json:"type"`
	EntityType        string   `json:"entityType"`
	LabelsOrTypes     []string `json:"labelsOrTypes"`
	Properties        []string `json:"properties"`
	IndexProvider     string   `json:"indexProvider,omitempty"`
	OwningConstraint  string   `json:"owningConstraint,omitempty"`
}

// ConstraintsAndIndexes is the tool's structured output: the full set of
// constraints and indexes present on the database, listed independently of
// each other. A constraint and its backing index are cross-referenced via
// ConstraintInfo.OwnedIndex / IndexInfo.OwningConstraint rather than nested.
type ConstraintsAndIndexes struct {
	Constraints []ConstraintInfo `json:"constraints"`
	Indexes     []IndexInfo      `json:"indexes"`
}

func processConstraints(records []*neo4j.Record) ([]ConstraintInfo, error) {
	constraints := make([]ConstraintInfo, 0, len(records))
	for _, record := range records {
		id, err := getInt64(record, "id")
		if err != nil {
			return nil, err
		}
		name, err := getString(record, "name")
		if err != nil {
			return nil, err
		}
		constraintType, err := getString(record, "type")
		if err != nil {
			return nil, err
		}
		entityType, err := getString(record, "entityType")
		if err != nil {
			return nil, err
		}
		labelsOrTypes, err := getStringSlice(record, "labelsOrTypes")
		if err != nil {
			return nil, err
		}
		properties, err := getStringSlice(record, "properties")
		if err != nil {
			return nil, err
		}

		constraints = append(constraints, ConstraintInfo{
			ID:            id,
			Name:          name,
			Type:          constraintType,
			EntityType:    entityType,
			LabelsOrTypes: labelsOrTypes,
			Properties:    properties,
			OwnedIndex:    getOptionalString(record, "ownedIndex"),
			PropertyType:  getOptionalString(record, "propertyType"),
		})
	}
	return constraints, nil
}

func processIndexes(records []*neo4j.Record) ([]IndexInfo, error) {
	indexes := make([]IndexInfo, 0, len(records))
	for _, record := range records {
		id, err := getInt64(record, "id")
		if err != nil {
			return nil, err
		}
		name, err := getString(record, "name")
		if err != nil {
			return nil, err
		}
		state, err := getString(record, "state")
		if err != nil {
			return nil, err
		}
		populationPercent, err := getFloat64(record, "populationPercent")
		if err != nil {
			return nil, err
		}
		indexType, err := getString(record, "type")
		if err != nil {
			return nil, err
		}
		entityType, err := getString(record, "entityType")
		if err != nil {
			return nil, err
		}
		labelsOrTypes, err := getStringSlice(record, "labelsOrTypes")
		if err != nil {
			return nil, err
		}
		properties, err := getStringSlice(record, "properties")
		if err != nil {
			return nil, err
		}

		indexes = append(indexes, IndexInfo{
			ID:                id,
			Name:              name,
			State:             state,
			PopulationPercent: populationPercent,
			Type:              indexType,
			EntityType:        entityType,
			LabelsOrTypes:     labelsOrTypes,
			Properties:        properties,
			IndexProvider:     getOptionalString(record, "indexProvider"),
			OwningConstraint:  getOptionalString(record, "owningConstraint"),
		})
	}
	return indexes, nil
}

func getInt64(record *neo4j.Record, column string) (int64, error) {
	raw, ok := record.Get(column)
	if !ok {
		return 0, fmt.Errorf("missing '%s' column in record", column)
	}
	value, ok := raw.(int64)
	if !ok {
		return 0, fmt.Errorf("invalid '%s' returned: expected int64, got %T", column, raw)
	}
	return value, nil
}

func getFloat64(record *neo4j.Record, column string) (float64, error) {
	raw, ok := record.Get(column)
	if !ok {
		return 0, fmt.Errorf("missing '%s' column in record", column)
	}
	value, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("invalid '%s' returned: expected float64, got %T", column, raw)
	}
	return value, nil
}

func getString(record *neo4j.Record, column string) (string, error) {
	raw, ok := record.Get(column)
	if !ok {
		return "", fmt.Errorf("missing '%s' column in record", column)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("invalid '%s' returned: expected string, got %T", column, raw)
	}
	return value, nil
}

// getOptionalString reads a nullable string column. A missing key, a nil
// value, or an unexpected type are all treated as "no value" (empty string)
// rather than an error — SHOW CONSTRAINTS/SHOW INDEXES leave ownedIndex,
// propertyType, indexProvider, and owningConstraint null when not applicable.
func getOptionalString(record *neo4j.Record, column string) string {
	raw, ok := record.Get(column)
	if !ok || raw == nil {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return value
}

func getStringSlice(record *neo4j.Record, column string) ([]string, error) {
	raw, ok := record.Get(column)
	if !ok {
		return nil, fmt.Errorf("missing '%s' column in record", column)
	}
	if raw == nil {
		return nil, nil
	}
	rawSlice, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid '%s' returned: expected []interface{}, got %T", column, raw)
	}
	values := make([]string, 0, len(rawSlice))
	for _, item := range rawSlice {
		itemStr, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("invalid '%s' entry returned: expected string, got %T", column, item)
		}
		values = append(values, itemStr)
	}
	return values, nil
}
