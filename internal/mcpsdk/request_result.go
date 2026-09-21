// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package mcpsdk

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallToolParams describes a single tool invocation as seen by a handler.
// Arguments is decoded into a plain map[string]any (rather than the SDK's
// json.RawMessage) so BindArguments can do the same JSON round-trip through
// map[string]any that this codebase's custom UnmarshalJSON types (see
// cypher.Params) were built against.
type CallToolParams struct {
	Name      string
	Arguments map[string]any
}

// CallToolRequest is the request passed to a ToolHandlerFunc.
type CallToolRequest struct {
	Params *CallToolParams
}

// BindArguments decodes the request's arguments into v. It round-trips
// through JSON (marshal the arguments map, then unmarshal into v) rather than
// doing a direct type conversion, so that json-tagged target types and custom
// json.Unmarshaler implementations (e.g. cypher.Params) behave exactly as if
// the arguments had arrived over the wire as JSON.
func (r *CallToolRequest) BindArguments(v any) error {
	var arguments map[string]any
	if r.Params != nil {
		arguments = r.Params.Arguments
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal arguments: %w", err)
	}
	return nil
}

// ToolHandlerFunc is the signature every MCP tool handler implements.
//
// A returned Go error is a protocol-level failure. Business-logic failures
// are reported by returning a result built with NewToolResultError (IsError
// set, err == nil) — the two channels are independent, matching this
// codebase's existing handler convention throughout.
type ToolHandlerFunc func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error)

// CallToolResult, Content, and TextContent are aliased directly from the SDK:
// consumers only ever construct them via NewToolResultText/NewToolResultError
// and read them back via AsTextContent, so no independent wrapper type is
// needed and the alias keeps `result.IsError`/`result.Content` field access
// working unchanged at every call site.
type CallToolResult = sdk.CallToolResult
type Content = sdk.Content
type TextContent = sdk.TextContent

// NewToolResultText builds a successful text-content result.
func NewToolResultText(text string) *CallToolResult {
	return &CallToolResult{Content: []Content{&TextContent{Text: text}}}
}

// NewToolResultTextAndStructured builds a successful result carrying both
// the text block every client can read and, per MCP's structured-output
// extension (see the tool's OutputSchema, set via WithOutputSchema), a
// schema-described JSON value clients can consume programmatically instead
// of re-parsing text. structured is typically a json.RawMessage wrapping a
// JSON string the caller already produced, which marshals onto the wire
// verbatim with no extra encode/decode pass.
func NewToolResultTextAndStructured(text string, structured any) *CallToolResult {
	return &CallToolResult{
		Content:           []Content{&TextContent{Text: text}},
		StructuredContent: structured,
	}
}

// NewToolResultError builds a business-logic error result (IsError set,
// no Go error returned) — the tool call still succeeds at the protocol level.
func NewToolResultError(text string) *CallToolResult {
	return &CallToolResult{IsError: true, Content: []Content{&TextContent{Text: text}}}
}

// AsTextContent type-asserts a Content value to *TextContent.
func AsTextContent(c Content) (*TextContent, bool) {
	tc, ok := c.(*TextContent)
	return tc, ok
}

// decodeArguments unmarshals the SDK's raw wire arguments into a plain map.
// An empty/absent payload decodes to an empty (non-nil) map so handlers never
// need a nil check before indexing request.Params.Arguments.
func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	return args, nil
}
