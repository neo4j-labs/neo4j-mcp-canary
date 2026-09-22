// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// listConstraintsAndIndexesOutputSchema is computed once at package init
// since ConstraintsAndIndexes's shape never changes between calls.
var listConstraintsAndIndexesOutputSchema = mcpsdk.MustOutputSchemaFor[ConstraintsAndIndexes]()

func ListConstraintsAndIndexesSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("list-constraints-and-indexes",
		mcpsdk.WithDescription(`
		List the constraints and indexes defined on the Neo4j database via structured fields — it does not accept or return raw Cypher.
		For each constraint, reports its name, type (e.g. UNIQUENESS, NODE_KEY, EXISTENCE), the entity type it applies to (NODE or RELATIONSHIP), the labels/types and properties it covers, and, when the constraint is backed by one, the name of its owned index.
		For each index, reports its name, type (e.g. RANGE, TEXT, POINT, FULLTEXT, VECTOR, LOOKUP), state (e.g. ONLINE, POPULATING, FAILED), population percentage, the entity type, labels/types and properties it covers, its provider, and, when it backs a constraint, the name of that owning constraint.
		Constraints and indexes are related: a uniqueness or key constraint is typically backed by an index of the same name, surfaced here via ownedIndex/owningConstraint on either side.
		Use this before creating a new constraint or index to check whether an equivalent one already exists, or to diagnose why a query isn't using an index (e.g. state is still POPULATING or FAILED).`),
		mcpsdk.WithTitleAnnotation("List Neo4j Constraints and Indexes"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(listConstraintsAndIndexesOutputSchema),
	)
}
