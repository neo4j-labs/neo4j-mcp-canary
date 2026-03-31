// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"github.com/mark3labs/mcp-go/mcp"
)

func GetSchemaSpec() mcp.Tool {
	return mcp.NewTool("get-schema",
		mcp.WithDescription(`
		Retrieve the schema information from the Neo4j database.
		Returns node labels with their property names and types, relationship types with their
		source and target labels and property names and types, and all database indexes including
		vector indexes with their dimensions and similarity function, and full-text indexes.
		Full-text indexes can be queried using db.index.fulltext.queryNodes() and
		db.index.fulltext.queryRelationships() procedures.
		Use this information to understand the graph data model and to write correct Cypher queries.
		If the database contains no data, no schema information is returned.`),
		mcp.WithTitleAnnotation("Get Neo4j Schema"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
