// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package patterns

import (
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graphutil"
)

const (
	motifDefaultLimit    = 25
	motifMaxLimit        = 100
	motifDefaultDegree   = 3
	motifMaxDegree       = 100_000_000
	motifDefaultCycleLen = 3
	motifMaxCycleLen     = 6
)

type FindMotifInput struct {
	Motif             string             `json:"motif"`
	RelationshipTypes []string           `json:"relationshipTypes,omitempty"`
	NodeLabels        []string           `json:"nodeLabels,omitempty"`
	Start             *graphutil.NodeRef `json:"start,omitempty"`
	MinDegree         int                `json:"minDegree,omitempty"`
	CycleLength       int                `json:"cycleLength,omitempty"`
	NodeProperties    []string           `json:"nodeProperties,omitempty"`
	Limit             int                `json:"limit,omitempty"`
}

func FindMotifSpec() mcpsdk.Tool {
	return mcpsdk.NewTool("find-motif",
		mcpsdk.WithDescription(fmt.Sprintf(`
		Detect a well-known structural shape in the graph. Read-only. Choose the motif:
		"star" — hub nodes with at least minDegree connections in any direction (default %d); "fan-out" — hubs with at least minDegree outgoing relationships (e.g. a component that depends on many others); "fan-in" — hubs with at least minDegree incoming relationships (e.g. a component many others depend on). Rows are {hub, degree}, biggest first.
		"triangle" — three nodes all connected to each other in any direction (e.g. mutual collaboration); each triangle is returned once. "cycle" — a directed loop of cycleLength hops (default %d, 2-%d), e.g. circular dependencies; each cycle is returned once. Rows are paths {length, nodes, relationships}.
		Name relationshipTypes and nodeLabels to keep the search focused and fast. start anchors the search to one node instead of scanning the whole graph. Use match-pattern for a shape that is not in this list.`, motifDefaultDegree, motifDefaultCycleLen, motifMaxCycleLen)),
		mcpsdk.WithString("motif", mcpsdk.Required(), mcpsdk.Enum("star", "fan-in", "fan-out", "triangle", "cycle"), mcpsdk.Description("The shape to look for.")),
		mcpsdk.WithArray("relationshipTypes", "string", mcpsdk.Description("Optional relationship types the shape is made of. Omit for every type (slower).")),
		mcpsdk.WithArray("nodeLabels", "string", mcpsdk.Description("Optional labels every node in the shape must have.")),
		mcpsdk.WithObject("start", mcpsdk.Description("Optional: anchor the search at this node (the hub for star/fan shapes, a member for triangle/cycle). "+graphutil.NodeRefDoc)),
		mcpsdk.WithInteger("minDegree", mcpsdk.Description(fmt.Sprintf("For star, fan-in and fan-out: the minimum number of connections (1-%d). Default %d.", motifMaxDegree, motifDefaultDegree))),
		mcpsdk.WithInteger("cycleLength", mcpsdk.Description(fmt.Sprintf("For cycle: the number of hops in the loop (2-%d). Default %d.", motifMaxCycleLen, motifDefaultCycleLen))),
		mcpsdk.WithArray("nodeProperties", "string", mcpsdk.Description("Optional property names (e.g. \"name\") to include on returned nodes, which otherwise carry only id and labels.")),
		mcpsdk.WithInteger("limit", mcpsdk.Description(fmt.Sprintf("Maximum rows to return (1-%d). Default %d.", motifMaxLimit, motifDefaultLimit))),
		mcpsdk.WithTitleAnnotation("Find Motif"),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(true),
		mcpsdk.WithOutputSchema(graphutil.OutputSchema),
	)
}

func FindMotifHandler(deps *tools.ToolDependencies) mcpsdk.ToolHandlerFunc {
	return graphutil.NewHandler(deps, "find-motif", buildFindMotif)
}

func buildFindMotif(in *FindMotifInput) (graphutil.Query, error) {
	limit, err := graphutil.IntInRange("limit", in.Limit, motifDefaultLimit, 1, motifMaxLimit)
	if err != nil {
		return graphutil.Query{}, err
	}
	typeExpr, err := graphutil.TypeExpr(in.RelationshipTypes)
	if err != nil {
		return graphutil.Query{}, err
	}
	labels, err := graphutil.LabelExpr(in.NodeLabels)
	if err != nil {
		return graphutil.Query{}, err
	}
	b := graphutil.NewBuilder()

	// anchor renders the optional start node as an inline pattern on v,
	// folding in the shared label restriction.
	anchor := func(v string) (string, error) {
		ref := graphutil.NodeRef{}
		if in.Start != nil {
			ref = *in.Start
		}
		conds, err := ref.Conditions(v, b)
		if err != nil {
			return "", fmt.Errorf("start: %w", err)
		}
		startLabels, err := graphutil.LabelExpr(append(append([]string(nil), in.NodeLabels...), ref.Labels...))
		if err != nil {
			return "", err
		}
		return "(" + v + startLabels + graphutil.Where(conds) + ")", nil
	}
	anchored := in.Start != nil

	switch in.Motif {
	case "star", "fan-in", "fan-out":
		minDegree, err := graphutil.IntInRange("minDegree", in.MinDegree, motifDefaultDegree, 1, motifMaxDegree)
		if err != nil {
			return graphutil.Query{}, err
		}
		direction := map[string]string{"star": graphutil.DirectionBoth, "fan-out": graphutil.DirectionOutgoing, "fan-in": graphutil.DirectionIncoming}[in.Motif]
		rel, err := graphutil.SingleRel(direction, "", typeExpr)
		if err != nil {
			return graphutil.Query{}, err
		}
		hub, err := anchor("h")
		if err != nil {
			return graphutil.Query{}, err
		}
		return graphutil.Query{
			Cypher: fmt.Sprintf("MATCH %s\nWITH h, COUNT { (h)%s() } AS degree\nWHERE degree >= %s\nRETURN %s AS hub, degree\nORDER BY degree DESC\nLIMIT %s",
				hub, rel, b.Param(minDegree), graphutil.NodeSummary("h", in.NodeProperties), b.Param(limit)),
			Params: b.Params(),
		}, nil

	case "triangle":
		rel, err := graphutil.SingleRel(graphutil.DirectionBoth, "", typeExpr)
		if err != nil {
			return graphutil.Query{}, err
		}
		a, err := anchor("a")
		if err != nil {
			return graphutil.Query{}, err
		}
		order := "elementId(a) < elementId(b) AND elementId(b) < elementId(c)"
		if anchored {
			order = "elementId(b) < elementId(c)"
		}
		return graphutil.Query{
			Cypher: fmt.Sprintf("MATCH p = %s%s(b%s)%s(c%s)%s(a)\nWHERE a <> b AND b <> c AND a <> c AND %s\nRETURN DISTINCT %s AS path\nLIMIT %s",
				a, rel, labels, rel, labels, rel, order, graphutil.PathMap("p", in.NodeProperties), b.Param(limit)),
			Params: b.Params(),
		}, nil

	case "cycle":
		length, err := graphutil.IntInRange("cycleLength", in.CycleLength, motifDefaultCycleLen, 2, motifMaxCycleLen)
		if err != nil {
			return graphutil.Query{}, err
		}
		rel, err := graphutil.QuantifiedRel(graphutil.DirectionOutgoing, typeExpr, length, length)
		if err != nil {
			return graphutil.Query{}, err
		}
		a, err := anchor("a")
		if err != nil {
			return graphutil.Query{}, err
		}
		// Distinct members, and — when not anchored — only the rotation that
		// starts at the smallest id, so each cycle is reported once.
		where := "WITH p, nodes(p) AS ns WHERE all(i IN range(1, size(ns) - 2) WHERE none(j IN range(0, i - 1) WHERE ns[i] = ns[j]))"
		if !anchored {
			where += " AND all(x IN ns WHERE elementId(x) >= elementId(ns[0]))"
		}
		if labels != "" {
			where += " AND all(x IN ns WHERE x" + labels + ")"
		}
		return graphutil.Query{
			Cypher: fmt.Sprintf("MATCH p = %s%s(a)\n%s\nRETURN %s AS path\nLIMIT %s", a, rel, where, graphutil.PathMap("p", in.NodeProperties), b.Param(limit)),
			Params: b.Params(),
		}, nil
	}
	return graphutil.Query{}, fmt.Errorf(`motif must be one of "star", "fan-in", "fan-out", "triangle" or "cycle" (got %q)`, in.Motif)
}
