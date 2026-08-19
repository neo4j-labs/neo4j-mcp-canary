// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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
// version string carries the "-aura" suffix — see checkMinimumVersion for
// why a bare classic version without that suffix is rejected outright.
// 5.27-aura is the classic-versioned counterpart to the 2026.07 calendar
// floor — see the calendar floor's doc comment for why.
const (
	minClassicAuraMajor = 5
	minClassicAuraMinor = 27
)

// classicAuraVersionPattern matches classic Neo4j versions reported by Aura,
// e.g. "5.26-aura", "5.27-aura". Aura can report this style even after the
// calendar-versioning cutover, so it is checked against its own floor
// (minClassicAuraMajor.minClassicAuraMinor) rather than the calendar floor.
// Note that a version matching this pattern (e.g. "5.26-aura") can still be
// rejected by CheckMinimumVersion if it's below the floor — this pattern
// only recognizes the shape, it doesn't imply acceptance.
var classicAuraVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)-aura$`)

// calendarVersionPattern matches calendar-versioned Neo4j releases, e.g.
// "2026.07", "2026.07.0". The patch component is optional and ignored —
// the year/month pair alone is sufficient to compare against the floor.
var calendarVersionPattern = regexp.MustCompile(`^(\d{4})\.(\d{1,2})(?:\.\d+)?$`)

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
//   - Classic-versioned Aura releases (e.g. "5.27-aura") must be >= 5.27-aura.
//   - Anything else — including a bare classic version with no "-aura"
//     suffix (e.g. "5.27"), even if numerically >= 5.27 — is rejected: the
//     Query API's queryType field, which read/write classification depends
//     on, never shipped on self-managed classic-versioned releases.
//
// Returns a *VersionError on any rejection so callers can report the exact
// reason to the operator.
func CheckMinimumVersion(version string) error {
	if m := classicAuraVersionPattern.FindStringSubmatch(version); m != nil {
		major, minor := atoiMust(m[1]), atoiMust(m[2])
		if major < minClassicAuraMajor || (major == minClassicAuraMajor && minor < minClassicAuraMinor) {
			return &VersionError{
				Got: version,
				Reason: fmt.Sprintf(
					"classic Aura versions require at least %d.%d-aura",
					minClassicAuraMajor, minClassicAuraMinor,
				),
			}
		}
		return nil
	}

	if m := calendarVersionPattern.FindStringSubmatch(version); m != nil {
		year, month := atoiMust(m[1]), atoiMust(m[2])
		if year < minCalendarYear || (year == minCalendarYear && month < minCalendarMonth) {
			return &VersionError{
				Got: version,
				Reason: fmt.Sprintf(
					"calendar-versioned releases require at least %d.%02d",
					minCalendarYear, minCalendarMonth,
				),
			}
		}
		return nil
	}

	return &VersionError{
		Got:    version,
		Reason: "unrecognized version format; expected a calendar version (e.g. \"2026.07\") or a classic Aura version (e.g. \"5.27-aura\")",
	}
}

// atoiMust parses a regexp-captured all-digit substring. The callers only
// ever pass groups matched by `\d+`/`\d{4}`/`\d{1,2}` patterns above, so a
// parse failure here would indicate the regexp itself is broken, not bad
// input — panicking surfaces that loudly during development/tests rather
// than silently miscomparing versions.
func atoiMust(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("atoiMust: %q is not all-digit despite matching a \\d+ pattern: %v", s, err))
	}
	return n
}
