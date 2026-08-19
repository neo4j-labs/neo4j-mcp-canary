// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import "testing"

func TestDetectMode(t *testing.T) {
	tests := []struct {
		uri  string
		want Mode
	}{
		{"http://localhost:7474", ModeQueryAPI},
		{"https://cab50b33.databases.neo4j.io", ModeQueryAPI},
		{"HTTP://localhost:7474", ModeQueryAPI},
		{"HTTPS://localhost:7474", ModeQueryAPI},
		{"bolt://localhost:7687", ModeBolt},
		{"bolt+s://localhost:7687", ModeBolt},
		{"neo4j://localhost:7687", ModeBolt},
		{"neo4j+s://cab50b33.databases.neo4j.io:7687", ModeBolt},
		{"neo4j+ssc://localhost:7687", ModeBolt},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got, err := DetectMode(tt.uri)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("DetectMode(%q) = %v, want %v", tt.uri, got, tt.want)
			}
		})
	}
}

func TestDetectMode_InvalidURI(t *testing.T) {
	// A control character in the URI is rejected by url.Parse.
	_, err := DetectMode("bolt://local\thost:7687")
	if err == nil {
		t.Fatal("expected an error for a malformed URI, got nil")
	}
}
