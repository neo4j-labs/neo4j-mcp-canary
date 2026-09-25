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
	"fmt"

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

// WithOutputSchema declares the JSON Schema describing the tool's
// structured output (see NewToolResultTextAndStructured). This is purely
// advisory metadata for clients — this codebase's tool registration path
// (Server.AddTool, not the SDK's generic AddTool[In,Out]) never validates a
// handler's CallToolResult.StructuredContent against it.
func WithOutputSchema(schema *jsonschema.Schema) ToolOption {
	return func(t *Tool) { t.OutputSchema = schema }
}

// MustOutputSchemaFor reflects a JSON Schema for T, for use with
// WithOutputSchema. Intended to be called once, at package init, over a
// fixed Go type — a failure here is a programming error (an unreflectable
// type), not a runtime condition, hence the panic rather than an error return.
func MustOutputSchemaFor[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpsdk: failed to reflect output schema for %T: %v", *new(T), err))
	}
	splitNullableTypesEverywhere(schema)
	return schema
}

// splitNullableTypesEverywhere walks every schema node reachable from root
// and rewrites a multi-value Types (e.g. ["null", "array"] — what
// jsonschema-go's reflector emits for every Go slice field, since a nil
// slice is valid JSON null) into an equivalent anyOf of single-type
// branches (e.g. anyOf: [{type: null}, {type: array}]).
//
// Both spellings are valid JSON Schema and mean the same thing, but a
// multi-value "type" is less portable: a client that maps tool schemas onto
// a single-type dialect (such as the OpenAPI subset used for Gemini function
// declarations) may reject the tool or silently drop the constraint. Sibling
// keywords (e.g. "items" on a nullable array) are left in place rather than
// copied into the anyOf branches — per JSON Schema semantics they're simply
// not evaluated against an instance of the "wrong" shape (a "null" instance
// never has to satisfy "items"), so leaving them where they are is both
// correct and simpler than duplicating them per branch.
func splitNullableTypesEverywhere(root *jsonschema.Schema) {
	walkSchemas(root, make(map[*jsonschema.Schema]bool), splitNullableTypes)
}

// splitNullableTypes rewrites one schema node in place; see
// splitNullableTypesEverywhere for the rationale.
func splitNullableTypes(s *jsonschema.Schema) {
	if len(s.Types) < 2 {
		return
	}

	hasNull := false
	others := make([]string, 0, len(s.Types))
	for _, t := range s.Types {
		if t == "null" {
			hasNull = true
		} else {
			others = append(others, t)
		}
	}
	if !hasNull || len(others) == 0 {
		return
	}

	anyOf := make([]*jsonschema.Schema, 0, len(others)+1)
	anyOf = append(anyOf, &jsonschema.Schema{Type: "null"})
	for _, t := range others {
		anyOf = append(anyOf, &jsonschema.Schema{Type: t})
	}
	s.Types = nil
	s.AnyOf = anyOf
}

// walkSchemas calls fn on every *jsonschema.Schema reachable from s (s
// itself included), following every field that can hold a nested schema.
// seen guards against revisiting a node reachable through more than one
// path (e.g. a $defs entry also referenced elsewhere), not against a true
// reference cycle — $ref by name doesn't hold a live *Schema pointer here,
// so no cycle-through-$ref is possible, but defensive dedup costs nothing.
func walkSchemas(s *jsonschema.Schema, seen map[*jsonschema.Schema]bool, fn func(*jsonschema.Schema)) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	fn(s)

	for _, child := range s.Properties {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.PatternProperties {
		walkSchemas(child, seen, fn)
	}
	walkSchemas(s.AdditionalProperties, seen, fn)
	walkSchemas(s.PropertyNames, seen, fn)
	walkSchemas(s.UnevaluatedProperties, seen, fn)

	walkSchemas(s.Items, seen, fn)
	for _, child := range s.PrefixItems {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.ItemsArray {
		walkSchemas(child, seen, fn)
	}
	walkSchemas(s.AdditionalItems, seen, fn)
	walkSchemas(s.Contains, seen, fn)
	walkSchemas(s.UnevaluatedItems, seen, fn)

	for _, child := range s.AllOf {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.AnyOf {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.OneOf {
		walkSchemas(child, seen, fn)
	}
	walkSchemas(s.Not, seen, fn)

	walkSchemas(s.If, seen, fn)
	walkSchemas(s.Then, seen, fn)
	walkSchemas(s.Else, seen, fn)
	for _, child := range s.DependentSchemas {
		walkSchemas(child, seen, fn)
	}

	for _, child := range s.Defs {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.Definitions {
		walkSchemas(child, seen, fn)
	}
	for _, child := range s.DependencySchemas {
		walkSchemas(child, seen, fn)
	}

	walkSchemas(s.ContentSchema, seen, fn)
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

// Enum restricts a property to one of the given values.
func Enum(values ...string) PropertyOption {
	return func(b *propertyBuilder) {
		enum := make([]any, len(values))
		for i, v := range values {
			enum[i] = v
		}
		b.schema.Enum = enum
	}
}

// WithString declares a "string"-typed input property.
func WithString(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "string", nil, opts) }
}

// WithObject declares an "object"-typed input property.
func WithObject(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "object", nil, opts) }
}

// WithArray declares an "array"-typed input property whose items are of
// itemType (for example "string").
func WithArray(name, itemType string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) {
		addProperty(t, name, "array", &jsonschema.Schema{Type: itemType}, opts)
	}
}

// WithInteger declares an "integer"-typed input property.
func WithInteger(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "integer", nil, opts) }
}

// WithBoolean declares a "boolean"-typed input property.
func WithBoolean(name string, opts ...PropertyOption) ToolOption {
	return func(t *Tool) { addProperty(t, name, "boolean", nil, opts) }
}

func addProperty(t *Tool, name, schemaType string, items *jsonschema.Schema, opts []PropertyOption) {
	b := &propertyBuilder{schema: &jsonschema.Schema{Type: schemaType, Items: items}}
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
