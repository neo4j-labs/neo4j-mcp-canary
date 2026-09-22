// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"context"
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// indexEntity is the minimal catalog lookup vector-search/fulltext-search
// need to auto-build the right MATCH pattern from just an index name,
// without asking the caller to declare entityType/labels redundantly (the
// index already fixes them). YIELD before WHERE — the ordering this
// codebase already learned the hard way when internal/tools/cypher's
// create-constraint/create-index tools first shipped it the other way
// around and a live-server integration test caught the syntax error.
const lookupIndexEntityQuery = "SHOW INDEXES YIELD name, entityType, labelsOrTypes WHERE name = $name"

type indexEntity struct {
	EntityType    string
	LabelsOrTypes []string
}

func lookupIndexEntity(ctx context.Context, deps *tools.ToolDependencies, name string) (indexEntity, error) {
	records, err := deps.DBService.ExecuteReadQuery(ctx, lookupIndexEntityQuery, map[string]any{"name": name})
	if err != nil {
		return indexEntity{}, err
	}
	if len(records) == 0 {
		return indexEntity{}, fmt.Errorf("no index named %q was found", name)
	}
	return recordToIndexEntity(records[0])
}

func recordToIndexEntity(record *neo4j.Record) (indexEntity, error) {
	entityTypeRaw, ok := record.Get("entityType")
	if !ok {
		return indexEntity{}, fmt.Errorf("missing 'entityType' column in SHOW INDEXES record")
	}
	entityType, ok := entityTypeRaw.(string)
	if !ok {
		return indexEntity{}, fmt.Errorf("invalid 'entityType' type in SHOW INDEXES record")
	}

	labelsOrTypes, err := stringSlice(record, "labelsOrTypes")
	if err != nil {
		return indexEntity{}, err
	}

	return indexEntity{EntityType: entityType, LabelsOrTypes: labelsOrTypes}, nil
}

// showIndexByNameQuery fetches the full canonical row for a single index by
// name — used by the two create-*-index tools' fetch-back-after-create
// step, the same pattern internal/tools/cypher's create-constraint/
// create-index already established.
const showIndexByNameQuery = "SHOW INDEXES YIELD id, name, state, populationPercent, type, entityType, labelsOrTypes, properties, indexProvider, owningConstraint WHERE name = $name"

// IndexInfo mirrors internal/tools/cypher's identically-shaped SHOW-INDEXES
// row type, independently owned rather than imported: this package's
// output contract shouldn't silently drift if cypher's own SHOW INDEXES
// mapping ever changes for reasons unrelated to search.
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

func fetchIndexByName(ctx context.Context, deps *tools.ToolDependencies, name string) (IndexInfo, error) {
	records, err := deps.DBService.ExecuteReadQuery(ctx, showIndexByNameQuery, map[string]any{"name": name})
	if err != nil {
		return IndexInfo{}, err
	}
	if len(records) == 0 {
		return IndexInfo{}, fmt.Errorf("index %q was created but could not be found afterward", name)
	}
	return recordToIndexInfo(records[0])
}

func recordToIndexInfo(record *neo4j.Record) (IndexInfo, error) {
	id, err := int64Value(record, "id")
	if err != nil {
		return IndexInfo{}, err
	}
	name, err := stringValue(record, "name")
	if err != nil {
		return IndexInfo{}, err
	}
	state, err := stringValue(record, "state")
	if err != nil {
		return IndexInfo{}, err
	}
	populationPercent, err := float64Value(record, "populationPercent")
	if err != nil {
		return IndexInfo{}, err
	}
	indexType, err := stringValue(record, "type")
	if err != nil {
		return IndexInfo{}, err
	}
	entityType, err := stringValue(record, "entityType")
	if err != nil {
		return IndexInfo{}, err
	}
	labelsOrTypes, err := stringSlice(record, "labelsOrTypes")
	if err != nil {
		return IndexInfo{}, err
	}
	properties, err := stringSlice(record, "properties")
	if err != nil {
		return IndexInfo{}, err
	}

	return IndexInfo{
		ID:                id,
		Name:              name,
		State:             state,
		PopulationPercent: populationPercent,
		Type:              indexType,
		EntityType:        entityType,
		LabelsOrTypes:     labelsOrTypes,
		Properties:        properties,
		IndexProvider:     optionalStringValue(record, "indexProvider"),
		OwningConstraint:  optionalStringValue(record, "owningConstraint"),
	}, nil
}

// --- record-decoding helpers shared by both lookups above ---

func stringValue(record *neo4j.Record, key string) (string, error) {
	raw, ok := record.Get(key)
	if !ok {
		return "", fmt.Errorf("missing %q column in SHOW INDEXES record", key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("invalid %q type in SHOW INDEXES record", key)
	}
	return s, nil
}

// optionalStringValue treats a missing/nil/wrong-type column as "" rather
// than an error — indexProvider/owningConstraint are legitimately null for
// most rows (only constraint-backed indexes have an owningConstraint, for
// instance), matching the same nullable-column convention
// internal/tools/cypher's list-constraints-and-indexes already uses.
func optionalStringValue(record *neo4j.Record, key string) string {
	raw, ok := record.Get(key)
	if !ok || raw == nil {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return s
}

func int64Value(record *neo4j.Record, key string) (int64, error) {
	raw, ok := record.Get(key)
	if !ok {
		return 0, fmt.Errorf("missing %q column in SHOW INDEXES record", key)
	}
	v, ok := raw.(int64)
	if !ok {
		return 0, fmt.Errorf("invalid %q type in SHOW INDEXES record", key)
	}
	return v, nil
}

func float64Value(record *neo4j.Record, key string) (float64, error) {
	raw, ok := record.Get(key)
	if !ok {
		return 0, fmt.Errorf("missing %q column in SHOW INDEXES record", key)
	}
	v, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("invalid %q type in SHOW INDEXES record", key)
	}
	return v, nil
}

func stringSlice(record *neo4j.Record, key string) ([]string, error) {
	raw, ok := record.Get(key)
	if !ok {
		return nil, fmt.Errorf("missing %q column in SHOW INDEXES record", key)
	}
	rawSlice, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid %q type in SHOW INDEXES record", key)
	}
	out := make([]string, 0, len(rawSlice))
	for _, v := range rawSlice {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("invalid element type in %q column of SHOW INDEXES record", key)
		}
		out = append(out, s)
	}
	return out, nil
}
