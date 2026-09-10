// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// JSON-RPC method names used to scope the generic method middleware below to
// specific handshake/call points. These mirror the mark3labs SDK's typed
// before/after-tool hooks, which the official SDK does not provide directly —
// instead it exposes a single generic method-level middleware chain
// (Server.AddReceivingMiddleware) that every method flows through, so the
// hook-like behavior this server needs is implemented here by scoping that
// middleware to a method name.
const (
	methodInitialize = "initialize"
	methodDiscover   = "server/discover"
	methodCallTool   = "tools/call"
)

// OnAfterCallTool registers fn to run after every tool call completes,
// regardless of success/failure. result is nil only if the call failed at the
// protocol level (fn is not invoked for calls to unknown tools, etc. — those
// never reach the "tools/call" method handler with a CallToolRequest).
func (s *Server) OnAfterCallTool(fn func(ctx context.Context, request *CallToolRequest, result *CallToolResult)) {
	s.inner.AddReceivingMiddleware(afterCallToolMiddleware(fn))
}

// OnBeforeInitialize registers fn to run before the server handles a classic
// "initialize" handshake request.
func (s *Server) OnBeforeInitialize(fn func(ctx context.Context)) {
	s.inner.AddReceivingMiddleware(beforeMethodMiddleware(methodInitialize, fn))
}

// OnBeforeDiscover registers fn to run before the server handles the newer
// stateless "discover" handshake request (protocol version 2026-07-28+).
func (s *Server) OnBeforeDiscover(fn func(ctx context.Context)) {
	s.inner.AddReceivingMiddleware(beforeMethodMiddleware(methodDiscover, fn))
}

func beforeMethodMiddleware(method string, fn func(ctx context.Context)) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			if m == method {
				fn(ctx)
			}
			return next(ctx, m, req)
		}
	}
}

func afterCallToolMiddleware(fn func(ctx context.Context, request *CallToolRequest, result *CallToolResult)) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, m string, req sdk.Request) (sdk.Result, error) {
			result, err := next(ctx, m, req)
			if m == methodCallTool {
				if ctr, ok := req.(*sdk.CallToolRequest); ok {
					var res *CallToolResult
					if result != nil {
						res, _ = result.(*CallToolResult)
					}
					args, decErr := decodeArguments(ctr.Params.Arguments)
					if decErr != nil {
						args = map[string]any{}
					}
					wrapped := &CallToolRequest{Params: &CallToolParams{Name: ctr.Params.Name, Arguments: args}}
					fn(ctx, wrapped, res)
				}
			}
			return result, err
		}
	}
}
