// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// DropConstraintInput is the struct the handler binds incoming arguments
// into via request.BindArguments. The JSON schema advertised to MCP clients
// is declared explicitly in DropConstraintSpec below.
type DropConstraintInput struct {
	Name string `json:"name"`
}

// dropConstraintOutputSchema is computed once at package init since
// DropConstraintOutput's shape never changes between calls.
var dropConstraintOutputSchema = mcpsdk.MustOutputSchemaFor[DropConstraintOutput]()

// DropConstraintSpec declares the MCP tool schema for drop-constraint.
//
// Only a structured name identifies the constraint to drop — this tool never
// accepts or returns raw Cypher.
func DropConstraintSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("drop-constraint",
		mcpsdk.WithDescription(`
		Drop a constraint from the Neo4j database by name via a structured field — it does not accept or return raw Cypher.
		Removing a constraint lifts the data-integrity guarantee it enforced (uniqueness, key, or property existence); existing data is not modified, but future writes will no longer be checked against that rule.
		Use list-constraints-and-indexes first to find the exact name if it isn't already known. Dropping a name that doesn't exist is not an error — the response's existed field reports whether the constraint was actually present.`),
		mcpsdk.WithString("name",
			mcpsdk.Required(),
			mcpsdk.Description("The name of the constraint to drop."),
		),
		mcpsdk.WithTitleAnnotation("Drop Constraint"),
		mcpsdk.WithReadOnlyHintAnnotation(false),
		mcpsdk.WithDestructiveHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(dropConstraintOutputSchema),
	)
}
