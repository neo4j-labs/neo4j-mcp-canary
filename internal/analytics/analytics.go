// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	mixpanel "github.com/mixpanel/mixpanel-go"
)

type analyticsConfig struct {
	distinctID  string
	startupTime int64
	isAura      bool
	mp          *mixpanel.ApiClient
}

type Analytics struct {
	disabled bool
	cfg      analyticsConfig
}

func NewAnalytics(mixPanelToken string, mixpanelEndpoint string, uri string) *Analytics {
	return &Analytics{
		cfg: analyticsConfig{
			distinctID:  GetDistinctID(),
			startupTime: time.Now().Unix(),
			isAura:      isAura(uri),
			mp:          mixpanel.NewApiClient(mixPanelToken, mixpanel.EuResidency()),
		},
	}
}

// NewAnalyticsWithClient for testing — pass an httptest.Server URL as mixpanelEndpoint
// and a token; the SDK will route requests there via ProxyApiClient.
func NewAnalyticsWithClient(mixPanelToken string, mixpanelEndpoint string, uri string) *Analytics {
	return NewAnalytics(mixPanelToken, mixpanelEndpoint, uri)
}

func isAura(uri string) bool {
	return strings.Contains(uri, "databases.neo4j.io")
}

func (a *Analytics) EmitEvent(event TrackEvent) {
	if a.disabled {
		return
	}
	slog.Info("Sending event to Mixpanel", "event", event.Event)
	if err := a.sendTrackEvent([]TrackEvent{event}); err != nil {
		slog.Error("Error while sending analytics events", "error", err.Error())
	}
}

func (a *Analytics) Enable()         { a.disabled = false }
func (a *Analytics) Disable()        { a.disabled = true }
func (a *Analytics) IsEnabled() bool { return !a.disabled }

func (a *Analytics) sendTrackEvent(events []TrackEvent) error {
	sdkEvents := make([]*mixpanel.Event, 0, len(events))
	for _, e := range events {
		props, err := toPropertiesMap(e.Properties)
		if err != nil {
			return fmt.Errorf("marshal properties for event %q: %w", e.Event, err)
		}
		sdkEvents = append(sdkEvents, a.cfg.mp.NewEvent(e.Event, a.cfg.distinctID, props))
	}

	if err := a.cfg.mp.Track(context.Background(), sdkEvents); err != nil {
		return fmt.Errorf("mixpanel track: %w", err)
	}
	return nil
}

// toPropertiesMap converts any properties struct to map[string]any via JSON
// so it's compatible with the SDK without duplicating field mappings.
func toPropertiesMap(props any) (map[string]any, error) {
	b, err := json.Marshal(props)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func GetDistinctID() string {
	id, err := uuid.NewV6()
	if err != nil {
		slog.Error("Error generating distinct ID for analytics", "error", err.Error())
		return ""
	}
	return id.String()
}
