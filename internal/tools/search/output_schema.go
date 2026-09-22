// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// cypherResponseOutputSchema mirrors read-cypher/write-cypher's own
// identically-named schema var (internal/tools/cypher/read_cypher_spec.go):
// vector-search/fulltext-search's rows are raw Cypher return values
// (entity, score), the same envelope shape as any other node-returning
// tool, rendered through the shared database.CypherResponse type — not a
// typed schema row like IndexInfo, so reusing the exported database-layer
// type directly (rather than duplicating it) is the right call here.
var cypherResponseOutputSchema = mcpsdk.MustOutputSchemaFor[database.CypherResponse]()
