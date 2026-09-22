// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
)

// discoveryResponse holds the fields of interest from the Neo4j discovery
// endpoint (GET on the base URI, unauthenticated). Only neo4j_version is
// used by this package; neo4j_edition is decoded for future use/debugging.
type discoveryResponse struct {
	Neo4jVersion string `json:"neo4j_version"`
	Neo4jEdition string `json:"neo4j_edition"`
}

// minCalendarYear and minCalendarMonth are the minimum calendar-versioned
// (e.g. "2026.07", "2026.07.0") Neo4j release this package supports the
// Query API against.
//
// This floor is 2026.07, not the Query API v2 endpoint's own general
// availability version (2026.06), because GetQueryType's read/write
// classification depends on the queryType field in the query response,
// which Neo4j's Query API only introduced in 2026.07 (confirmed against
// Neo4j's docs-query-api changelog and verified live: a 2026.06.0 server's
// response — buffered or streaming, with or without includeCounters —
// carries no queryType field at all, and the "containsUpdates" counter
// available via includeCounters reflects actual execution, not the EXPLAIN
// pre-flight check read-cypher relies on, since EXPLAIN never executes).
// Without queryType, there is no reliable way to classify a query as
// read-only before running it, which read-cypher's write-rejection
// guarantee depends on — so servers below this floor are rejected rather
// than silently offering a degraded or unsafe guard.
const (
	minCalendarYear  = 2026
	minCalendarMonth = 7
)

// minClassicAuraMajor and minClassicAuraMinor are the minimum classic
// (pre-calendar-versioning) release this package accepts, and only when the
// version string carries the "-aura" suffix — a bare classic version without
// it is rejected outright (see CheckMinimumVersion). This isn't just a
// product-support floor: github.com/neo4j-contrib/query-go-sdk v0.6.0 (our
// pinned, and currently newest published, dependency) hardcodes its
// typed-JSON media type to "v1.1" (introduced in Neo4j's 2025.11 calendar
// release), which self-managed classic releases predate and don't
// recognize — every Query API call against a bare self-managed classic
// server 406s regardless of this check, so accepting one here would just be
// promising connectivity this SDK build can't deliver.
const (
	minClassicAuraMajor = 5
	minClassicAuraMinor = 26
)

// VersionError is returned by CheckMinimumVersion when the connected Neo4j
// server's reported version is below the minimum this package requires for
// Query API support, or is in a format that cannot be classified at all.
type VersionError struct {
	// Got is the raw neo4j_version string reported by the server's
	// discovery endpoint.
	Got string
	// Reason explains why Got was rejected.
	Reason string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("neo4j version %q does not support the Query API for this server: %s", e.Got, e.Reason)
}

// DiscoverVersion performs an unauthenticated GET against baseURL (the same
// URI configured as NEO4J_URI) and returns the neo4j_version string reported
// by the discovery endpoint, e.g. "2026.07" or "5.27-aura".
func DiscoverVersion(ctx context.Context, httpClient *http.Client, baseURL string) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build discovery request for %q: %w", baseURL, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach Neo4j discovery endpoint at %q: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read discovery response from %q: %w", baseURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery endpoint at %q returned HTTP %d: %s", baseURL, resp.StatusCode, string(body))
	}

	var discovery discoveryResponse
	if err := json.Unmarshal(body, &discovery); err != nil {
		return "", fmt.Errorf("failed to decode discovery response from %q: %w", baseURL, err)
	}

	if discovery.Neo4jVersion == "" {
		return "", fmt.Errorf("discovery response from %q did not include neo4j_version", baseURL)
	}

	return discovery.Neo4jVersion, nil
}

// EnsureMinimumVersion discovers the connected server's reported Neo4j
// version and checks it against CheckMinimumVersion's floor, in one call.
// Intended to run once at startup, before any Service is
// constructed — see the project plan's "main.go wiring" section.
func EnsureMinimumVersion(ctx context.Context, httpClient *http.Client, baseURL string) error {
	version, err := DiscoverVersion(ctx, httpClient, baseURL)
	if err != nil {
		return fmt.Errorf("failed to determine Neo4j version for Query API support check: %w", err)
	}
	return CheckMinimumVersion(version)
}

// CheckMinimumVersion validates that version meets the floor this package
// requires for Query API support:
//
//   - Calendar-versioned releases (e.g. "2026.07", "2026.07.0") must be >=
//     2026.07.
//   - Classic-versioned Aura releases (e.g. "5.26-aura") must be >= 5.26-aura.
//   - Anything else — including a bare classic version with no "-aura"
//     suffix (e.g. "5.26"), even if numerically >= 5.26 — is rejected: see
//     minClassicAuraMajor/minClassicAuraMinor's doc comment for why.
//
// Returns a *VersionError on any rejection so callers can report the exact
// reason to the operator.
//
// The comparison logic itself lives in database.MeetsMinimumVersion (moved
// there so internal/readiness can reuse it for the search category's own,
// different floor) — this function is a thin wrapper that preserves this
// package's original external contract (a *VersionError, not a bare bool).
func CheckMinimumVersion(version string) error {
	if ok, reason := database.MeetsMinimumVersion(version, minCalendarYear, minCalendarMonth, minClassicAuraMajor, minClassicAuraMinor); !ok {
		return &VersionError{Got: version, Reason: reason}
	}
	return nil
}
