// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/denisbrodbeck/machineid"
	"github.com/google/uuid"
	mixpanel "github.com/mixpanel/mixpanel-go"
)

// httpClientTransport adapts our HTTPClient interface into an http.RoundTripper,
// allowing the Mixpanel SDK to use our injectable client (including mocks in tests).
// The endpoint is stored here so we can rewrite the URL on every request —
// the SDK resolves its own internal URL before hitting the transport, which
// would otherwise bypass our configured proxy endpoint.
type httpClientTransport struct {
	client   HTTPClient
	endpoint string
}

func (t *httpClientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := strings.TrimLeft(req.URL.Path, "/")
	url := t.endpoint + "/" + path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}
	return t.client.Post(url, req.Header.Get("Content-Type"), req.Body)
}

type analyticsConfig struct {
	distinctID  string
	machineID   string
	binaryPath  string
	token       string
	startupTime int64
	isAura      bool
	outboundIP  string
	mp          *mixpanel.ApiClient
}

type Analytics struct {
	disabled bool
	cfg      analyticsConfig
}

// NewAnalytics creates an Analytics instance using the default http.Client.
func NewAnalytics(mixPanelToken string, mixpanelEndpoint string, uri string) *Analytics {
	return NewAnalyticsWithClient(mixPanelToken, mixpanelEndpoint, &http.Client{Timeout: 10 * time.Second}, uri)
}

// NewAnalyticsWithClient creates an Analytics instance with an injectable HTTPClient,
// allowing tests to intercept outbound Mixpanel calls via a mock.
func NewAnalyticsWithClient(mixPanelToken string, mixpanelEndpoint string, client HTTPClient, uri string) *Analytics {
	endpoint := strings.TrimRight(mixpanelEndpoint, "/")

	var mpClient *mixpanel.ApiClient
	if client != nil {
		httpClient := &http.Client{Transport: &httpClientTransport{client: client, endpoint: endpoint}}
		mpClient = mixpanel.NewApiClient(mixPanelToken,
			mixpanel.HttpClient(httpClient),
		)
	} else {
		mpClient = mixpanel.NewApiClient(mixPanelToken,
			mixpanel.ProxyApiLocation(endpoint),
		)
	}

	return &Analytics{
		cfg: analyticsConfig{
			distinctID:  GetDistinctID(),
			machineID:   GetMachineID(),
			binaryPath:  GetBinaryPath(),
			token:       mixPanelToken,
			startupTime: time.Now().Unix(),
			isAura:      isAura(uri),
			mp:          mpClient,
		},
	}
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
		return fmt.Errorf("mixpanel track error: %w", err)
	}
	slog.Info("Sent event to Mixpanel", "event", sdkEvents[0].Name)
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

// GetBinaryPath returns the absolute path of the running binary via os.Executable.
// Symlinks are resolved so the real on-disk path is reported.
// Any occurrence of the user's home directory or username is redacted.
// Returns an empty string on failure.
func GetBinaryPath() string {
	path, err := os.Executable()
	if err != nil {
		slog.Warn("Could not determine binary path for analytics", "error", err)
		return ""
	}

	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		slog.Warn("Could not resolve binary path symlinks for analytics", "error", err)
		// Continue with the unresolved path rather than returning empty.
	}

	return redactPath(path)
}

// redactPath removes personally identifiable segments from a file path.
// It replaces the home directory prefix first (most specific), then falls back
// to replacing any remaining occurrences of the username.
func redactPath(path string) string {
	// 1. Replace home directory prefix — works on Linux (/home/user),
	//    macOS (/Users/user) and Windows (C:\Users\user).
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// filepath.Rel gives us the portion after the home dir, without
		// needing to worry about slash style differences on Windows.
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("<home>", rel)
		}
	}

	// 2. Fallback: replace the username directly in case the home dir lookup
	//    failed or the binary lives outside the home dir but still contains
	//    the username (e.g. /tmp/username/bin).
	if user, err := user.Current(); err == nil && user.Username != "" {
		// On Windows, Current().Username may be "DOMAIN\user" — strip the domain.
		username := user.Username
		if idx := strings.LastIndex(username, `\`); idx != -1 {
			username = username[idx+1:]
		}
		path = strings.ReplaceAll(path, username, "<user>")
	}

	return path
}

// GetMachineID returns a stable, privacy-safe machine identifier using the OS-provided
// hardware UUID, HMAC-hashed with the app name so the raw system UUID is never exposed.
// Returns an empty string on failure (e.g. insufficient permissions on some Linux configs).
func GetMachineID() string {
	id, err := machineid.ProtectedID("neo4j-mcp-canary")
	if err != nil {
		slog.Warn("Could not retrieve machine ID for analytics", "error", err)
		return ""
	}
	return id
}

func GetDistinctID() string {
	id, err := uuid.NewV6()
	if err != nil {
		slog.Error("Error generating distinct ID for analytics", "error", err.Error())
		return ""
	}
	return id.String()
}
