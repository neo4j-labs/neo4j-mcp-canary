// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the underlying MCP SDK server, exposing only the operations
// this codebase uses: tool registration, stdio/HTTP transports, and the
// tool-call/handshake hooks used for deferred DB verification and analytics.
type Server struct {
	inner *sdk.Server

	mu    sync.Mutex
	tools map[string]Tool
}

// ServerOption configures a Server built by NewServer.
type ServerOption func(*sdk.ServerOptions)

// WithInstructions sets the server's advertised usage instructions.
func WithInstructions(instructions string) ServerOption {
	return func(o *sdk.ServerOptions) { o.Instructions = instructions }
}

// NewServer creates a new MCP server. The tools capability is advertised
// automatically once at least one tool has been added — there is no need to
// opt in explicitly (unlike some MCP SDKs).
func NewServer(name, version string, opts ...ServerOption) *Server {
	so := &sdk.ServerOptions{}
	for _, opt := range opts {
		opt(so)
	}
	return &Server{
		inner: sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, so),
		tools: make(map[string]Tool),
	}
}

// ServerTool pairs a Tool definition with its handler, mirroring the batch
// registration shape used by tools_register.go.
type ServerTool struct {
	Tool    Tool
	Handler ToolHandlerFunc
}

// AddTool registers (or replaces) a single tool.
func (s *Server) AddTool(t Tool, h ToolHandlerFunc) {
	s.mu.Lock()
	s.tools[t.Name] = t
	s.mu.Unlock()

	tt := t
	s.inner.AddTool(&tt, adaptHandler(h))
}

// AddTools registers (or replaces) a batch of tools. Calling it more than
// once with overlapping tool names is safe — a later AddTool/AddTools call
// simply replaces the earlier definition for that name.
func (s *Server) AddTools(defs ...ServerTool) {
	for _, d := range defs {
		s.AddTool(d.Tool, d.Handler)
	}
}

// ListTools returns every tool currently registered on this server. Backed by
// our own bookkeeping (rather than a live protocol round-trip) so it can be
// used synchronously, e.g. by tests asserting on conditional tool registration.
func (s *Server) ListTools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	return out
}

// ServeStdio runs the server over stdio until ctx is cancelled or the session
// ends. A closed/exhausted stdin (an unattended stdio session, or a test
// harness that doesn't attach one) is treated as a clean shutdown rather than
// an error, matching how a stdio MCP server is expected to behave when its
// input stream simply ends.
func (s *Server) ServeStdio(ctx context.Context) error {
	err := s.inner.Run(ctx, &sdk.StdioTransport{})
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// HTTPOptions configures the HTTP transport handler returned by HTTPHandler.
type HTTPOptions struct {
	// Stateless disables session-ID tracking: every request is handled
	// independently, with no server-to-client requests. This is what a
	// simple multi-tenant tool server wants.
	Stateless bool
}

// HTTPHandler returns a plain http.Handler serving the streamable-HTTP MCP
// transport. Callers are free to wrap it with their own net/http middleware
// (auth, CORS, logging, ...) exactly as with any other http.Handler.
func (s *Server) HTTPHandler(opts HTTPOptions) http.Handler {
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s.inner },
		&sdk.StreamableHTTPOptions{Stateless: opts.Stateless},
	)
}

func adaptHandler(h ToolHandlerFunc) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args, err := decodeArguments(req.Params.Arguments)
		if err != nil {
			return NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		wrapped := &CallToolRequest{Params: &CallToolParams{Name: req.Params.Name, Arguments: args}}
		return h(ctx, wrapped)
	}
}
