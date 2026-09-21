// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package tools

// Category classifies a tool for selection purposes (see
// server.ToolDefinition). Every tool must belong to exactly one category.
type Category string

const (
	CategoryCypher   Category = "cypher"
	CategoryGDS      Category = "gds"
	CategoryFeedback Category = "feedback"
)

// AllCategories returns every known category, in declaration order. It is
// the single source of truth used to validate category names supplied via
// configuration or an HTTP request header.
func AllCategories() []Category {
	return []Category{CategoryCypher, CategoryGDS, CategoryFeedback}
}
