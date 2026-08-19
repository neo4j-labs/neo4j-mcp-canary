// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"context"
	"fmt"
	"net/http"

	query "github.com/neo4j-contrib/query-go-sdk"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/auth"
)

// NewStaticClientFactory returns a ClientFactory that always returns the
// same client's QueryService, ignoring ctx. Use for STDIO transport mode,
// where credentials are fixed at startup (from NEO4J_USERNAME/NEO4J_PASSWORD)
// and one long-lived client is reused for the life of the process — the
// Query API equivalent of the one long-lived neo4j.Driver the Bolt path
// builds in main.go.
func NewStaticClientFactory(client *query.QueryAPIClient) ClientFactory {
	return func(context.Context) (query.QueryService, error) {
		return client.Query, nil
	}
}

// NewPerRequestClientFactory returns a ClientFactory for HTTP transport
// mode, where each MCP request carries its own Neo4j credentials (Bearer or
// Basic) that must be forwarded per call rather than fixed at startup —
// mirroring Neo4jService.getHTTPAuthToken's per-request impersonation for
// the Bolt path in internal/database/service.go.
//
// The query-go-sdk bakes credentials into the client at construction time
// (no per-call auth override like the driver's ExecuteQueryWithAuthToken),
// so each call here builds a fresh, lightweight *query.QueryAPIClient —
// cheap, since NewClient does no network I/O itself. All of them share
// httpClient so the underlying TCP/TLS connections are pooled and reused
// across requests instead of each ephemeral client paying its own
// connection-setup cost.
//
// The QueryService returned here does not retain a reference to the
// *query.QueryAPIClient it came from, and callers must not call Close() on
// it — there is nothing transport-specific to release per call, and closing
// would drain httpClient's idle connections out from under every other
// in-flight or subsequent request sharing it.
func NewPerRequestClientFactory(baseURL, database string, httpClient *http.Client) ClientFactory {
	return func(ctx context.Context) (query.QueryService, error) {
		opts := []query.Option{
			query.WithBaseURL(baseURL),
			query.WithDatabase(database),
			query.WithStreamingSupport(true),
			query.WithHTTPClient(httpClient),
		}

		if token, ok := auth.GetBearerToken(ctx); ok {
			opts = append(opts, query.WithBearerToken(token))
		} else if username, password, ok := auth.GetBasicAuthCredentials(ctx); ok {
			opts = append(opts, query.WithBasicAuth(username, password))
		} else {
			return nil, fmt.Errorf("no Neo4j credentials found on the request context for this Query API call")
		}

		client, err := query.NewClient(opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to build per-request query api client: %w", err)
		}
		return client.Query, nil
	}
}
