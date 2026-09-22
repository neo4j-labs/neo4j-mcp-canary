// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// CreateConstraintInput is the struct the handler binds incoming arguments
// into via request.BindArguments. The JSON schema advertised to MCP clients
// is declared explicitly in CreateConstraintSpec below, following the same
// hand-built-schema convention as every other tool in this package.
type CreateConstraintInput struct {
	Name             string   `json:"name,omitempty"`
	EntityType       string   `json:"entityType"`
	Label            string   `json:"label,omitempty"`
	RelationshipType string   `json:"relationshipType,omitempty"`
	Properties       []string `json:"properties"`
	ConstraintType   string   `json:"constraintType"`
}

// createConstraintOutputSchema is computed once at package init since
// CreateConstraintOutput's shape never changes between calls.
var createConstraintOutputSchema = mcpsdk.MustOutputSchemaFor[CreateConstraintOutput]()

// CreateConstraintSpec declares the MCP tool schema for create-constraint.
//
// Every input is a structured field and the output is a structured
// confirmation object — this tool never accepts or returns raw Cypher; the
// generated statement is a pure internal implementation detail.
//
// UNIQUENESS is supported on every edition of Neo4j; KEY and
// PROPERTY_EXISTENCE constraints are Enterprise-only features. This server
// has no reliable way to detect the edition ahead of time, so a KEY or
// PROPERTY_EXISTENCE request against a Community Edition database will be
// rejected by the server itself at execution time — that rejection is
// surfaced as this tool's error. Do not retry a KEY or PROPERTY_EXISTENCE
// constraint in a loop after such a failure; treat it as a hard edition
// limitation.
func CreateConstraintSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("create-constraint",
		mcpsdk.WithDescription(`
		Create a constraint on the Neo4j database via structured fields — it does not accept or return raw Cypher.
		A constraint enforces a data-integrity rule on a node label or relationship type: UNIQUENESS forbids duplicate values for the given property (or property combination), KEY additionally requires the property to be present on every matching entity, and PROPERTY_EXISTENCE only requires the property to be present.
		UNIQUENESS is available on every edition of Neo4j. KEY and PROPERTY_EXISTENCE are Enterprise-only: against a Community Edition database, the server itself will reject the request, and that rejection is returned as this tool's error — do not retry a KEY or PROPERTY_EXISTENCE constraint after such a failure, it will not succeed against Community Edition.
		When name is omitted, a generated name is assigned and returned in the response so it can be used later with drop-constraint.`),
		mcpsdk.WithString("name",
			mcpsdk.Description("Optional name for the constraint. When omitted, a name is generated automatically and returned in the response."),
		),
		mcpsdk.WithString("entityType",
			mcpsdk.Required(),
			mcpsdk.Enum("NODE", "RELATIONSHIP"),
			mcpsdk.Description("Whether the constraint applies to a node label (NODE) or a relationship type (RELATIONSHIP)."),
		),
		mcpsdk.WithString("label",
			mcpsdk.Description("The node label the constraint applies to. Required when entityType is NODE; must not be set when entityType is RELATIONSHIP."),
		),
		mcpsdk.WithString("relationshipType",
			mcpsdk.Description("The relationship type the constraint applies to. Required when entityType is RELATIONSHIP; must not be set when entityType is NODE."),
		),
		mcpsdk.WithArray("properties", "string",
			mcpsdk.Required(),
			mcpsdk.Description("The property key(s) the constraint covers. PROPERTY_EXISTENCE accepts exactly one property (no composite form); UNIQUENESS and KEY accept one or more."),
		),
		mcpsdk.WithString("constraintType",
			mcpsdk.Required(),
			mcpsdk.Enum("UNIQUENESS", "KEY", "PROPERTY_EXISTENCE"),
			mcpsdk.Description("The kind of constraint to create. KEY and PROPERTY_EXISTENCE are Enterprise-only and will be rejected by the server on Community Edition."),
		),
		mcpsdk.WithTitleAnnotation("Create Constraint"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(createConstraintOutputSchema),
	)
}
