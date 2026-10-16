// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graph

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

type FindNodesInput struct {
	graphutil.NodeRef
	ReturnProperties []string `json:"returnProperties,omitempty"`
	OrderBy          string   `json:"orderBy,omitempty"`
	OrderDirection   string   `json:"orderDirection,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Skip             int      `json:"skip,omitempty"`
}

func FindNodesSpec() mcpsdk.Tool {
	opts := []mcpsdk.ToolOption{
		mcpsdk.WithDescription(fmt.Sprintf(`
		Find nodes by label, exact property values and/or filters, and return each as {id, labels, properties}. Read-only.
		Use this to discover nodes and get their ids; use get-node for one known node plus its relationship counts, and count-nodes when you only need a number.
		Results are paginated: at most %d per call (default %d); use skip to page. With no criteria at all it returns the first nodes in the database, so pass labels or match where you can.
		Use returnProperties to return only the properties you need — leave out large ones such as embeddings.`, maxFindLimit, defaultFindLimit)),
	}
	opts = append(opts, graphutil.NodeRefOptions("Optional labels the node must all have, e.g. [\"Person\"].")...)
	opts = append(opts,
		mcpsdk.WithArray("returnProperties", "string", mcpsdk.Description("Optional property names to return instead of all properties.")),
		mcpsdk.WithString("orderBy", mcpsdk.Description("Optional property name to sort by.")),
		mcpsdk.WithString("orderDirection", mcpsdk.Enum("ASC", "DESC"), mcpsdk.Description("Sort direction when orderBy is set. Default ASC.")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum nodes to return (1-%d). Default %d.", maxFindLimit, defaultFindLimit))),
		mcpsdk.WithInteger("skip", mcpsdk.Description("Number of nodes to skip, for paging. Default 0.")),
		mcpsdk.WithTitleAnnotation("Find Nodes"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
	return mcpsdk.NewTool("find-nodes", opts...)
}

func FindNodesHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-nodes", buildFindNodes)
}

func buildFindNodes(in *FindNodesInput) (graphutil.Query, error) {
	b := graphutil.NewBuilder()
	match, err := nodeMatch(in.NodeRef, "n", b)
	if err != nil {
		return graphutil.Query{}, err
	}
	order, err := orderClause("n", in.OrderBy, in.OrderDirection)
	if err != nil {
		return graphutil.Query{}, err
	}
	page, err := pagination(b, in.Limit, in.Skip)
	if err != nil {
		return graphutil.Query{}, err
	}
	return graphutil.Query{
		Cypher: match + "\nRETURN " + graphutil.NodeMap("n", in.ReturnProperties) + " AS node" + order + page,
		Params: b.Params(),
	}, nil
}
