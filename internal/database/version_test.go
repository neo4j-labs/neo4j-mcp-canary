// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package database

import "testing"

// TestMeetsMinimumVersion_QueryAPIFloor ports queryapi/version_test.go's
// TestCheckMinimumVersion table verbatim against the generalized comparator
// at the same floor (2026.07 / 5.26-aura) — the regression proof that
// moving/generalizing the comparator preserved behavior exactly.
func TestMeetsMinimumVersion_QueryAPIFloor(t *testing.T) {
	tests := []struct {
		version string
		wantOK  bool
	}{
		// Calendar versions.
		{"2026.07", true},
		{"2026.07.0", true},
		{"2026.08", true},
		{"2027.01", true},
		{"2026.06", false},
		{"2026.06.0", false},
		{"2025.12", false},
		// Classic Aura versions.
		{"5.26-aura", true},
		{"5.27-aura", true},
		{"6.0-aura", true},
		{"5.25-aura", false},
		{"5.1-aura", false},
		// Bare classic versions (no -aura suffix) are always rejected, even
		// when numerically >= the Aura floor.
		{"5.27", false},
		{"5.26", false},
		{"5.26.30", false},
		// Malformed / unrecognized.
		{"", false},
		{"not-a-version", false},
		{"2026", false},
		{"5.26-something", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			ok, reason := MeetsMinimumVersion(tt.version, 2026, 7, 5, 26)
			if ok != tt.wantOK {
				t.Errorf("MeetsMinimumVersion(%q) ok = %v (reason %q), want %v", tt.version, ok, reason, tt.wantOK)
			}
			if !ok && reason == "" {
				t.Errorf("MeetsMinimumVersion(%q) rejected with no reason", tt.version)
			}
		})
	}
}

// TestMeetsMinimumVersion_SearchFloor exercises the search category's own
// floor (2026.09 / 5.27-aura) to confirm the comparator generalizes to a
// different floor, not just the Query API's original constants.
func TestMeetsMinimumVersion_SearchFloor(t *testing.T) {
	tests := []struct {
		version string
		wantOK  bool
	}{
		{"2026.09", true},
		{"2026.09.0", true},
		{"2026.10", true},
		{"2027.01", true},
		{"2026.08", false},
		{"5.27-aura", true},
		{"5.28-aura", true},
		{"5.26-aura", false},
		{"5.27", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			ok, _ := MeetsMinimumVersion(tt.version, 2026, 9, 5, 27)
			if ok != tt.wantOK {
				t.Errorf("MeetsMinimumVersion(%q, floor=2026.09/5.27-aura) = %v, want %v", tt.version, ok, tt.wantOK)
			}
		})
	}
}
