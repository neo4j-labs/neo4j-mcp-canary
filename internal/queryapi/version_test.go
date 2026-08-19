// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckMinimumVersion(t *testing.T) {
	tests := []struct {
		version string
		wantErr bool
	}{
		// Calendar versions.
		{"2026.07", false},
		{"2026.07.0", false},
		{"2026.08", false},
		{"2027.01", false},
		{"2026.06", true},
		{"2026.06.0", true},
		{"2025.12", true},
		// Classic Aura versions.
		{"5.27-aura", false},
		{"5.28-aura", false},
		{"6.0-aura", false},
		{"5.26-aura", true},
		{"5.1-aura", true},
		// Bare classic versions (no -aura suffix) are always rejected, even
		// when numerically >= the Aura floor.
		{"5.28", true},
		{"5.27", true},
		// Malformed / unrecognized.
		{"", true},
		{"not-a-version", true},
		{"2026", true},
		{"5.26-something", true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			err := CheckMinimumVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckMinimumVersion(%q) error = %v, wantErr %v", tt.version, err, tt.wantErr)
			}
		})
	}
}

func TestDiscoverVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(discoveryResponse{
			Neo4jVersion: "5.27-aura",
			Neo4jEdition: "enterprise",
		})
	}))
	defer server.Close()

	got, err := DiscoverVersion(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "5.27-aura" {
		t.Errorf("DiscoverVersion() = %q, want %q", got, "5.27-aura")
	}
}

func TestDiscoverVersion_MissingField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"neo4j_edition": "enterprise"}`))
	}))
	defer server.Close()

	_, err := DiscoverVersion(context.Background(), server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected an error when neo4j_version is missing, got nil")
	}
}

func TestDiscoverVersion_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	_, err := DiscoverVersion(context.Background(), server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to mention the status code, got: %v", err)
	}
}
