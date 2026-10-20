// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package traversal

import (
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

// Handlers reject arguments they don't declare, so a property advertised in
// a tool's schema without a matching input field would make that argument
// unusable.
func TestSchemaPropertiesBindToInputFields(t *testing.T) {
	for _, c := range []struct {
		spec  mcpsdk.Tool
		input any
	}{
		{ExpandNeighborsSpec(), ExpandNeighborsInput{}},
		{FindShortestPathSpec(), FindShortestPathInput{}},
		{FindAllPathsSpec(), FindAllPathsInput{}},
		{TraverseSpec(), TraverseInput{}},
	} {
		if missing := graphutil.UndeclaredInputFields(c.spec, c.input); len(missing) > 0 {
			t.Errorf("%s advertises %v but its input struct has no such field", c.spec.Name, missing)
		}
	}
}
