// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

func GetSchemaSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("get-schema",
		mcpsdk.WithDescription(`
		Retrieve the schema information from the Neo4j database, including node labels, relationship types, and property keys.
		If the database contains no data, no schema information is returned.
		Requires APOC to be installed on the target database (uses apoc.meta.schema).`),
		mcpsdk.WithTitleAnnotation("Get Neo4j Schema"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithOpenWorldHintAnnotation(true),
	)
}
