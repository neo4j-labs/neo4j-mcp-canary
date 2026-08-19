// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
)

func TestNewStaticClientFactory(t *testing.T) {
	client, err := query.NewClient(
		query.WithBasicAuth("neo4j", "password"),
		query.WithBaseURL("http://localhost:7474"),
		query.WithStreamingSupport(true),
	)
	if err != nil {
		t.Fatalf("query.NewClient: %v", err)
	}
	defer client.Close()

	factory := NewStaticClientFactory(client)

	got, err := factory(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if got != client.Query {
		t.Error("expected the static factory to always return the same client.Query instance")
	}

	// Confirm it really is ctx-independent by calling with a context that
	// carries different (irrelevant) values.
	ctxWithAuth := auth.WithBasicAuth(context.Background(), "someone-else", "irrelevant")
	got2, err := factory(ctxWithAuth)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if got2 != client.Query {
		t.Error("expected the static factory to ignore ctx")
	}
}

func TestNewPerRequestClientFactory_NoCredentials(t *testing.T) {
	factory := NewPerRequestClientFactory("http://localhost:7474", "neo4j", http.DefaultClient)

	_, err := factory(context.Background())
	if err == nil {
		t.Fatal("expected an error when ctx carries no credentials, got nil")
	}
}

func TestNewPerRequestClientFactory_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "s3cret" {
			t.Errorf("unexpected/missing basic auth on request: ok=%v user=%q pass=%q", ok, user, pass)
		}
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	}))
	defer server.Close()

	factory := NewPerRequestClientFactory(server.URL, "neo4j", server.Client())

	ctx := auth.WithBasicAuth(context.Background(), "alice", "s3cret")
	svc, err := factory(ctx)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}

	stream, err := svc.ExecuteStream(ctx, "RETURN 1", nil)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	for _, recErr := range stream.Records() {
		if recErr != nil {
			t.Fatalf("unexpected record error: %v", recErr)
		}
	}
}

func TestNewPerRequestClientFactory_BearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer my-token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer my-token")
		}
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	}))
	defer server.Close()

	factory := NewPerRequestClientFactory(server.URL, "neo4j", server.Client())

	ctx := auth.WithBearerToken(context.Background(), "my-token")
	svc, err := factory(ctx)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}

	stream, err := svc.ExecuteStream(ctx, "RETURN 1", nil)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	for _, recErr := range stream.Records() {
		if recErr != nil {
			t.Fatalf("unexpected record error: %v", recErr)
		}
	}
}

func TestNewPerRequestClientFactory_SharesHTTPClient(t *testing.T) {
	// Two ephemeral clients built by the same factory, given the same
	// *http.Client, should reuse its transport/connection pool rather than
	// each constructing their own. We can't observe pooling directly, but we
	// can confirm both requests succeed against the same shared client and
	// that the factory doesn't panic/error when called twice in a row.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeStream(w,
			wireEvent{Event: "Header", Body: map[string]any{"fields": []string{}}},
			wireEvent{Event: "Summary", Body: map[string]any{"queryType": "r"}},
		)
	}))
	defer server.Close()

	shared := server.Client()
	factory := NewPerRequestClientFactory(server.URL, "neo4j", shared)
	ctx := auth.WithBasicAuth(context.Background(), "neo4j", "password")

	for i := 0; i < 2; i++ {
		svc, err := factory(ctx)
		if err != nil {
			t.Fatalf("factory call %d: %v", i, err)
		}
		stream, err := svc.ExecuteStream(ctx, "RETURN 1", nil)
		if err != nil {
			t.Fatalf("ExecuteStream call %d: %v", i, err)
		}
		for _, recErr := range stream.Records() {
			if recErr != nil {
				t.Fatalf("call %d: unexpected record error: %v", i, recErr)
			}
		}
	}
}
