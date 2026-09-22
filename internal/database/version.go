// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import (
	"fmt"
	"regexp"
	"strconv"
)

// classicAuraVersionPattern matches classic Neo4j versions reported by
// Aura, e.g. "5.26-aura", "5.27-aura".
var classicAuraVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)-aura$`)

// calendarVersionPattern matches calendar-versioned Neo4j releases, e.g.
// "2026.07", "2026.07.0". The patch component is optional and ignored —
// the year/month pair alone is sufficient to compare against a floor.
var calendarVersionPattern = regexp.MustCompile(`^(\d{4})\.(\d{1,2})(?:\.\d+)?$`)

// MeetsMinimumVersion reports whether version clears the given floor.
// Calendar-versioned releases (e.g. "2026.09", "2026.09.0") are compared as
// (year, month) against (minCalendarYear, minCalendarMonth). Classic Aura
// releases (e.g. "5.27-aura") are compared as (major, minor) against
// (minClassicAuraMajor, minClassicAuraMinor). Anything else — including a
// bare classic version with no "-aura" suffix (e.g. "5.27"), even if
// numerically above the floor — does not meet it: callers that need to
// treat a bare classic version as Aura must synthesize the "-aura" suffix
// themselves before calling this function (see
// internal/readiness.Checker.Verify for why that's a caller-side decision,
// not something this comparator can infer from the string alone).
//
// ok is false with a non-empty reason on rejection, explaining which rule
// applied; ok is true with an empty reason on acceptance.
func MeetsMinimumVersion(version string, minCalendarYear, minCalendarMonth, minClassicAuraMajor, minClassicAuraMinor int) (ok bool, reason string) {
	// calendarVersionPattern is checked first because it's unambiguous (an
	// exact 4-digit year) — classicAuraVersionPattern's unanchored \d+ major
	// would otherwise also match a calendar-shaped string were the "-aura"
	// suffix ever optional; kept first defensively even though the current
	// pattern requires the suffix.
	if m := calendarVersionPattern.FindStringSubmatch(version); m != nil {
		year, month := atoiMust(m[1]), atoiMust(m[2])
		if year < minCalendarYear || (year == minCalendarYear && month < minCalendarMonth) {
			return false, fmt.Sprintf(
				"calendar-versioned releases require at least %d.%02d",
				minCalendarYear, minCalendarMonth,
			)
		}
		return true, ""
	}

	if m := classicAuraVersionPattern.FindStringSubmatch(version); m != nil {
		major, minor := atoiMust(m[1]), atoiMust(m[2])
		if major < minClassicAuraMajor || (major == minClassicAuraMajor && minor < minClassicAuraMinor) {
			return false, fmt.Sprintf(
				"classic Aura versions require at least %d.%d-aura",
				minClassicAuraMajor, minClassicAuraMinor,
			)
		}
		return true, ""
	}

	return false, "unrecognized version format; expected a calendar version (e.g. \"2026.09\") or a classic Aura version (e.g. \"5.27-aura\")"
}

// atoiMust parses a regexp-captured all-digit substring. Callers only ever
// pass groups matched by \d+/\d{4}/\d{1,2} patterns above, so a parse
// failure here would indicate the regexp itself is broken, not bad input —
// panicking surfaces that loudly during development/tests rather than
// silently miscomparing versions.
func atoiMust(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("atoiMust: %q is not all-digit despite matching a \\d+ pattern: %v", s, err))
	}
	return n
}
