// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// DefaultFallbackSampleSize is used when SchemaSampleSize is not configured.
const DefaultFallbackSampleSize = 1000

const (
	// nodePropertiesQuery retrieves property information for all node labels.
	// Uses the built-in db.schema.nodeTypeProperties() procedure (available since Neo4j 3.4).
	nodePropertiesQuery = `CALL db.schema.nodeTypeProperties() YIELD nodeLabels, propertyName, propertyTypes`

	// relPropertiesQuery retrieves property information for all relationship types.
	// Uses the built-in db.schema.relTypeProperties() procedure (available since Neo4j 3.4).
	relPropertiesQuery = `CALL db.schema.relTypeProperties() YIELD relType, propertyName, propertyTypes`

	// relPatternsQuery discovers which node labels are connected by which relationship types.
	// Uses UNWIND to handle multi-label nodes, producing one row per (fromLabel, relType, toLabel) combination.
	relPatternsQuery = `
		MATCH (a)-[r]->(b)
		UNWIND labels(a) AS fromLabel
		UNWIND labels(b) AS toLabel
		WITH DISTINCT fromLabel, type(r) AS relType, toLabel
		RETURN fromLabel, relType, toLabel`

	// indexesQuery retrieves all online indexes (including vector indexes).
	// Uses the SHOW INDEXES Cypher command (available since Neo4j 4.2).
	// Token lookup indexes are excluded as they are system-level indexes and not useful for query authoring.
	indexesQuery = `
		SHOW INDEXES
		YIELD name, type, entityType, labelsOrTypes, properties, state, options
		WHERE state = 'ONLINE' AND type <> 'LOOKUP'`

	// --- Sampling-based fallback queries (Spark-connector inspired) ---
	// These are used when the primary db.schema procedures time out on large graphs.
	// Instead of scanning all data via procedures, they sample a limited number of
	// records and infer the schema from actual property values using valueType() (Neo4j 5.x+).

	// sampleNodePropertiesQuery samples nodes and infers property names and types.
	sampleNodePropertiesQuery = `
		MATCH (n)
		WITH n LIMIT $sampleSize
		UNWIND labels(n) AS label
		UNWIND keys(n) AS key
		WITH label, key, valueType(n[key]) AS propType
		RETURN label, key, collect(DISTINCT propType) AS types
		ORDER BY label, key`

	// sampleRelPropertiesQuery samples relationships and infers property names and types.
	sampleRelPropertiesQuery = `
		MATCH ()-[r]->()
		WITH r LIMIT $sampleSize
		WITH type(r) AS relType, r
		UNWIND keys(r) AS key
		WITH relType, key, valueType(r[key]) AS propType
		RETURN relType, key, collect(DISTINCT propType) AS types
		ORDER BY relType, key`

	// sampleRelPatternsQuery samples relationships to discover (fromLabel, relType, toLabel) patterns.
	sampleRelPatternsQuery = `
		MATCH (a)-[r]->(b)
		WITH a, r, b LIMIT $sampleSize
		UNWIND labels(a) AS fromLabel
		UNWIND labels(b) AS toLabel
		WITH DISTINCT fromLabel, type(r) AS relType, toLabel
		RETURN fromLabel, relType, toLabel`
)

// --- Output types ---

// SchemaResponse is the structured output returned by the get-schema tool.
type SchemaResponse struct {
	Nodes         []NodeSchema         `json:"nodes,omitempty"`
	Relationships []RelationshipSchema `json:"relationships,omitempty"`
	Indexes       []IndexInfo          `json:"indexes,omitempty"`
}

// NodeSchema describes a single node label and its properties.
type NodeSchema struct {
	Label      string            `json:"label"`
	Properties map[string]string `json:"properties,omitempty"`
}

// RelationshipSchema describes a relationship pattern (from label -> to label) and its properties.
type RelationshipSchema struct {
	Type       string            `json:"type"`
	From       string            `json:"from,omitempty"`
	To         string            `json:"to,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// IndexInfo describes a database index, with additional fields for vector indexes.
type IndexInfo struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	EntityType         string   `json:"entityType"`
	LabelsOrTypes      []string `json:"labelsOrTypes"`
	Properties         []string `json:"properties"`
	Dimensions         *int64   `json:"dimensions,omitempty"`
	SimilarityFunction *string  `json:"similarityFunction,omitempty"`
}

// --- Handler ---

// GetSchemaHandler returns a handler function for the get-schema tool.
// The schemaSampleSize parameter is accepted for API compatibility but is no longer used;
// the built-in db.schema procedures handle sampling internally.
func GetSchemaHandler(deps *tools.ToolDependencies, schemaSampleSize int32) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetSchema(ctx, deps)
	}
}

// handleGetSchema retrieves Neo4j schema information using built-in Cypher procedures,
// removing the dependency on the APOC library.
//
// When SchemaTimeout is configured (> 0), the primary schema procedures
// (db.schema.nodeTypeProperties / relTypeProperties) are executed with a timeout.
// If they exceed the deadline, the handler falls back to a Spark-connector-inspired
// sampling approach that infers the schema from a limited number of records.
func handleGetSchema(ctx context.Context, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	if deps.DBService == nil {
		errMessage := "database service is not initialized"
		slog.Error(errMessage)
		return mcp.NewToolResultError(errMessage), nil
	}

	slog.Info("retrieving schema from the database")

	// Create a child context with a timeout for the primary schema queries.
	// If the timeout is 0 (disabled), use the parent context as-is.
	var schemaCtx context.Context
	var cancel context.CancelFunc
	if deps.SchemaTimeout > 0 {
		schemaCtx, cancel = context.WithTimeout(ctx, deps.SchemaTimeout)
		defer cancel()
	} else {
		schemaCtx = ctx
	}

	// Step 1: Fetch node properties via db.schema.nodeTypeProperties()
	nodeRecords, err := deps.DBService.ExecuteReadQuery(schemaCtx, nodePropertiesQuery, map[string]any{})
	if err != nil {
		if schemaCtx.Err() == context.DeadlineExceeded {
			slog.Warn("primary schema query timed out, falling back to sampling approach",
				"timeout", deps.SchemaTimeout, "phase", "nodeProperties")
			return handleGetSchemaFallback(ctx, deps)
		}
		slog.Error("failed to fetch node type properties", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve node schema: %s", err.Error())), nil
	}

	// Step 2: Fetch relationship properties via db.schema.relTypeProperties()
	relRecords, err := deps.DBService.ExecuteReadQuery(schemaCtx, relPropertiesQuery, map[string]any{})
	if err != nil {
		if schemaCtx.Err() == context.DeadlineExceeded {
			slog.Warn("primary schema query timed out, falling back to sampling approach",
				"timeout", deps.SchemaTimeout, "phase", "relProperties")
			return handleGetSchemaFallback(ctx, deps)
		}
		slog.Error("failed to fetch relationship type properties", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve relationship schema: %s", err.Error())), nil
	}

	// Empty database: both procedures return no records when the database has no data.
	if len(nodeRecords) == 0 && len(relRecords) == 0 {
		slog.Warn("schema is empty, no data in the database")
		return mcp.NewToolResultText("The get-schema tool executed successfully; however, since the Neo4j instance contains no data, no schema information was returned."), nil
	}

	// Step 3: Fetch relationship patterns (graceful degradation on failure)
	patternRecords, err := deps.DBService.ExecuteReadQuery(ctx, relPatternsQuery, map[string]any{})
	if err != nil {
		slog.Warn("failed to fetch relationship patterns, continuing without pattern information", "error", err)
		patternRecords = nil
	}

	// Step 4: Fetch indexes including vector indexes (graceful degradation on failure)
	indexRecords, err := deps.DBService.ExecuteReadQuery(ctx, indexesQuery, map[string]any{})
	if err != nil {
		slog.Warn("failed to fetch indexes, continuing without index information", "error", err)
		indexRecords = nil
	}

	// Step 5: Assemble the schema response
	response, err := buildSchemaResponse(nodeRecords, relRecords, patternRecords, indexRecords)
	if err != nil {
		slog.Error("failed to process schema", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	jsonData, err := json.Marshal(response)
	if err != nil {
		slog.Error("failed to serialize schema", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleGetSchemaFallback uses a Spark-connector-inspired sampling approach to infer
// the schema from a limited number of records. This avoids the full-scan behaviour of
// db.schema.nodeTypeProperties() / relTypeProperties() which can time out on large graphs.
// Property types are inferred using the valueType() function (Neo4j 5.x+).
func handleGetSchemaFallback(ctx context.Context, deps *tools.ToolDependencies) (*mcp.CallToolResult, error) {
	sampleSize := deps.SchemaSampleSize
	if sampleSize <= 0 {
		sampleSize = DefaultFallbackSampleSize
	}

	slog.Info("using sampling-based schema inference (fallback)", "sampleSize", sampleSize)

	// Emit analytics event to record the timeout fallback
	if deps.AnalyticsService != nil && deps.AnalyticsService.IsEnabled() {
		timeoutSeconds := deps.SchemaTimeout.Seconds()
		deps.AnalyticsService.EmitEvent(
			deps.AnalyticsService.NewSchemaTimeoutFallbackEvent(timeoutSeconds, sampleSize),
		)
	}

	params := map[string]any{"sampleSize": int64(sampleSize)}

	// Step 1: Sample node properties
	nodeRecords, err := deps.DBService.ExecuteReadQuery(ctx, sampleNodePropertiesQuery, params)
	if err != nil {
		slog.Error("failed to fetch sampled node properties", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve node schema via sampling: %s", err.Error())), nil
	}

	// Step 2: Sample relationship properties
	relRecords, err := deps.DBService.ExecuteReadQuery(ctx, sampleRelPropertiesQuery, params)
	if err != nil {
		slog.Error("failed to fetch sampled relationship properties", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve relationship schema via sampling: %s", err.Error())), nil
	}

	// Empty database
	if len(nodeRecords) == 0 && len(relRecords) == 0 {
		slog.Warn("schema is empty, no data in the database")
		return mcp.NewToolResultText("The get-schema tool executed successfully; however, since the Neo4j instance contains no data, no schema information was returned."), nil
	}

	// Step 3: Process sampled results into the same property map format
	nodeProps := processSampledNodeProperties(nodeRecords)
	relProps := processSampledRelProperties(relRecords)

	// Step 4: Sample relationship patterns (graceful degradation on failure)
	patternRecords, err := deps.DBService.ExecuteReadQuery(ctx, sampleRelPatternsQuery, params)
	if err != nil {
		slog.Warn("failed to fetch relationship patterns via sampling, continuing without", "error", err)
		patternRecords = nil
	}

	// Step 5: Fetch indexes — this is a fast metadata query, not a data scan
	indexRecords, err := deps.DBService.ExecuteReadQuery(ctx, indexesQuery, map[string]any{})
	if err != nil {
		slog.Warn("failed to fetch indexes, continuing without index information", "error", err)
		indexRecords = nil
	}

	// Step 6: Assemble using the same building functions as the primary path
	nodes := buildNodeSchemas(nodeProps)
	relationships := buildRelSchemas(relProps, patternRecords)
	indexes := processIndexes(indexRecords)

	response := &SchemaResponse{
		Nodes:         nodes,
		Relationships: relationships,
		Indexes:       indexes,
	}

	jsonData, err := json.Marshal(response)
	if err != nil {
		slog.Error("failed to serialize schema", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// --- Processing ---

// buildSchemaResponse assembles a complete SchemaResponse from the individual query results.
func buildSchemaResponse(nodeRecords, relRecords, patternRecords, indexRecords []*neo4j.Record) (*SchemaResponse, error) {
	// Process node properties: label -> propName -> type
	nodeProps, err := processNodeProperties(nodeRecords)
	if err != nil {
		return nil, fmt.Errorf("processing node properties: %w", err)
	}

	// Process relationship properties: relType -> propName -> type
	relProps, err := processRelProperties(relRecords)
	if err != nil {
		return nil, fmt.Errorf("processing relationship properties: %w", err)
	}

	// Build node schemas
	nodes := buildNodeSchemas(nodeProps)

	// Build relationship schemas from patterns + properties
	relationships := buildRelSchemas(relProps, patternRecords)

	// Process indexes
	indexes := processIndexes(indexRecords)

	return &SchemaResponse{
		Nodes:         nodes,
		Relationships: relationships,
		Indexes:       indexes,
	}, nil
}

// processNodeProperties extracts per-label property maps from db.schema.nodeTypeProperties() results.
// Each row has nodeLabels (a list of labels for that node type), propertyName, and propertyTypes.
// Properties are assigned to every label in the combination, so multi-label nodes contribute
// their properties to each individual label entry.
//
// Note: db.schema.nodeTypeProperties() may return rows with a null propertyName for labels
// that have no properties. In this case we register the label but skip adding any property.
func processNodeProperties(records []*neo4j.Record) (map[string]map[string]string, error) {
	nodeMap := make(map[string]map[string]string)

	for _, record := range records {
		nodeLabelsRaw, ok := record.Get("nodeLabels")
		if !ok {
			return nil, fmt.Errorf("missing 'nodeLabels' column")
		}
		propertyNameRaw, ok := record.Get("propertyName")
		if !ok {
			return nil, fmt.Errorf("missing 'propertyName' column")
		}
		propertyTypesRaw, ok := record.Get("propertyTypes")
		if !ok {
			return nil, fmt.Errorf("missing 'propertyTypes' column")
		}

		labels, ok := toStringSlice(nodeLabelsRaw)
		if !ok {
			return nil, fmt.Errorf("invalid nodeLabels: expected list of strings")
		}

		// Ensure every label is registered in the map, even if it has no properties.
		for _, label := range labels {
			if nodeMap[label] == nil {
				nodeMap[label] = make(map[string]string)
			}
		}

		// propertyName is null when a label has no properties — skip the property but
		// the label itself was already registered above.
		if propertyNameRaw == nil {
			continue
		}

		propName, ok := propertyNameRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid propertyName: expected string")
		}

		propType := normalizePropertyTypes(propertyTypesRaw)

		for _, label := range labels {
			nodeMap[label][propName] = propType
		}
	}

	return nodeMap, nil
}

// processRelProperties extracts per-type property maps from db.schema.relTypeProperties() results.
// The relType column comes formatted as ":`TYPE_NAME`" and is cleaned to extract the bare type name.
//
// Note: db.schema.relTypeProperties() returns rows with a null propertyName for relationship types
// that have no properties (e.g. DIRECTED, PRODUCED). In this case we register the type but skip
// adding any property.
func processRelProperties(records []*neo4j.Record) (map[string]map[string]string, error) {
	relMap := make(map[string]map[string]string)

	for _, record := range records {
		relTypeRaw, ok := record.Get("relType")
		if !ok {
			return nil, fmt.Errorf("missing 'relType' column")
		}
		propertyNameRaw, ok := record.Get("propertyName")
		if !ok {
			return nil, fmt.Errorf("missing 'propertyName' column")
		}
		propertyTypesRaw, ok := record.Get("propertyTypes")
		if !ok {
			return nil, fmt.Errorf("missing 'propertyTypes' column")
		}

		relTypeStr, ok := relTypeRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid relType: expected string")
		}
		relType := cleanRelType(relTypeStr)

		// Ensure every relationship type is registered in the map, even if it has no properties.
		if relMap[relType] == nil {
			relMap[relType] = make(map[string]string)
		}

		// propertyName is null when a relationship type has no properties — skip the property
		// but the type itself was already registered above.
		if propertyNameRaw == nil {
			continue
		}

		propName, ok := propertyNameRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid propertyName: expected string")
		}

		propType := normalizePropertyTypes(propertyTypesRaw)

		relMap[relType][propName] = propType
	}

	return relMap, nil
}

// buildNodeSchemas creates a sorted list of NodeSchema from the per-label property map.
// Labels matching internal prefixes (e.g. _Bloom_) are excluded.
func buildNodeSchemas(nodeProps map[string]map[string]string) []NodeSchema {
	nodes := make([]NodeSchema, 0, len(nodeProps))
	for label, props := range nodeProps {
		if isInternalLabel(label) {
			continue
		}
		var properties map[string]string
		if len(props) > 0 {
			properties = props
		}
		nodes = append(nodes, NodeSchema{
			Label:      label,
			Properties: properties,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Label < nodes[j].Label
	})
	return nodes
}

// buildRelSchemas creates a sorted list of RelationshipSchema by combining relationship patterns
// (which tell us from -> type -> to) with relationship properties (which tell us the property types).
func buildRelSchemas(relProps map[string]map[string]string, patternRecords []*neo4j.Record) []RelationshipSchema {
	type patternKey struct {
		from    string
		relType string
		to      string
	}

	seen := make(map[patternKey]bool)
	rels := make([]RelationshipSchema, 0)
	seenTypes := make(map[string]bool)

	// Build from relationship patterns
	for _, record := range patternRecords {
		fromRaw, ok1 := record.Get("fromLabel")
		relRaw, ok2 := record.Get("relType")
		toRaw, ok3 := record.Get("toLabel")
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		from, ok1 := fromRaw.(string)
		relType, ok2 := relRaw.(string)
		to, ok3 := toRaw.(string)
		if !ok1 || !ok2 || !ok3 {
			continue
		}

		// Skip patterns involving internal labels or relationship types
		if isInternalLabel(from) || isInternalLabel(to) || isInternalLabel(relType) {
			continue
		}

		key := patternKey{from: from, relType: relType, to: to}
		if seen[key] {
			continue
		}
		seen[key] = true
		seenTypes[relType] = true

		var props map[string]string
		if p, ok := relProps[relType]; ok && len(p) > 0 {
			props = p
		}

		rels = append(rels, RelationshipSchema{
			Type:       relType,
			From:       from,
			To:         to,
			Properties: props,
		})
	}

	// Include relationship types that have properties but weren't found in patterns.
	// This handles the edge case where the patterns query returned nil (graceful degradation)
	// but relationship properties were still discovered.
	for relType, props := range relProps {
		if seenTypes[relType] || isInternalLabel(relType) {
			continue
		}
		var properties map[string]string
		if len(props) > 0 {
			properties = props
		}
		rels = append(rels, RelationshipSchema{
			Type:       relType,
			Properties: properties,
		})
	}

	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Type != rels[j].Type {
			return rels[i].Type < rels[j].Type
		}
		if rels[i].From != rels[j].From {
			return rels[i].From < rels[j].From
		}
		return rels[i].To < rels[j].To
	})

	return rels
}

// processIndexes extracts index information from SHOW INDEXES results.
// For vector indexes, additional configuration (dimensions, similarity function) is extracted
// from the options map.
func processIndexes(records []*neo4j.Record) []IndexInfo {
	if len(records) == 0 {
		return nil
	}

	indexes := make([]IndexInfo, 0, len(records))

	for _, record := range records {
		nameRaw, ok := record.Get("name")
		if !ok {
			continue
		}
		typeRaw, ok := record.Get("type")
		if !ok {
			continue
		}
		entityTypeRaw, ok := record.Get("entityType")
		if !ok {
			continue
		}
		labelsOrTypesRaw, ok := record.Get("labelsOrTypes")
		if !ok {
			continue
		}
		propertiesRaw, ok := record.Get("properties")
		if !ok {
			continue
		}

		name, _ := nameRaw.(string)
		indexType, _ := typeRaw.(string)
		entityType, _ := entityTypeRaw.(string)
		labelsOrTypes, _ := toStringSlice(labelsOrTypesRaw)
		properties, _ := toStringSlice(propertiesRaw)

		// Skip indexes that exclusively reference internal labels/types
		if allInternal(labelsOrTypes) {
			continue
		}

		info := IndexInfo{
			Name:          name,
			Type:          indexType,
			EntityType:    entityType,
			LabelsOrTypes: labelsOrTypes,
			Properties:    properties,
		}

		// Extract vector-specific configuration from the options map
		if indexType == "VECTOR" {
			if optionsRaw, ok := record.Get("options"); ok {
				extractVectorConfig(&info, optionsRaw)
			}
		}

		indexes = append(indexes, info)
	}

	sort.Slice(indexes, func(i, j int) bool {
		return indexes[i].Name < indexes[j].Name
	})

	return indexes
}

// extractVectorConfig extracts dimensions and similarity function from a vector index's options map.
// The options structure is: {"indexConfig": {"vector.dimensions": N, "vector.similarity_function": "..."}}
func extractVectorConfig(info *IndexInfo, optionsRaw any) {
	options, ok := optionsRaw.(map[string]any)
	if !ok {
		return
	}
	indexConfig, ok := options["indexConfig"].(map[string]any)
	if !ok {
		return
	}
	if dims, ok := indexConfig["vector.dimensions"]; ok {
		if dimsInt, ok := toInt64(dims); ok {
			info.Dimensions = &dimsInt
		}
	}
	if simFunc, ok := indexConfig["vector.similarity_function"].(string); ok {
		info.SimilarityFunction = &simFunc
	}
}

// --- Sampling processors ---

// processSampledNodeProperties builds the label → property → type map from sampling query results.
// Each record has columns: label (string), key (string), types (list of strings from valueType()).
func processSampledNodeProperties(records []*neo4j.Record) map[string]map[string]string {
	nodeMap := make(map[string]map[string]string)

	for _, record := range records {
		labelRaw, ok := record.Get("label")
		if !ok {
			continue
		}
		keyRaw, ok := record.Get("key")
		if !ok {
			continue
		}
		typesRaw, ok := record.Get("types")
		if !ok {
			continue
		}

		label, ok := labelRaw.(string)
		if !ok {
			continue
		}
		key, ok := keyRaw.(string)
		if !ok {
			continue
		}

		if nodeMap[label] == nil {
			nodeMap[label] = make(map[string]string)
		}

		nodeMap[label][key] = normalizeValueTypes(typesRaw)
	}

	return nodeMap
}

// processSampledRelProperties builds the relType → property → type map from sampling query results.
// Each record has columns: relType (string), key (string), types (list of strings from valueType()).
func processSampledRelProperties(records []*neo4j.Record) map[string]map[string]string {
	relMap := make(map[string]map[string]string)

	for _, record := range records {
		relTypeRaw, ok := record.Get("relType")
		if !ok {
			continue
		}
		keyRaw, ok := record.Get("key")
		if !ok {
			continue
		}
		typesRaw, ok := record.Get("types")
		if !ok {
			continue
		}

		relType, ok := relTypeRaw.(string)
		if !ok {
			continue
		}
		key, ok := keyRaw.(string)
		if !ok {
			continue
		}

		if relMap[relType] == nil {
			relMap[relType] = make(map[string]string)
		}

		relMap[relType][key] = normalizeValueTypes(typesRaw)
	}

	return relMap
}

// normalizeValueTypes converts a list of valueType() strings into the same format
// used by the primary schema path. When multiple types are observed, they are joined with " | ".
func normalizeValueTypes(raw any) string {
	types, ok := toStringSlice(raw)
	if !ok || len(types) == 0 {
		return "ANY"
	}

	normalized := make([]string, 0, len(types))
	for _, t := range types {
		normalized = append(normalized, normalizeValueType(t))
	}

	if len(normalized) == 1 {
		return normalized[0]
	}

	sort.Strings(normalized)
	return strings.Join(normalized, " | ")
}

// normalizeValueType maps valueType() output to the format used by the primary schema path.
// valueType() returns standardized Cypher type names (Neo4j 5.x+) such as "STRING", "INTEGER",
// "ZONED DATETIME", "LIST<FLOAT>", etc. Most pass through unchanged; only the temporal types
// with spaces need mapping to the underscore-separated format.
func normalizeValueType(t string) string {
	switch t {
	case "ZONED DATETIME":
		return "DATE_TIME"
	case "LOCAL DATETIME":
		return "LOCAL_DATE_TIME"
	case "LOCAL TIME":
		return "LOCAL_TIME"
	case "ZONED TIME":
		return "ZONED_TIME"
	default:
		return t
	}
}

// --- Filtering ---

// internalLabelPrefixes lists prefixes used by Neo4j tooling for internal metadata.
// Labels and relationship types matching these prefixes are excluded from schema output
// because they represent tool-specific state (e.g. Bloom perspectives and scenes)
// that has no value for LLM-driven query authoring.
var internalLabelPrefixes = []string{
	"_Bloom_",
}

// isInternalLabel returns true if the label or relationship type is internal metadata
// that should be excluded from the schema output.
func isInternalLabel(name string) bool {
	for _, prefix := range internalLabelPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// allInternal returns true if every entry in the slice is an internal label.
// Returns false for empty slices (an index with no labels should not be silently dropped).
func allInternal(names []string) bool {
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if !isInternalLabel(name) {
			return false
		}
	}
	return true
}

// --- Helpers ---

// cleanRelType strips the ":`...`" formatting from relationship types
// as returned by db.schema.relTypeProperties().
// Example: ":`ACTED_IN`" -> "ACTED_IN"
func cleanRelType(relType string) string {
	s := strings.TrimPrefix(relType, ":`")
	s = strings.TrimSuffix(s, "`")
	return s
}

// normalizePropertyTypes converts the propertyTypes list from db.schema procedures
// into a single string representation. When a property has multiple observed types
// (heterogeneous data), they are joined with " | ".
func normalizePropertyTypes(raw any) string {
	types, ok := toStringSlice(raw)
	if !ok || len(types) == 0 {
		return "ANY"
	}

	normalized := make([]string, 0, len(types))
	for _, t := range types {
		normalized = append(normalized, normalizeType(t))
	}

	if len(normalized) == 1 {
		return normalized[0]
	}
	return strings.Join(normalized, " | ")
}

// normalizeType maps type names from db.schema procedures to consistent uppercase names.
// This provides more granularity than the previous APOC-based approach, which reported
// all list types as just "LIST". The new format distinguishes LIST<STRING> from LIST<FLOAT>,
// which is particularly useful for identifying vector embedding properties.
func normalizeType(t string) string {
	switch t {
	case "String":
		return "STRING"
	case "Long":
		return "INTEGER"
	case "Double", "Float":
		return "FLOAT"
	case "Boolean":
		return "BOOLEAN"
	case "Date":
		return "DATE"
	case "DateTime":
		return "DATE_TIME"
	case "LocalDateTime":
		return "LOCAL_DATE_TIME"
	case "LocalTime":
		return "LOCAL_TIME"
	case "Time":
		return "ZONED_TIME"
	case "Point":
		return "POINT"
	case "Duration":
		return "DURATION"
	case "StringArray":
		return "LIST<STRING>"
	case "LongArray":
		return "LIST<INTEGER>"
	case "DoubleArray", "FloatArray":
		return "LIST<FLOAT>"
	case "BooleanArray":
		return "LIST<BOOLEAN>"
	case "DateArray":
		return "LIST<DATE>"
	case "PointArray":
		return "LIST<POINT>"
	default:
		// Pass through unknown types in uppercase (e.g., "VECTOR" for the native vector type)
		return strings.ToUpper(t)
	}
}

// toStringSlice converts an any value (expected to be []any of strings) to []string.
func toStringSlice(raw any) ([]string, bool) {
	slice, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, s)
	}
	return result, true
}

// toInt64 converts a numeric any value to int64.
// Handles int64, int, and float64 (which is common in JSON-decoded maps).
func toInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}
