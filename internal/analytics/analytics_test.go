// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package analytics_test

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	amocks "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"

	"go.uber.org/mock/gomock"
)

// newTestAnalytics creates an analytics service for testing
func newTestAnalytics(t *testing.T, token, endpoint string, client analytics.HTTPClient, uri string) *analytics.Analytics {
	t.Helper()
	return analytics.NewAnalyticsWithClient(token, endpoint, client, uri)
}

func TestAnalytics(t *testing.T) {
	t.Run("EmitEvent should not send event if disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := amocks.NewMockHTTPClient(ctrl)

		analyticsService := newTestAnalytics(t, "test-token", "http://localhost", mockClient, "bolt://localhost:7687")
		analyticsService.Disable()
		analyticsService.EmitEvent(analytics.TrackEvent{Event: "test_event"})
	})

	t.Run("EmitEvent should send event if enabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := amocks.NewMockHTTPClient(ctrl)

		mockClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any()).Return(&http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("1")),
		}, nil)

		analyticsService := newTestAnalytics(t, "test-token", "http://localhost", mockClient, "bolt://localhost:7687")
		analyticsService.EmitEvent(analytics.TrackEvent{Event: "test_event"})
	})

	t.Run("EmitEvent should send the correct event in the body", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := amocks.NewMockHTTPClient(ctrl)

		event := analytics.TrackEvent{
			Event: "specific_event",
			Properties: map[string]interface{}{
				"key": "value",
			},
		}

		mockClient.EXPECT().Post("http://localhost/track?verbose=1", gomock.Any(), gomock.Any()).
			DoAndReturn(func(_, _ string, body io.Reader) (*http.Response, error) {
				bodyBytes, err := io.ReadAll(body)
				if err != nil {
					t.Fatalf("error reading body: %v", err)
				}

				var decodedEvents []analytics.TrackEvent
				err = json.Unmarshal(bodyBytes, &decodedEvents)
				if err != nil {
					t.Fatalf("error unmarshalling body: %v", err)
				}
				if len(decodedEvents) != 1 {
					t.Fatalf("expected 1 event, got %d", len(decodedEvents))
				}
				decodedEvent := decodedEvents[0]

				if decodedEvent.Event != "specific_event" {
					t.Errorf("expected event 'specific_event', got '%s'", decodedEvent.Event)
				}
				properties, ok := decodedEvent.Properties.(map[string]interface{})
				if !ok {
					t.Fatalf("properties is not a map[string]interface{}")
				}
				if properties["key"] != "value" {
					t.Errorf("expected properties['key'] to be 'value', got '%v'", properties["key"])
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("1")),
				}, nil
			})

		analyticsService := newTestAnalytics(t, "test-token", "http://localhost", mockClient, "bolt://localhost:7687")
		analyticsService.EmitEvent(event)
	})

	t.Run("EmitEvent should send the correct event in the body", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := amocks.NewMockHTTPClient(ctrl)

		event := analytics.TrackEvent{
			Event: "specific_event",
			Properties: map[string]interface{}{
				"key": "value",
			},
		}

		mockClient.EXPECT().Post("http://localhost/track?verbose=1", gomock.Any(), gomock.Any()).
			DoAndReturn(func(_, _ string, body io.Reader) (*http.Response, error) {
				bodyBytes, err := io.ReadAll(body)
				if err != nil {
					t.Fatalf("error reading body: %v", err)
				}

				var decodedEvents []analytics.TrackEvent
				err = json.Unmarshal(bodyBytes, &decodedEvents)
				if err != nil {
					t.Fatalf("error unmarshalling body: %v", err)
				}
				if len(decodedEvents) != 1 {
					t.Fatalf("expected 1 event, got %d", len(decodedEvents))
				}
				decodedEvent := decodedEvents[0]

				if decodedEvent.Event != "specific_event" {
					t.Errorf("expected event 'specific_event', got '%s'", decodedEvent.Event)
				}
				properties, ok := decodedEvent.Properties.(map[string]interface{})
				if !ok {
					t.Fatalf("properties is not a map[string]interface{}")
				}
				if properties["key"] != "value" {
					t.Errorf("expected properties['key'] to be 'value', got '%v'", properties["key"])
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("1")),
				}, nil
			})

		analyticsService := newTestAnalytics(t, "test-token", "http://localhost", mockClient, "bolt://localhost:7687")
		analyticsService.EmitEvent(event)
	})

	t.Run("EmitEvent should construct the correct URL (only one '/' between host and path)", func(t *testing.T) {
		testCases := []struct {
			name             string
			mixpanelEndpoint string
			expectedURL      string
		}{
			{
				name:             "endpoint with trailing slash",
				mixpanelEndpoint: "http://localhost/",
				expectedURL:      "http://localhost/track?verbose=1",
			},
			{
				name:             "endpoint without trailing slash",
				mixpanelEndpoint: "http://localhost",
				expectedURL:      "http://localhost/track?verbose=1",
			},
			{
				name:             "endpoint with multiple trailing slashes",
				mixpanelEndpoint: "http://localhost//",
				expectedURL:      "http://localhost/track?verbose=1",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				mockClient := amocks.NewMockHTTPClient(ctrl)

				mockClient.EXPECT().Post(tc.expectedURL, gomock.Any(), gomock.Any()).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("1")),
				}, nil)

				analyticsService := newTestAnalytics(t, "test-token", tc.mixpanelEndpoint, mockClient, "bolt://localhost:7687")
				analyticsService.EmitEvent(analytics.TrackEvent{Event: "test_event"})
			})
		}
	})
}

func TestEventCreation(t *testing.T) {
	analyticsService := newTestAnalytics(t, "test-token", "http://localhost", nil, "bolt://localhost:7687")

	t.Run("NewGDSProjCreatedEvent", func(t *testing.T) {
		event := analyticsService.NewGDSProjCreatedEvent()
		if event.Event != "MCP-NEO4J-CANARY_GDS_PROJ_CREATED" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_GDS_PROJ_CREATED")
		}
		assertBaseProperties(t, event.Properties)
	})

	t.Run("NewGDSProjDropEvent", func(t *testing.T) {
		event := analyticsService.NewGDSProjDropEvent()
		if event.Event != "MCP-NEO4J-CANARY_GDS_PROJ_DROP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_GDS_PROJ_DROP")
		}
		assertBaseProperties(t, event.Properties)
	})

	t.Run("NewToolEvent without vector info", func(t *testing.T) {
		event := analyticsService.NewToolEvent("gds", true, nil)
		if event.Event != "MCP-NEO4J-CANARY_TOOL_USED" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_TOOL_USED")
		}
		props := assertBaseProperties(t, event.Properties)
		if props["tools_used"] != "gds" {
			t.Errorf("unexpected tools_used: got %v, want %v", props["tools_used"], "gds")
		}
		if props["success"] != true {
			t.Errorf("unexpected success: got %v, want %v", props["success"], true)
		}
		// Vector properties should not be present when vectorInfo is nil
		if _, exists := props["vectorIndex"]; exists {
			t.Errorf("vectorIndex should not be present when vectorInfo is nil")
		}
		if _, exists := props["vectorSearch"]; exists {
			t.Errorf("vectorSearch should not be present when vectorInfo is nil")
		}
		if _, exists := props["vectorPropertySet"]; exists {
			t.Errorf("vectorPropertySet should not be present when vectorInfo is nil")
		}
	})

	t.Run("NewToolEvent with vector index count for get-schema", func(t *testing.T) {
		count := 3
		vectorInfo := &analytics.ToolVectorInfo{
			VectorIndexCount: &count,
		}
		event := analyticsService.NewToolEvent("get-schema", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if props["tools_used"] != "get-schema" {
			t.Errorf("unexpected tools_used: got %v, want %v", props["tools_used"], "get-schema")
		}
		if props["vectorIndex"] != float64(3) {
			t.Errorf("unexpected vectorIndex: got %v, want %v", props["vectorIndex"], 3)
		}
		// vectorSearch and vectorPropertySet should not be present
		if _, exists := props["vectorSearch"]; exists {
			t.Errorf("vectorSearch should not be present for get-schema")
		}
	})

	t.Run("NewToolEvent with zero vector indexes for get-schema", func(t *testing.T) {
		count := 0
		vectorInfo := &analytics.ToolVectorInfo{
			VectorIndexCount: &count,
		}
		event := analyticsService.NewToolEvent("get-schema", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		// Even with 0, the field should be present since the pointer is non-nil
		if props["vectorIndex"] != float64(0) {
			t.Errorf("unexpected vectorIndex: got %v, want %v", props["vectorIndex"], 0)
		}
	})

	t.Run("NewToolEvent with fulltext index count for get-schema", func(t *testing.T) {
		vectorCount := 2
		fulltextCount := 5
		vectorInfo := &analytics.ToolVectorInfo{
			VectorIndexCount:   &vectorCount,
			FullTextIndexCount: &fulltextCount,
		}
		event := analyticsService.NewToolEvent("get-schema", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if props["vectorIndex"] != float64(2) {
			t.Errorf("unexpected vectorIndex: got %v, want %v", props["vectorIndex"], 2)
		}
		if props["fullTextIndex"] != float64(5) {
			t.Errorf("unexpected fullTextIndex: got %v, want %v", props["fullTextIndex"], 5)
		}
	})

	t.Run("NewToolEvent without fulltext index count omits field", func(t *testing.T) {
		vectorCount := 1
		vectorInfo := &analytics.ToolVectorInfo{
			VectorIndexCount: &vectorCount,
		}
		event := analyticsService.NewToolEvent("get-schema", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if _, exists := props["fullTextIndex"]; exists {
			t.Errorf("fullTextIndex should not be present when not set")
		}
	})

	t.Run("NewToolEvent with vector property set for write-cypher", func(t *testing.T) {
		vectorSearch := false
		vectorPropertySet := true
		vectorInfo := &analytics.ToolVectorInfo{
			VectorSearch:      &vectorSearch,
			VectorPropertySet: &vectorPropertySet,
		}
		event := analyticsService.NewToolEvent("write-cypher", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if props["vectorSearch"] != false {
			t.Errorf("unexpected vectorSearch: got %v, want %v", props["vectorSearch"], false)
		}
		if props["vectorPropertySet"] != true {
			t.Errorf("unexpected vectorPropertySet: got %v, want %v", props["vectorPropertySet"], true)
		}
	})

	t.Run("NewToolEvent with full-text search for read-cypher", func(t *testing.T) {
		fullTextSearch := true
		vectorInfo := &analytics.ToolVectorInfo{
			FullTextSearch: &fullTextSearch,
		}
		event := analyticsService.NewToolEvent("read-cypher", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if props["fullTextSearch"] != true {
			t.Errorf("unexpected fullTextSearch: got %v, want %v", props["fullTextSearch"], true)
		}
	})

	t.Run("NewToolEvent without full-text search omits field", func(t *testing.T) {
		vectorCount := 1
		vectorInfo := &analytics.ToolVectorInfo{
			VectorIndexCount: &vectorCount,
		}
		event := analyticsService.NewToolEvent("get-schema", true, vectorInfo)
		props := assertBaseProperties(t, event.Properties)
		if _, exists := props["fullTextSearch"]; exists {
			t.Errorf("fullTextSearch should not be present when not set")
		}
	})

	t.Run("NewSchemaRetrievalEvent with sampled outcome", func(t *testing.T) {
		event := analyticsService.NewSchemaRetrievalEvent("sampled", 2500, 30.0, 1000, 12, 5, 8, 1, 0)
		if event.Event != "MCP-NEO4J-CANARY_SCHEMA_RETRIEVAL" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_SCHEMA_RETRIEVAL")
		}
		props := assertBaseProperties(t, event.Properties)
		if props["outcome"] != "sampled" {
			t.Errorf("unexpected outcome: got %v, want %v", props["outcome"], "sampled")
		}
		if props["duration_ms"] != float64(2500) {
			t.Errorf("unexpected duration_ms: got %v, want %v", props["duration_ms"], 2500)
		}
		if props["timeout_seconds"] != float64(30) {
			t.Errorf("unexpected timeout_seconds: got %v, want %v", props["timeout_seconds"], 30)
		}
		if props["sample_size"] != float64(1000) {
			t.Errorf("unexpected sample_size: got %v, want %v", props["sample_size"], 1000)
		}
		if props["node_label_count"] != float64(12) {
			t.Errorf("unexpected node_label_count: got %v, want %v", props["node_label_count"], 12)
		}
		if props["rel_type_count"] != float64(5) {
			t.Errorf("unexpected rel_type_count: got %v, want %v", props["rel_type_count"], 5)
		}
		if props["index_count"] != float64(8) {
			t.Errorf("unexpected index_count: got %v, want %v", props["index_count"], 8)
		}
		if props["missing_node_label_count"] != float64(1) {
			t.Errorf("unexpected missing_node_label_count: got %v, want %v", props["missing_node_label_count"], 1)
		}
		if props["missing_rel_type_count"] != float64(0) {
			t.Errorf("unexpected missing_rel_type_count: got %v, want %v", props["missing_rel_type_count"], 0)
		}
	})

	t.Run("NewSchemaRetrievalEvent with full_scan outcome", func(t *testing.T) {
		// Full-scan path: sample_size is 0 (not used by the primary schema queries)
		// and serialises to a numeric zero rather than being omitted — this lets
		// the Mixpanel consumer group-by outcome without null-handling pitfalls.
		event := analyticsService.NewSchemaRetrievalEvent("full_scan", 850, 30.0, 0, 3, 2, 4, 0, 0)
		props := assertBaseProperties(t, event.Properties)
		if props["outcome"] != "full_scan" {
			t.Errorf("unexpected outcome: got %v, want %v", props["outcome"], "full_scan")
		}
		if props["sample_size"] != float64(0) {
			t.Errorf("unexpected sample_size: got %v, want %v", props["sample_size"], 0)
		}
		if props["duration_ms"] != float64(850) {
			t.Errorf("unexpected duration_ms: got %v, want %v", props["duration_ms"], 850)
		}
	})

	t.Run("NewSchemaRetrievalEvent clamps negative numeric inputs to zero", func(t *testing.T) {
		// Defensive behaviour: a driver hiccup or an interface change could plausibly
		// surface a negative count where none makes physical sense (you can't have
		// -3 node labels). The constructor clamps rather than letting the nonsense
		// leak into the Mixpanel distribution — a 0 in the data is a known "no signal"
		// value, a -3 is a confusing outlier that poisons dashboards.
		//
		// timeoutSeconds is intentionally NOT clamped: a negative configured timeout
		// is an operator misconfiguration we'd rather surface than silently correct.
		event := analyticsService.NewSchemaRetrievalEvent("full_scan", -100, 30.0, -5, -1, -2, -3, -4, -6)
		props := assertBaseProperties(t, event.Properties)
		for _, field := range []string{
			"duration_ms",
			"sample_size",
			"node_label_count",
			"rel_type_count",
			"index_count",
			"missing_node_label_count",
			"missing_rel_type_count",
		} {
			if props[field] != float64(0) {
				t.Errorf("expected %s clamped to 0, got %v", field, props[field])
			}
		}
		// timeoutSeconds should pass through unclamped (even though we passed a positive
		// value here — this asserts the field still round-trips, which is the fallback
		// check if a future refactor accidentally starts clamping it).
		if props["timeout_seconds"] != float64(30) {
			t.Errorf("expected timeout_seconds unclamped at 30, got %v", props["timeout_seconds"])
		}
	})

	t.Run("NewStartupEvent", func(t *testing.T) {
		event := analyticsService.NewStartupEvent(config.TransportModeStdio, false, "1.0.0")
		if event.Event != "MCP-NEO4J-CANARY_MCP_STARTUP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_MCP_STARTUP")
		}
		props := assertBaseProperties(t, event.Properties)
		if props["$os"] != runtime.GOOS {
			t.Errorf("unexpected os: got %v, want %v", props["os"], runtime.GOOS)
		}
		if props["os_arch"] != runtime.GOARCH {
			t.Errorf("unexpected os_arch: got %v, want %v", props["os_arch"], runtime.GOARCH)
		}
		if props["isAura"] == true {
			t.Errorf("unexpected aura: got %v, want %v", props["isAura"], false)
		}
		if props["mcp_version"] != "1.0.0" {
			t.Errorf("unexpected mcp_version: got %v, want %v", props["mcp_version"], "1.0.0")
		}
		if props["transport_mode"] != "stdio" {
			t.Errorf("unexpected transport_mode: got %v, want %v", props["transport_mode"], "stdio")
		}
	})

	t.Run("NewConnectionInitializedEvent", func(t *testing.T) {
		event := analyticsService.NewConnectionInitializedEvent(analytics.ConnectionEventInfo{
			Neo4jVersion:  "2025.09.01",
			CypherVersion: []string{"5", "25"},
			Edition:       "enterprise",
		})
		if event.Event != "MCP-NEO4J-CANARY_CONNECTION_INITIALIZED" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_CONNECTION_INITIALIZED")
		}
		props := assertBaseProperties(t, event.Properties)
		if props["neo4j_version"] != "2025.09.01" {
			t.Errorf("unexpected Neo4jVersion: got %v, want %v", props["neo4j_version"], "2025.09.01")
		}
		if props["edition"] != "enterprise" {
			t.Errorf("unexpected edition: got %v, want %v", props["edition"], "enterprise")
		}

		cypherVersion, ok := props["cypher_version"].([]interface{})
		if !ok {
			t.Fatalf("cypher_version is not a []interface{}")
		}
		if len(cypherVersion) != 2 || cypherVersion[0] != "5" || cypherVersion[1] != "25" {
			t.Errorf("unexpected cypher_version: got %v, want %v", props["cypher_version"], []string{"5", "25"})
		}
	})

	t.Run("NewStartupEvent with Aura database", func(t *testing.T) {
		auraAnalytics := newTestAnalytics(t, "test-token", "http://localhost", nil, "bolt://mydb.databases.neo4j.io")
		event := auraAnalytics.NewStartupEvent(config.TransportModeHTTP, false, "1.0.0")

		if event.Event != "MCP-NEO4J-CANARY_MCP_STARTUP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_MCP_STARTUP")
		}
		props := assertBaseProperties(t, event.Properties)
		if props["$os"] != runtime.GOOS {
			t.Errorf("unexpected os: got %v, want %v", props["os"], runtime.GOOS)
		}
		if props["os_arch"] != runtime.GOARCH {
			t.Errorf("unexpected os_arch: got %v, want %v", props["os_arch"], runtime.GOARCH)
		}
		if props["isAura"] == false {
			t.Errorf("unexpected aura: got %v, want %v", props["isAura"], true)
		}
		if props["mcp_version"] != "1.0.0" {
			t.Errorf("unexpected mcp_version: got %v, want %v", props["mcp_version"], "1.0.0")
		}
	})

	t.Run("NewStartupEvent with STDIO transport mode", func(t *testing.T) {
		stdioAnalytics := analytics.NewAnalyticsWithClient(
			"test-token",
			"http://localhost",
			nil,
			"bolt://localhost:7687",
		)
		event := stdioAnalytics.NewStartupEvent(config.TransportModeStdio, false, "1.0.0")

		if event.Event != "MCP-NEO4J-CANARY_MCP_STARTUP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_MCP_STARTUP")
		}

		props := assertBaseProperties(t, event.Properties)

		// Verify transport_mode is set to "stdio"
		if props["transport_mode"] != "stdio" {
			t.Errorf("unexpected transport_mode: got %v, want %v", props["transport_mode"], "stdio")
		}

		// Verify tls_enabled is NOT present in STDIO mode (uses omitempty)
		if _, exists := props["tls_enabled"]; exists {
			t.Errorf("tls_enabled should not be present in STDIO mode, but found: %v", props["tls_enabled"])
		}
	})

	t.Run("NewStartupEvent with HTTP transport mode and TLS enabled", func(t *testing.T) {
		httpAnalytics := analytics.NewAnalyticsWithClient(
			"test-token",
			"http://localhost",
			nil,
			"bolt://localhost:7687",
		)
		event := httpAnalytics.NewStartupEvent(config.TransportModeHTTP, true, "1.0.0")

		if event.Event != "MCP-NEO4J-CANARY_MCP_STARTUP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_MCP_STARTUP")
		}

		props := assertBaseProperties(t, event.Properties)

		// Verify transport_mode is set to "http"
		if props["transport_mode"] != "http" {
			t.Errorf("unexpected transport_mode: got %v, want %v", props["transport_mode"], "http")
		}

		// Verify tls_enabled is present and set to true in HTTP mode
		tlsEnabled, exists := props["tls_enabled"]
		if !exists {
			t.Errorf("tls_enabled should be present in HTTP mode")
		} else if tlsEnabled != true {
			t.Errorf("unexpected tls_enabled: got %v, want %v", tlsEnabled, true)
		}
	})

	t.Run("NewStartupEvent with HTTP transport mode and TLS disabled", func(t *testing.T) {
		httpAnalytics := analytics.NewAnalyticsWithClient(
			"test-token",
			"http://localhost",
			nil,
			"bolt://localhost:7687",
		)
		event := httpAnalytics.NewStartupEvent(config.TransportModeHTTP, false, "1.0.0")

		if event.Event != "MCP-NEO4J-CANARY_MCP_STARTUP" {
			t.Errorf("unexpected event name: got %s, want %s", event.Event, "MCP-NEO4J-CANARY_MCP_STARTUP")
		}

		props := assertBaseProperties(t, event.Properties)

		// Verify transport_mode is set to "http"
		if props["transport_mode"] != "http" {
			t.Errorf("unexpected transport_mode: got %v, want %v", props["transport_mode"], "http")
		}

		// Verify tls_enabled is present and set to false in HTTP mode
		tlsEnabled, exists := props["tls_enabled"]
		if !exists {
			t.Errorf("tls_enabled should be present in HTTP mode")
		} else if tlsEnabled != false {
			t.Errorf("unexpected tls_enabled: got %v, want %v", tlsEnabled, false)
		}
	})

}

func assertBaseProperties(t *testing.T, props interface{}) map[string]interface{} {
	t.Helper()
	p, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("failed to marshal properties: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(p, &m); err != nil {
		t.Fatalf("failed to unmarshal properties to map: %v", err)
	}

	if m["token"] != "test-token" {
		t.Errorf("unexpected token: got %v, want %v", m["token"], "test-token")
	}
	if _, ok := m["time"].(float64); !ok {
		t.Errorf("time is not a number")
	}
	if _, ok := m["distinct_id"].(string); !ok {
		t.Errorf("distinct_id is not a string")
	}
	if _, ok := m["$insert_id"].(string); !ok {
		t.Errorf("$insert_id is not a string")
	}
	if _, ok := m["uptime"].(float64); !ok {
		t.Errorf("uptime is not a number")
	}
	if _, ok := m["$os"].(string); !ok {
		t.Errorf("$os is not a string")
	}
	if _, ok := m["os_arch"].(string); !ok {
		t.Errorf("os_arch is not a string")
	}
	if _, ok := m["isAura"].(bool); !ok {
		t.Errorf("isAura is not a bool")
	}
	return m
}
