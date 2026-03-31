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

// unauthenticatedJsonRpcProperties contains the JsonRpc request
type unauthenticatedJsonRpcProperties struct {
	baseProperties
	JsonRpcMethod string `json:"method"`
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

// schemaTimeoutFallbackProperties contains properties for schema timeout fallback events.
type schemaTimeoutFallbackProperties struct {
	baseProperties
	TimeoutSeconds float64 `json:"timeout_seconds"`
	SampleSize     int     `json:"sample_size"`
}

// NewSchemaTimeoutFallbackEvent creates an event recording that get-schema timed out
// and fell back to the sampling-based approach.
func (a *Analytics) NewSchemaTimeoutFallbackEvent(timeoutSeconds float64, sampleSize int) TrackEvent {
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "SCHEMA_TIMEOUT_FALLBACK"}, "_"),
		Properties: schemaTimeoutFallbackProperties{
			baseProperties: a.getBaseProperties(),
			TimeoutSeconds: timeoutSeconds,
			SampleSize:     sampleSize,
		},
	}
}

// NewUnauthenticatedJsonRpcEvent creates events for unauthenticated JSONRPC requests e.g tools/list from MCP Clients
// Only applies to HTTP(S) transport and is typically found where MCP Clients are checking the MCP Server is alive
func (a *Analytics) NewUnauthenticatedJsonRpcEvent(jsonrpc string) TrackEvent {
	slog.Info("NewUnauthenticatedJsonRpcEvent", "method", jsonrpc)
	return TrackEvent{
		Event: strings.Join([]string{eventNamePrefix, "UNAUTHENTICATED_JSONRPC"}, "_"),
		Properties: unauthenticatedJsonRpcProperties{
			baseProperties: a.getBaseProperties(),
			JsonRpcMethod:  strings.ToUpper(jsonrpc),
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
