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
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// DefaultFallbackSampleSize is used when SchemaSampleSize is not configured.
// This value is interpreted as the per-label / per-relationship-type budget,
// not a database-wide total — total work scales with the number of distinct
// labels and relationship types in the graph, not with the number of records.
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

	// --- Sampling-based fallback queries ---
	//
	// These are used when the primary db.schema procedures time out on large graphs.
	// Unlike a plain `MATCH (n) LIMIT k` sample — which is biased by storage order
	// and will almost exclusively return the dominant label on skewed graphs — these
	// queries use `db.labels()` / `db.relationshipTypes()` to enumerate every label
	// and relationship type and then take a bounded per-entity sample via a CALL
	// subquery. Every label and relationship type therefore gets its own budget,
	// so rare entities are represented even when one entity dominates by volume.
	//
	// The $sampleSize parameter is the per-label / per-type budget. Total work is
	// bounded by sampleSize × number-of-labels (for node properties) and
	// sampleSize × number-of-relationship-types (for relationship properties and
	// patterns), so it scales with schema breadth rather than data volume.
	//
	// Known limitation: a label or relationship type whose sampled records happen
	// to have no properties will not appear in the output (the inner UNWIND yields
	// zero rows). The completeness heuristic in populateMetadataHeuristics catches
	// this case when an index references the missing label/type.
	//
	// Property types are inferred using valueType() (Neo4j 5.x+). The CALL { WITH var }
	// importing form is used for compatibility with Neo4j 5.0–5.22; the newer
	// CALL (var) { ... } scope syntax (5.23+) would be a drop-in replacement.

	// sampleNodePropertiesQuery samples up to $sampleSize nodes per label and
	// infers property names and types from the sampled nodes.
	sampleNodePropertiesQuery = `
		CALL db.labels() YIELD label
		CALL {
			WITH label
			MATCH (n) WHERE label IN labels(n)
			WITH n LIMIT $sampleSize
			UNWIND keys(n) AS key
			WITH key, valueType(n[key]) AS propType
			RETURN key, collect(DISTINCT propType) AS types
		}
		RETURN label, key, types
		ORDER BY label, key`

	// sampleRelPropertiesQuery samples up to $sampleSize relationships per type
	// and infers property names and types from the sampled relationships.
	sampleRelPropertiesQuery = `
		CALL db.relationshipTypes() YIELD relationshipType
		CALL {
			WITH relationshipType
			MATCH ()-[r]->() WHERE type(r) = relationshipType
			WITH r LIMIT $sampleSize
			UNWIND keys(r) AS key
			WITH key, valueType(r[key]) AS propType
			RETURN key, collect(DISTINCT propType) AS types
		}
		RETURN relationshipType AS relType, key, types
		ORDER BY relType, key`

	// sampleRelPatternsQuery samples up to $sampleSize relationships per type
	// and discovers (fromLabel, relType, toLabel) patterns from each sample.
	sampleRelPatternsQuery = `
		CALL db.relationshipTypes() YIELD relationshipType
		CALL {
			WITH relationshipType
			MATCH (a)-[r]->(b) WHERE type(r) = relationshipType
			WITH a, b LIMIT $sampleSize
			UNWIND labels(a) AS fromLabel
			UNWIND labels(b) AS toLabel
			RETURN fromLabel, toLabel
		}
		RETURN DISTINCT fromLabel, relationshipType AS relType, toLabel
		ORDER BY fromLabel, relType, toLabel`
)

// --- Output types ---

// SchemaResponse is the structured output returned by the get-schema tool.
type SchemaResponse struct {
	Nodes         []NodeSchema         `json:"nodes,omitempty"`
	Relationships []RelationshipSchema `json:"relationships,omitempty"`
	Indexes       []IndexInfo          `json:"indexes,omitempty"`
	Metadata      *SchemaMetadata      `json:"metadata,omitempty"`
}

// NodeSchema describes a single node label and its properties.
type NodeSchema struct {
	Label              string            `json:"label"`
	Properties         map[string]string `json:"properties,omitempty"`
	RequiredProperties []string          `json:"requiredProperties,omitempty"`
}

// RelationshipSchema describes a relationship pattern (from label -> to label) and its properties.
type RelationshipSchema struct {
	Type               string            `json:"type"`
	From               string            `json:"from,omitempty"`
	To                 string            `json:"to,omitempty"`
	Properties         map[string]string `json:"properties,omitempty"`
	RequiredProperties []string          `json:"requiredProperties,omitempty"`
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

// Schema source values for SchemaMetadata.Source.
const (
	// SchemaSourceFullScan indicates the schema was retrieved via the built-in
	// db.schema.nodeTypeProperties() and db.schema.relTypeProperties() procedures,
	// which cover all data in the database.
	SchemaSourceFullScan = "full_scan"

	// SchemaSourceSampled indicates the schema was inferred from a bounded sample
	// of records after the primary db.schema procedures exceeded the configured timeout.
	// Results may be incomplete — rare labels, relationship types, or properties
	// may be absent from the response.
	SchemaSourceSampled = "sampled"
)

// SchemaMetadata describes the provenance of a SchemaResponse so that consumers
// (in particular LLM agents) can weight the schema appropriately when writing
// Cypher queries or reasoning about the data model.
type SchemaMetadata struct {
	// Source is how the schema was retrieved: SchemaSourceFullScan or SchemaSourceSampled.
	Source string `json:"source"`

	// SampleSize is the number of records examined when Source is "sampled".
	// Omitted when Source is "full_scan".
	SampleSize int `json:"sampleSize,omitempty"`

	// TimeoutSeconds is the configured timeout that was exceeded, triggering the
	// fallback sampling path. Omitted when Source is "full_scan".
	TimeoutSeconds float64 `json:"timeoutSeconds,omitempty"`

	// MissingNodeLabels lists node labels that appear in the indexes array but
	// are absent from the nodes array. Because indexes come from database metadata
	// (SHOW INDEXES) rather than data sampling, they are always complete — so any
	// discrepancy here is a strong signal that the main schema retrieval returned
	// an incomplete picture, even when Source is "full_scan".
	MissingNodeLabels []string `json:"missingNodeLabels,omitempty"`

	// MissingRelTypes lists relationship types that appear in the indexes array
	// but are absent from the relationships array. See MissingNodeLabels.
	MissingRelTypes []string `json:"missingRelTypes,omitempty"`

	// Note is a human/LLM-readable description of any caveats about the schema.
	// Populated when the retrieval was sampled, when a completeness heuristic
	// fired, or both. Absent when the schema was retrieved via a full scan with
	// no detected discrepancies.
	Note string `json:"note,omitempty"`
}

// newFullScanMetadata returns metadata indicating the primary schema path succeeded.
// The Note and Missing* fields are populated later by populateMetadataHeuristics
// once the full response (including indexes) is available.
func newFullScanMetadata() *SchemaMetadata {
	return &SchemaMetadata{Source: SchemaSourceFullScan}
}

// newSampledMetadata returns metadata indicating the sampling fallback was used.
// The Note and Missing* fields are populated later by populateMetadataHeuristics
// once the full response (including indexes) is available.
func newSampledMetadata(sampleSize int, timeout time.Duration) *SchemaMetadata {
	return &SchemaMetadata{
		Source:         SchemaSourceSampled,
		SampleSize:     sampleSize,
		TimeoutSeconds: timeout.Seconds(),
	}
}

// populateMetadataHeuristics runs quality checks on a fully-assembled SchemaResponse
// and populates the Metadata.MissingNodeLabels, MissingRelTypes, and Note fields.
//
// It compares labels and relationship types referenced by the indexes array
// against those present in the nodes and relationships arrays. Any label or
// type that appears in indexes (database metadata, always complete) but not in
// the main arrays is recorded as "missing" — a signal that the agent should
// cross-check by querying those labels/types directly.
//
// The Note is composed dynamically so that both the retrieval path (sampled vs
// full-scan) and the heuristic outcome are reflected together. For a clean
// full-scan with no discrepancies the Note is left empty.
func populateMetadataHeuristics(response *SchemaResponse) {
	if response == nil || response.Metadata == nil {
		return
	}
	detectMissingEntities(response)
	buildMetadataNote(response)
}

// detectMissingEntities populates MissingNodeLabels and MissingRelTypes on
// response.Metadata by comparing the indexes array against the nodes and
// relationships arrays. Internal labels (e.g. _Bloom_*) are excluded because
// they are already filtered out of the main schema.
func detectMissingEntities(response *SchemaResponse) {
	meta := response.Metadata

	presentLabels := make(map[string]struct{}, len(response.Nodes))
	for _, n := range response.Nodes {
		presentLabels[n.Label] = struct{}{}
	}
	presentRelTypes := make(map[string]struct{}, len(response.Relationships))
	for _, r := range response.Relationships {
		presentRelTypes[r.Type] = struct{}{}
	}

	indexedLabels := make(map[string]struct{})
	indexedRelTypes := make(map[string]struct{})
	for _, idx := range response.Indexes {
		for _, lt := range idx.LabelsOrTypes {
			if isInternalLabel(lt) {
				continue
			}
			switch idx.EntityType {
			case "NODE":
				indexedLabels[lt] = struct{}{}
			case "RELATIONSHIP":
				indexedRelTypes[lt] = struct{}{}
			}
		}
	}

	var missingLabels []string
	for l := range indexedLabels {
		if _, ok := presentLabels[l]; !ok {
			missingLabels = append(missingLabels, l)
		}
	}
	var missingRelTypes []string
	for r := range indexedRelTypes {
		if _, ok := presentRelTypes[r]; !ok {
			missingRelTypes = append(missingRelTypes, r)
		}
	}
	sort.Strings(missingLabels)
	sort.Strings(missingRelTypes)

	meta.MissingNodeLabels = missingLabels
	meta.MissingRelTypes = missingRelTypes
}

// buildMetadataNote composes the Note field from the source of the schema and
// any incompleteness detected by detectMissingEntities. A clean full scan
// produces an empty Note so the agent can trust the response without friction.
func buildMetadataNote(response *SchemaResponse) {
	meta := response.Metadata
	hasMissing := len(meta.MissingNodeLabels) > 0 || len(meta.MissingRelTypes) > 0

	var parts []string

	if meta.Source == SchemaSourceSampled {
		parts = append(parts, fmt.Sprintf(
			"Schema was inferred from a sample of %d records after the full-scan schema query "+
				"exceeded the %.1fs timeout. Rare labels, relationship types, or properties may be "+
				"missing from the nodes and relationships arrays.",
			meta.SampleSize, meta.TimeoutSeconds))
	}

	if hasMissing {
		bits := []string{
			"The indexes array references labels or relationship types that are absent from " +
				"the main schema arrays, so the retrieval returned an incomplete picture of the database.",
		}
		if len(meta.MissingNodeLabels) > 0 {
			bits = append(bits, fmt.Sprintf(
				"Node labels present in indexes but missing from the nodes array: %s.",
				strings.Join(meta.MissingNodeLabels, ", ")))
		}
		if len(meta.MissingRelTypes) > 0 {
			bits = append(bits, fmt.Sprintf(
				"Relationship types present in indexes but missing from the relationships array: %s.",
				strings.Join(meta.MissingRelTypes, ", ")))
		}
		bits = append(bits,
			"Query these directly (for example MATCH (n:<Label>) RETURN n LIMIT 1) to discover "+
				"their properties, and treat already-listed entities with mild caution because "+
				"their properties may be similarly incomplete.")
		parts = append(parts, strings.Join(bits, " "))
	} else if meta.Source == SchemaSourceSampled {
		parts = append(parts,
			"The indexes array is complete (sourced from database metadata, not data sampling) "+
				"and should be used to cross-check which labels and relationship types exist in the database.")
	}

	meta.Note = strings.Join(parts, " ")
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
//
// The returned SchemaResponse always carries a Metadata field indicating which
// path was used (full_scan vs sampled). After the response is assembled a
// completeness heuristic compares the indexes array (from SHOW INDEXES, always
// complete) against the nodes and relationships arrays; any label or relationship
// type present in indexes but absent from the main arrays is recorded in
// Metadata.MissingNodeLabels / MissingRelTypes and summarised in Metadata.Note.
// This catches the case where the primary db.schema procedures silently return
// an incomplete result on large or unusual graphs even without a timeout.
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
	response.Metadata = newFullScanMetadata()
	populateMetadataHeuristics(response)

	jsonData, err := json.Marshal(response)
	if err != nil {
		slog.Error("failed to serialize schema", "error", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleGetSchemaFallback uses a per-label / per-relationship-type sampling
// approach to infer the schema from a bounded number of records when the
// primary db.schema procedures would otherwise time out on large graphs.
//
// For each label returned by db.labels() — and each type returned by
// db.relationshipTypes() — a CALL subquery samples up to SchemaSampleSize
// records and infers property types via valueType() (Neo4j 5.x+). This avoids
// the storage-order bias of a plain `MATCH (n) LIMIT k` sample, which on a
// graph dominated by one label would almost never surface the rarer ones.
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
		Metadata:      newSampledMetadata(sampleSize, deps.SchemaTimeout),
	}
	populateMetadataHeuristics(response)

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
func processNodeProperties(records []*neo4j.Record) (map[string]map[string]propMeta, error) {
	nodeMap := make(map[string]map[string]propMeta)

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
				nodeMap[label] = make(map[string]propMeta)
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

		info := propMeta{
			Type:     normalizePropertyTypes(propertyTypesRaw),
			Required: allNotNull(propertyTypesRaw),
		}

		for _, label := range labels {
			nodeMap[label][propName] = info
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
func processRelProperties(records []*neo4j.Record) (map[string]map[string]propMeta, error) {
	relMap := make(map[string]map[string]propMeta)

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
			relMap[relType] = make(map[string]propMeta)
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

		relMap[relType][propName] = propMeta{
			Type:     normalizePropertyTypes(propertyTypesRaw),
			Required: allNotNull(propertyTypesRaw),
		}
	}

	return relMap, nil
}

// splitPropMeta converts the internal propMeta map into the two public-facing
// fields on a schema entry: a name→type map for Properties and a sorted list
// of required property names. Returns (nil, nil) for an empty input so callers
// can rely on omitempty behaviour.
func splitPropMeta(m map[string]propMeta) (map[string]string, []string) {
	if len(m) == 0 {
		return nil, nil
	}
	properties := make(map[string]string, len(m))
	var required []string
	for name, info := range m {
		properties[name] = info.Type
		if info.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return properties, required
}

// buildNodeSchemas creates a sorted list of NodeSchema from the per-label property map.
// Labels matching internal prefixes (e.g. _Bloom_) are excluded.
func buildNodeSchemas(nodeProps map[string]map[string]propMeta) []NodeSchema {
	nodes := make([]NodeSchema, 0, len(nodeProps))
	for label, props := range nodeProps {
		if isInternalLabel(label) {
			continue
		}
		properties, required := splitPropMeta(props)
		nodes = append(nodes, NodeSchema{
			Label:              label,
			Properties:         properties,
			RequiredProperties: required,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Label < nodes[j].Label
	})
	return nodes
}

// buildRelSchemas creates a sorted list of RelationshipSchema by combining relationship patterns
// (which tell us from -> type -> to) with relationship properties (which tell us the property types).
func buildRelSchemas(relProps map[string]map[string]propMeta, patternRecords []*neo4j.Record) []RelationshipSchema {
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

		properties, required := splitPropMeta(relProps[relType])

		rels = append(rels, RelationshipSchema{
			Type:               relType,
			From:               from,
			To:                 to,
			Properties:         properties,
			RequiredProperties: required,
		})
	}

	// Include relationship types that have properties but weren't found in patterns.
	// This handles the edge case where the patterns query returned nil (graceful degradation)
	// but relationship properties were still discovered.
	for relType, props := range relProps {
		if seenTypes[relType] || isInternalLabel(relType) {
			continue
		}
		properties, required := splitPropMeta(props)
		rels = append(rels, RelationshipSchema{
			Type:               relType,
			Properties:         properties,
			RequiredProperties: required,
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

// processSampledNodeProperties builds the label → property → propMeta map from sampling query results.
// Each record has columns: label (string), key (string), types (list of strings from valueType()).
func processSampledNodeProperties(records []*neo4j.Record) map[string]map[string]propMeta {
	nodeMap := make(map[string]map[string]propMeta)

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
			nodeMap[label] = make(map[string]propMeta)
		}

		nodeMap[label][key] = propMeta{
			Type:     normalizeValueTypes(typesRaw),
			Required: allNotNull(typesRaw),
		}
	}

	return nodeMap
}

// processSampledRelProperties builds the relType → property → propMeta map from sampling query results.
// Each record has columns: relType (string), key (string), types (list of strings from valueType()).
func processSampledRelProperties(records []*neo4j.Record) map[string]map[string]propMeta {
	relMap := make(map[string]map[string]propMeta)

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
			relMap[relType] = make(map[string]propMeta)
		}

		relMap[relType][key] = propMeta{
			Type:     normalizeValueTypes(typesRaw),
			Required: allNotNull(typesRaw),
		}
	}

	return relMap
}

// normalizeValueTypes converts a list of valueType() strings into the same format
// used by the primary schema path. When multiple types are observed, they are joined with " | ".
// Duplicate entries that arise after stripping NOT NULL are collapsed; output is sorted for
// deterministic comparison across platforms.
func normalizeValueTypes(raw any) string {
	types, ok := toStringSlice(raw)
	if !ok || len(types) == 0 {
		return "ANY"
	}

	seen := make(map[string]struct{}, len(types))
	normalized := make([]string, 0, len(types))
	for _, t := range types {
		n := normalizeValueType(t)
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
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
//
// Any "NOT NULL" existence-constraint suffix (including nested ones inside LIST<...>) is
// stripped so that types are reported consistently. Required-ness is surfaced via the
// schema's RequiredProperties field rather than baked into the type string.
func normalizeValueType(t string) string {
	t = stripNotNull(t)
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

// propMeta is the internal per-property value used by the processing pipeline.
// It carries both the type string and whether every observation of the property
// came with a NOT NULL existence constraint. The public schema splits these
// back into Properties (type map) and RequiredProperties (list of names).
type propMeta struct {
	Type     string
	Required bool
}

// stripNotNull removes every occurrence of " NOT NULL" from a type string so
// that types are consistent regardless of existence-constraint status. It
// handles both outer ("STRING NOT NULL") and nested ("LIST<STRING NOT NULL> NOT NULL")
// positions, which both Neo4j's db.schema procedures and valueType() can emit.
func stripNotNull(t string) string {
	return strings.ReplaceAll(t, " NOT NULL", "")
}

// allNotNull reports whether every entry in the raw propertyTypes list carries
// the NOT NULL existence-constraint suffix. A property is treated as required
// only when every observation of it was constrained — a single nullable
// observation means the property is genuinely nullable somewhere in the data.
func allNotNull(raw any) bool {
	types, ok := toStringSlice(raw)
	if !ok || len(types) == 0 {
		return false
	}
	for _, t := range types {
		if !strings.HasSuffix(t, " NOT NULL") {
			return false
		}
	}
	return true
}

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
// (heterogeneous data), they are joined with " | ". Duplicate entries — which can
// arise after stripping NOT NULL suffixes (e.g. "String NOT NULL" and "String" both
// normalise to "STRING") — are collapsed.
func normalizePropertyTypes(raw any) string {
	types, ok := toStringSlice(raw)
	if !ok || len(types) == 0 {
		return "ANY"
	}

	seen := make(map[string]struct{}, len(types))
	normalized := make([]string, 0, len(types))
	for _, t := range types {
		n := normalizeType(t)
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
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
//
// Any "NOT NULL" existence-constraint suffix is stripped so that the type is reported
// consistently regardless of constraint status. Required-ness is surfaced separately
// via NodeSchema.RequiredProperties / RelationshipSchema.RequiredProperties.
func normalizeType(t string) string {
	t = stripNotNull(t)
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
