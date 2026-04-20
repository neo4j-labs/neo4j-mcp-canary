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
		Each node and relationship may also expose a 'requiredProperties' list naming the properties
		that are backed by a NOT NULL existence constraint — properties in this list are guaranteed
		to be present on every instance, so queries can skip null checks for them.
		The response also includes a 'metadata' field describing how the schema was retrieved.
		If metadata.source is 'sampled', the schema was inferred from a limited sample (because
		the full-scan query exceeded the configured timeout) and rare labels, relationship types
		or properties may be missing — cross-check against the 'indexes' array, which is always
		complete, to discover labels and relationship types that sampling may have missed.
		The metadata may also contain 'missingNodeLabels' and 'missingRelTypes' arrays listing
		entities that appear in indexes but are absent from the main schema arrays. If these
		are non-empty the schema is incomplete even when source is 'full_scan'; query the
		named labels or relationship types directly (for example MATCH (n:<Label>) RETURN n
		LIMIT 1) to discover their structure before writing queries against them.
		If the database contains no data, no schema information is returned.`),
		mcp.WithTitleAnnotation("Get Neo4j Schema"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
	)
}
