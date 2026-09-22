// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package readiness

import (
	"context"
	"log"
	"regexp"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/queryapi"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// minSearchCalendarYear/Month and minSearchClassicAuraMajor/Minor are the
// version floor tools.CategorySearch's SEARCH-clause syntax requires:
// calendar >= 2026.09.0 (native full-text SEARCH support), or classic Aura
// >= 5.27-aura. Fixed source constants, not configurable — like the Query
// API's own floor (internal/queryapi/version.go), this is a hard Cypher
// grammar requirement, not an operator policy knob.
const (
	minSearchCalendarYear     = 2026
	minSearchCalendarMonth    = 9
	minSearchClassicAuraMajor = 5
	minSearchClassicAuraMinor = 27
)

// bareClassicVersionPattern matches a classic Neo4j version with no "-aura"
// suffix, e.g. "5.27" or "5.27.0" — the shape CALL dbms.components() always
// reports, even for Aura-classic instances (the "-aura" marker only ever
// appears in the Query API's HTTP discovery endpoint response, a different
// code path entirely — see the doc comment on the Aura-normalization step
// in Verify below).
var bareClassicVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.\d+)?$`)

// Result reports what Checker.Verify discovered about the connected Neo4j
// instance.
type Result struct {
	// GDSInstalled reports whether the Graph Data Science plugin is
	// available. GDS is optional, so its absence is not an error.
	GDSInstalled bool
	// SearchVersionSupported reports whether the connected server clears
	// the version floor tools.CategorySearch requires (see
	// minSearchCalendarYear/Month and minSearchClassicAuraMajor/Minor
	// above).
	SearchVersionSupported bool
}

// Checker verifies that a Neo4j connection is ready for the server to use.
// It is transport-agnostic: it only calls database.Service, so it behaves
// identically whether that service is Bolt- or Query-API-backed.
type Checker struct {
	db  database.Service
	uri string
}

// NewChecker creates a Checker backed by the given database.Service. uri is
// the configured Neo4j connection URI (Config.URI) — needed only to detect
// whether the connected instance is Aura-managed, for the search-version
// check's classic-Aura normalization; it is never used to connect.
func NewChecker(db database.Service, uri string) *Checker {
	return &Checker{db: db, uri: uri}
}

// Verify checks the Neo4j requirements:
//   - A valid connection with a Neo4j instance.
//   - The ability to perform a read query (database name is correctly defined).
//   - In case GDS is not installed, Result.GDSInstalled is false rather than an error.
//   - Whether the connected server's version clears the search category's floor.
func (c *Checker) Verify(ctx context.Context) (Result, error) {
	if err := c.db.VerifyConnectivity(ctx); err != nil {
		return Result{}, err
	}

	result := Result{
		GDSInstalled:           c.checkGDSInstalled(ctx),
		SearchVersionSupported: c.checkSearchVersionSupported(ctx),
	}
	return result, nil
}

// checkGDSInstalled calls gds.version() to determine if GDS is installed.
// GDS is optional, so a query failure just means "not installed", not an
// error — logged and swallowed, same as before this function was split out
// of Verify.
func (c *Checker) checkGDSInstalled(ctx context.Context) bool {
	records, err := c.db.ExecuteReadQuery(ctx, "RETURN gds.version() as gdsVersion", nil)
	if err != nil {
		log.Print("Impossible to verify GDS installation.")
		return false
	}

	if len(records) == 1 && len(records[0].Values) == 1 {
		if _, ok := records[0].Values[0].(string); ok {
			return true
		}
	}
	return false
}

// checkSearchVersionSupported runs CALL dbms.components() — the same
// transport-agnostic query internal/eventing already uses for telemetry —
// and compares the "Neo4j Kernel" version against the search category's
// floor. Fails closed (false) on any query error or unparseable version,
// same failure posture as checkGDSInstalled.
//
// Aura normalization: dbms.components() never reports the "-aura" suffix
// database.MeetsMinimumVersion's classic-floor branch requires — that
// marker only appears in the Query API's separate HTTP discovery response.
// So a real Aura-classic instance's bare version (e.g. "5.27.0") is
// synthesized into the "-aura" shape ("5.27-aura") before comparing, but
// only when the connection URI itself is confirmed Aura-managed —
// otherwise a self-managed classic server could falsely claim the Aura
// floor.
func (c *Checker) checkSearchVersionSupported(ctx context.Context) bool {
	records, err := c.db.ExecuteReadQuery(ctx, "CALL dbms.components()", map[string]any{})
	if err != nil {
		log.Print("Impossible to verify Neo4j server version for search-tool support.")
		return false
	}

	version := kernelVersion(records)
	if version == "" {
		return false
	}

	if queryapi.IsAuraHost(c.uri) {
		if m := bareClassicVersionPattern.FindStringSubmatch(version); m != nil {
			version = m[1] + "." + m[2] + "-aura"
		}
	}

	ok, _ := database.MeetsMinimumVersion(version, minSearchCalendarYear, minSearchCalendarMonth, minSearchClassicAuraMajor, minSearchClassicAuraMinor)
	return ok
}

// kernelVersion extracts the "Neo4j Kernel" row's version string from
// dbms.components() records — the same extraction
// internal/eventing.recordsToConnectionEventInfo already performs (for
// telemetry, not gating) as part of a wider per-component switch;
// duplicated here rather than shared since this is the only field this
// package needs and the two call sites' purposes don't otherwise overlap.
func kernelVersion(records []*neo4j.Record) string {
	for _, record := range records {
		nameRaw, ok := record.Get("name")
		if !ok {
			continue
		}
		name, ok := nameRaw.(string)
		if !ok || name != "Neo4j Kernel" {
			continue
		}
		versionsRaw, ok := record.Get("versions")
		if !ok {
			continue
		}
		versions, ok := versionsRaw.([]any)
		if !ok || len(versions) == 0 {
			continue
		}
		if v, ok := versions[0].(string); ok {
			return v
		}
	}
	return ""
}
