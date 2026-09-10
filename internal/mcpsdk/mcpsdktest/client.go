// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package mcpsdktest is a thin MCP client wrapper used only by this
// repository's own test suites (unit, integration, e2e) to drive a real
// Neo4jMCPServer over stdio or streamable HTTP. It exists for the same
// reason internal/mcpsdk exists: so a future MCP SDK change is absorbed in
// one place instead of in every test file.
package mcpsdktest

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool, InitializeResult, ListToolsResult, CallToolResult, Content, and
// TextContent are aliased from the SDK so that test code driving a Client
// never needs to import an MCP SDK directly — mcpsdktest and mcpsdk are the
// only two packages in this repository allowed to do that.
type Tool = sdk.Tool
type InitializeResult = sdk.InitializeResult
type ListToolsResult = sdk.ListToolsResult
type CallToolResult = sdk.CallToolResult
type Content = sdk.Content
type TextContent = sdk.TextContent

// AsTextContent type-asserts a Content value to *TextContent.
func AsTextContent(c Content) (*TextContent, bool) {
	tc, ok := c.(*TextContent)
	return tc, ok
}

// Client is a minimal MCP client sufficient for this repo's test suites:
// connect (which performs the initialize handshake), list tools, call a
// tool, and close.
type Client struct {
	inner     *sdk.Client
	transport sdk.Transport
	session   *sdk.ClientSession
}

// NewStdioClient builds a client that will run binPath as a subprocess and
// speak MCP over its stdin/stdout, for use by the e2e suite.
func NewStdioClient(clientName, clientVersion, binPath string, args []string) *Client {
	return &Client{
		inner:     sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil),
		transport: &sdk.CommandTransport{Command: exec.Command(binPath, args...)},
	}
}

// HTTPClientOption configures the HTTP transport built by NewHTTPClient.
type HTTPClientOption func(*sdk.StreamableClientTransport)

// WithHTTPHeader adds a fixed header (e.g. Authorization) to every request
// the client makes. Safe to call more than once to set multiple headers.
func WithHTTPHeader(key, value string) HTTPClientOption {
	return func(t *sdk.StreamableClientTransport) {
		if t.HTTPClient == nil {
			t.HTTPClient = &http.Client{}
		}
		rt, ok := t.HTTPClient.Transport.(*headerRoundTripper)
		if !ok {
			rt = &headerRoundTripper{header: http.Header{}, base: t.HTTPClient.Transport}
			t.HTTPClient.Transport = rt
		}
		rt.header.Set(key, value)
	}
}

type headerRoundTripper struct {
	header http.Header
	base   http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, vv := range h.header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// NewHTTPClient builds a client that speaks MCP over streamable HTTP against url.
func NewHTTPClient(clientName, clientVersion, url string, opts ...HTTPClientOption) *Client {
	t := &sdk.StreamableClientTransport{Endpoint: url}
	for _, opt := range opts {
		opt(t)
	}
	return &Client{
		inner:     sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil),
		transport: t,
	}
}

// Initialize connects the client and performs the MCP handshake. The
// official SDK folds the handshake into Connect itself, so this is
// effectively "connect if not already connected".
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	session, err := c.inner.Connect(ctx, c.transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	c.session = session
	return session.InitializeResult(), nil
}

// ListTools lists every tool the server currently advertises.
func (c *Client) ListTools(ctx context.Context) (*ListToolsResult, error) {
	return c.session.ListTools(ctx, nil)
}

// CallTool invokes a tool by name with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*CallToolResult, error) {
	return c.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
}

// Close ends the client session.
func (c *Client) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}
