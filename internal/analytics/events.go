// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package analytics

import (
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"

	"github.com/google/uuid"
)

const eventNamePrefix = "MCP-NEO4J-CANARY"

// baseProperties are the base properties attached to a MixPanel "track" event.
// DistinctID is a distinct ID used to identify unique users, we do not use this information, therefore for us it will be distinct different executions.
// InsertID is used to deduplicate duplicate messages.
type baseProperties struct {
	Token      string `json:"token"`
	Time       int64  `json:"time"`
	DistinctID string `json:"distinct_id"`
	InsertID   string `json:"$insert_id"`
	Uptime     int64  `json:"uptime"`
	OS         string `json:"$os"`
	OSArch     string `json:"os_arch"`
	IsAura     bool   `json:"isAura"`
	IP         string `json:"$ip,omitempty"`
	MachineID  string `json:"machine_id,omitempty"`
	BinaryPath string `json:"binary_path,omitempty"`
}

// serverStartupProperties contains server-level information available at startup (no DB query required)
type serverStartupProperties struct {
	baseProperties
	McpVersion    string               `json:"mcp_version"`
	TransportMode config.TransportMode `json:"transport_mode"`
	TLSEnabled    *bool                `json:"tls_enabled,omitempty"` // Only for HTTP mode, pointer allows explicit false
}

// connectionInitializedProperties contains Neo4j-specific information (requires DB query)
type connectionInitializedProperties struct {
	baseProperties
	Neo4jVersion  string   `json:"neo4j_version"`
	Edition       string   `json:"edition"`
	CypherVersion []string `json:"cypher_version"`
}

// unauthenticatedJSONRPCProperties contains the JSON-RPC request method.
type unauthenticatedJSONRPCProperties struct {
	baseProperties
	JSONRPCMethod string `json:"method"`
}

// ToolVectorInfo carries optional vector and index-related properties for tool events.
// When nil, no vector properties are included in the event.
type ToolVectorInfo struct {
	// VectorIndexCount is the number of VECTOR indexes detected in the get-schema response.
	VectorIndexCount *int `json:"vectorIndex,omitempty"`
	// FullTextIndexCount is the number of FULLTEXT indexes detected in the get-schema response.
	FullTextIndexCount *int `json:"fullTextIndex,omitempty"`
	// VectorSearch indicates whether the Cypher query uses vector index search
	// (e.g. db.index.vector.queryNodes, db.index.vector.queryRelationships).
	VectorSearch *bool `json:"vectorSearch,omitempty"`
	// VectorPropertySet indicates whether the Cypher query sets vector properties
	// (e.g. db.create.setNodeVectorProperty, db.create.setRelationshipVectorProperty).
	VectorPropertySet *bool `json:"vectorPropertySet,omitempty"`
	// FullTextSearch indicates whether the Cypher query uses full-text index search
	// (e.g. db.index.fulltext.queryNodes, db.index.fulltext.queryRelationships).
	FullTextSearch *bool `json:"fullTextSearch,omitempty"`
}

// toolProperties contains tool event properties (used for both STDIO and HTTP modes)
// Note: Neo4j connection info (version, edition, cypher version) is sent once in CONNECTION_INITIALIZED event
type toolProperties struct {
	baseProperties
	ToolUsed           string `json:"tools_used"`
	Success            bool   `json:"success"`
	VectorIndexCount   *int   `json:"vectorIndex,omitempty"`
	FullTextIndexCount *int   `json:"fullTextIndex,omitempty"`
	VectorSearch       *bool  `json:"vectorSearch,omitempty"`
	VectorPropertySet  *bool  `json:"vectorPropertySet,omitempty"`
	FullTextSearch     *bool  `json:"fullTextSearch,omitempty"`
}

type TrackEvent struct {
	Event      string      `json:"event"`
	Properties interface{} `json:"properties"`
}

func (a *Analytics) NewGDSProjCreatedEvent() TrackEvent {
	return TrackEvent{
		Event:      strings.Join([]string{eventNamePrefix, "GDS_PROJ_CREATED"}, "_"),
		Properties: a.getBaseProperties(),
	}
}

func (a *Analytics) NewGDSProjDropEvent() TrackEvent {
	return TrackEvent{
		Event:      strings.Join([]string{eventNamePrefix, "GDS_PROJ_DROP"}, "_"),
		Properties: a.getBaseProperties(),
	}
}

// ConnectionEventInfo contains Neo4j connection information obtained from database queries
type ConnectionEventInfo struct {
	Neo4jVersion  string
	Edition       string
	CypherVersion []string
}

// NewStartupEvent creates a server startup event with information available immediately (no DB query)
func (a *Analytics) NewStartupEvent(transportMode config.TransportMode, tlsEnabled bool, mcpVersion string) TrackEvent {
	props := serverStartupProperties{
		baseProperties: a.getBaseProperties(),
		McpVersion:     mcpVersion,
		TransportMode:  transportMode,
	}

	// Only include TLS field for HTTP mode (omitted for STDIO via omitempty tag with nil pointer)
	if props.TransportMode == config.TransportModeHTTP {
		props.TLSEnabled = &tlsEnabled
	}

	return TrackEvent{
		Event:      strings.Join([]string{eventNamePrefix, "MCP_STARTUP"}, "_"),
		Properties: props,
	}
}

// NewConnectionInitializedEvent creates a connection initialized event with DB information (STDIO mode only)
func (a *Analytics) NewConnectionInitializedEvent(connInfo ConnectionEventInfo) TrackEvent {
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "CONNECTION_INITIALIZED"}, "_"),
		Properties: connectionInitializedProperties{
			baseProperties: a.getBaseProperties(),
			Neo4jVersion:   connInfo.Neo4jVersion,
			Edition:        connInfo.Edition,
			CypherVersion:  connInfo.CypherVersion,
		},
	}
}

// NewToolEvent creates a tool usage event (used for both STDIO and HTTP modes)
// Note: Connection info (Neo4j version, edition) is sent separately in CONNECTION_INITIALIZED event
// The vectorInfo parameter is optional; when non-nil, vector-related properties are included.
func (a *Analytics) NewToolEvent(toolsUsed string, success bool, vectorInfo *ToolVectorInfo) TrackEvent {
	props := toolProperties{
		baseProperties: a.getBaseProperties(),
		ToolUsed:       toolsUsed,
		Success:        success,
	}
	if vectorInfo != nil {
		props.VectorIndexCount = vectorInfo.VectorIndexCount
		props.FullTextIndexCount = vectorInfo.FullTextIndexCount
		props.VectorSearch = vectorInfo.VectorSearch
		props.VectorPropertySet = vectorInfo.VectorPropertySet
		props.FullTextSearch = vectorInfo.FullTextSearch
	}
	return TrackEvent{
		Event:      strings.Join([]string{eventNamePrefix, "TOOL_USED"}, "_"),
		Properties: props,
	}
}

// schemaRetrievalProperties carries the fields for a SCHEMA_RETRIEVAL event.
//
// The event fires once per get-schema invocation that produces a response, on
// both the full-scan (primary) and sampled (fallback) paths. Over time the
// Mixpanel stream answers three calibration questions at once:
//  1. What fraction of calls exceed the configured timeout and fall back to
//     sampling? (outcome = sampled vs full_scan)
//  2. How long does a successful full scan take? (duration_ms distribution,
//     filtered to outcome = full_scan) — drives future default-timeout choices.
//  3. On what kinds of graphs does sampling kick in? (node_label_count,
//     rel_type_count, index_count when outcome = sampled)
//
// The missing_* counts surface how often the completeness heuristic fires,
// which is a secondary data-quality signal on top of the calibration split.
type schemaRetrievalProperties struct {
	baseProperties
	// Outcome tags which path produced the response. Valid values:
	//   "full_scan" — primary db.schema.* procedures completed within timeout
	//   "sampled"  — primary timed out, response came from the sampling fallback
	Outcome string `json:"outcome"`
	// DurationMs is wall-clock time from the start of handleGetSchema until
	// just before the response is returned. For the sampled outcome this
	// includes the full timeout wait PLUS the sampling queries — i.e. the
	// end-to-end time the caller experienced, not just the sampling portion.
	DurationMs int64 `json:"duration_ms"`
	// TimeoutSeconds is the configured SchemaTimeout in seconds, 0 if disabled.
	// Always included so the distribution can be re-bucketed after a default change.
	TimeoutSeconds float64 `json:"timeout_seconds"`
	// SampleSize is the per-label / per-relationship-type budget used on the
	// sampling path. Zero when Outcome is "full_scan" (the primary path does
	// not use sample budgets).
	SampleSize int `json:"sample_size"`
	// NodeLabelCount, RelTypeCount and IndexCount capture the breadth of the
	// response. For an empty graph all three are 0. Internal labels (_Bloom_*)
	// are already excluded from the arrays these are derived from.
	NodeLabelCount int `json:"node_label_count"`
	RelTypeCount   int `json:"rel_type_count"`
	IndexCount     int `json:"index_count"`
	// MissingNodeLabelCount and MissingRelTypeCount are the lengths of the
	// completeness-heuristic's Missing* arrays — labels/types present in the
	// indexes array but absent from the main schema arrays. Non-zero even on
	// the full_scan path indicates that the primary procedures returned a
	// silently-incomplete schema, which is useful to know in aggregate even
	// when the individual request did not time out.
	MissingNodeLabelCount int `json:"missing_node_label_count"`
	MissingRelTypeCount   int `json:"missing_rel_type_count"`
}

// NewSchemaRetrievalEvent records a single get-schema invocation's outcome,
// timing, and resulting schema breadth. See schemaRetrievalProperties for
// the field semantics.
//
// Negative numeric inputs are clamped to 0 so a driver or interface bug
// doesn't leak a nonsense value into the distribution.
func (a *Analytics) NewSchemaRetrievalEvent(
	outcome string,
	durationMs int64,
	timeoutSeconds float64,
	sampleSize int,
	nodeLabelCount, relTypeCount, indexCount int,
	missingNodeLabelCount, missingRelTypeCount int,
) TrackEvent {
	if durationMs < 0 {
		durationMs = 0
	}
	if sampleSize < 0 {
		sampleSize = 0
	}
	if nodeLabelCount < 0 {
		nodeLabelCount = 0
	}
	if relTypeCount < 0 {
		relTypeCount = 0
	}
	if indexCount < 0 {
		indexCount = 0
	}
	if missingNodeLabelCount < 0 {
		missingNodeLabelCount = 0
	}
	if missingRelTypeCount < 0 {
		missingRelTypeCount = 0
	}
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "SCHEMA_RETRIEVAL"}, "_"),
		Properties: schemaRetrievalProperties{
			baseProperties:        a.getBaseProperties(),
			Outcome:               outcome,
			DurationMs:            durationMs,
			TimeoutSeconds:        timeoutSeconds,
			SampleSize:            sampleSize,
			NodeLabelCount:        nodeLabelCount,
			RelTypeCount:          relTypeCount,
			IndexCount:            indexCount,
			MissingNodeLabelCount: missingNodeLabelCount,
			MissingRelTypeCount:   missingRelTypeCount,
		},
	}
}

// cypherEstimateProperties carries the fields for a CYPHER_ESTIMATE_ACCURACY event.
//
// The event closes the feedback loop on the EXPLAIN-time estimate guard applied
// by read-cypher: each invocation records what the planner predicted, what the
// query actually produced, and how the guard acted. Running the event stream
// through Mixpanel over time gives us the true accuracy distribution of the
// planner's EstimatedRows on real MCP traffic, which is the signal needed to
// tune DefaultCypherMaxEstimatedRows away from the current best-guess default.
//
// Deliberately NO query text: the Cypher can reference schema identifiers
// (label names, property names) and in the worst case embed inline literals
// that weren't parameterised. Calibration only needs the shape distribution
// (estimate vs actual) — which is the whole point of having a separate row cap
// and timeout as the actual protection mechanism. If a specific anomalous event
// needs investigation, correlate by timestamp against server-side query.log.
type cypherEstimateProperties struct {
	baseProperties
	// EstimatedRows is the planner's root-operator EstimatedRows value. Zero when
	// no estimate was available (empty plan, missing key, or the query was a
	// bare EXPLAIN/PROFILE that short-circuited).
	EstimatedRows int64 `json:"estimated_rows"`
	// ActualRows is the number of records returned to the caller. Zero when
	// Outcome is "refused_over_estimate" — we never executed, so there is no
	// actual to compare against.
	ActualRows int `json:"actual_rows"`
	// Truncated is true when the row cap was hit during iteration. Only
	// meaningful when Outcome is "executed" or "estimate_error" (both paths
	// actually ran the query).
	Truncated bool `json:"truncated"`
	// Outcome tags what path the query took. Valid values:
	//   "executed"              — estimate under threshold, query ran
	//   "refused_over_estimate" — estimate exceeded threshold, guard blocked
	//   "estimate_error"        — EstimateRowCount failed, query ran fail-open
	Outcome string `json:"outcome"`
	// EstimateThreshold and RowCap echo the config active at emission time so
	// the distribution can be correctly bucketed when defaults change across
	// releases.
	EstimateThreshold int `json:"estimate_threshold"`
	RowCap            int `json:"row_cap"`
}

// NewCypherEstimateEvent records a single read-cypher invocation's estimate-versus-actual
// signal. See cypherEstimateProperties for the field semantics.
//
// Negative numeric inputs are clamped to 0 — the only source of negative values
// would be a driver or interface bug, and we'd rather surface 0 than leak a
// nonsense number into the distribution.
func (a *Analytics) NewCypherEstimateEvent(
	outcome string,
	estimatedRows int64,
	actualRows int,
	truncated bool,
	estimateThreshold, rowCap int,
) TrackEvent {
	if estimatedRows < 0 {
		estimatedRows = 0
	}
	if actualRows < 0 {
		actualRows = 0
	}
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "CYPHER_ESTIMATE_ACCURACY"}, "_"),
		Properties: cypherEstimateProperties{
			baseProperties:    a.getBaseProperties(),
			EstimatedRows:     estimatedRows,
			ActualRows:        actualRows,
			Truncated:         truncated,
			Outcome:           outcome,
			EstimateThreshold: estimateThreshold,
			RowCap:            rowCap,
		},
	}
}

// NewUnauthenticatedJSONRPCEvent creates events for unauthenticated JSON-RPC requests e.g tools/list from MCP Clients
// Only applies to HTTP(S) transport and is typically found where MCP Clients are checking the MCP Server is alive
func (a *Analytics) NewUnauthenticatedJSONRPCEvent(jsonrpc string) TrackEvent {
	slog.Info("NewUnauthenticatedJSONRPCEvent", "method", jsonrpc)
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "UNAUTHENTICATED_JSONRPC"}, "_"),
		Properties: unauthenticatedJSONRPCProperties{
			baseProperties: a.getBaseProperties(),
			JSONRPCMethod:  strings.ToUpper(jsonrpc),
		},
	}
}

func (a *Analytics) getBaseProperties() baseProperties {
	uptime := time.Now().Unix() - a.cfg.startupTime
	insertID := a.newInsertID()
	return baseProperties{
		Token:      a.cfg.token,
		DistinctID: a.cfg.distinctID,
		Time:       time.Now().UnixMilli(),
		InsertID:   insertID,
		Uptime:     uptime,
		OS:         runtime.GOOS,
		OSArch:     runtime.GOARCH,
		IsAura:     a.cfg.isAura,
		MachineID:  a.cfg.machineID,
		BinaryPath: a.cfg.binaryPath,
	}
}

func (a *Analytics) newInsertID() string {
	insertID, err := uuid.NewV6()
	if err != nil {
		slog.Error("Error while generating insert ID for analytics", "error", err.Error())
		return ""
	}
	return insertID.String()
}
