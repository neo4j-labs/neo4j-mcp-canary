// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package mcpsdk is the only package in this repository allowed to import an
// MCP SDK directly (currently github.com/modelcontextprotocol/go-sdk/mcp). It
// re-exposes the small slice of the SDK's API this project actually uses,
// under names stable enough that a future SDK change — the MCP spec and its
// SDKs move fast, including breaking changes — is absorbed here instead of
// rippling through every tool spec/handler and every test tier.
package mcpsdk

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tool is an MCP tool definition (name, description, input schema, hint
// annotations). Schemas are always hand-built via the With*/PropertyOption
// functions below rather than reflected from a Go struct — see the rationale
// on ReadCypherSpec/WriteCypherSpec for why reflection-based schema inference
// is deliberately avoided in this codebase.
type Tool = sdk.Tool

// ToolOption configures a Tool built by NewTool.
type ToolOption func(*Tool)

// NewTool builds a Tool with an empty object input schema, then applies opts.
func NewTool(name string, opts ...ToolOption) Tool {
	t := Tool{
		Name:        name,
		InputSchema: &jsonschema.Schema{Type: "object"},
	}
	for _, opt := range opts {
		opt(&t)
	}
	return t
}

// WithDescription sets the tool's human-readable description.
func WithDescription(description string) ToolOption {
	return func(t *Tool) { t.Description = description }
}

// propertyBuilder accumulates a single input property's schema plus whether
// it should be added to the parent schema's "required" list.
type propertyBuilder struct {
	schema   *jsonschema.Schema
	required bool
}

// PropertyOption configures a single tool input property.
type PropertyOption func(*propertyBuilder)

// Required marks the property as required on the tool's input schema.
func Required() PropertyOption {
	return func(b *propertyBuilder) { b.required = true }
}

// Description sets a property's human-readable description.
func Description(description string) PropertyOption {
	return func(b *propertyBuilder) { b.schema.Description = description }
}

// MaxLength sets a string property's maximum length.
func MaxLength(n int) PropertyOption {
	return func(b *propertyBuilder) { b.schema.MaxLength = &n }
}

// WithString declares a "string"-typed input property.
func WithString(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "string", opts) }
}

// WithObject declares an "object"-typed input property.
func WithObject(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "object", opts) }
}

func addProperty(t *Tool, name, schemaType string, opts []PropertyOption) {
	b := &propertyBuilder{schema: &jsonschema.Schema{Type: schemaType}}
	for _, opt := range opts {
		opt(b)
	}

	schema, ok := t.InputSchema.(*jsonschema.Schema)
	if !ok || schema == nil {
		schema = &jsonschema.Schema{Type: "object"}
		t.InputSchema = schema
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	schema.Properties[name] = b.schema
	if b.required {
		schema.Required = append(schema.Required, name)
	}
}

func ensureAnnotations(t *Tool) *sdk.ToolAnnotations {
	if t.Annotations == nil {
		t.Annotations = &sdk.ToolAnnotations{}
	}
	return t.Annotations
}

// WithTitleAnnotation sets the tool's display-title hint.
func WithTitleAnnotation(title string) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).Title = title }
}

// WithReadOnlyHintAnnotation hints whether the tool modifies its environment.
func WithReadOnlyHintAnnotation(v bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).ReadOnlyHint = v }
}

// WithDestructiveHintAnnotation hints whether the tool may perform destructive updates.
func WithDestructiveHintAnnotation(v bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).DestructiveHint = &v }
}

// WithIdempotentHintAnnotation hints whether repeated calls with the same
// arguments have no additional effect.
func WithIdempotentHintAnnotation(v bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).IdempotentHint = v }
}

// WithOpenWorldHintAnnotation hints whether the tool interacts with an open
// world of external entities.
func WithOpenWorldHintAnnotation(v bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).OpenWorldHint = &v }
}
