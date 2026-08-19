// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

func TestEncodeOutput_JSONPassesThroughUnchanged(t *testing.T) {
	input := `{"rows":[{"name":"Alice"}],"rowCount":1,"truncated":false}`

	got, err := EncodeOutput(input, config.OutputFormatJSON)
	if err != nil {
		t.Fatalf("EncodeOutput() error = %v", err)
	}
	if got != input {
		t.Errorf("EncodeOutput() = %q, want unchanged %q", got, input)
	}
}

func TestEncodeOutput_TOONConvertsTabularRows(t *testing.T) {
	input := `{"rows":[{"age":30,"name":"Alice"},{"age":25,"name":"Bob"}],"rowCount":2,"truncated":false}`

	got, err := EncodeOutput(input, config.OutputFormatTOON)
	if err != nil {
		t.Fatalf("EncodeOutput() error = %v", err)
	}

	want := "rowCount: 2\nrows[2]{age,name}:\n  30,Alice\n  25,Bob\ntruncated: false"
	if got != want {
		t.Errorf("EncodeOutput() = %q, want %q", got, want)
	}
}

func TestEncodeOutput_TOONInvalidJSONErrors(t *testing.T) {
	_, err := EncodeOutput("not json", config.OutputFormatTOON)
	if err == nil {
		t.Fatal("EncodeOutput() error = nil, want error for invalid JSON input")
	}
}
