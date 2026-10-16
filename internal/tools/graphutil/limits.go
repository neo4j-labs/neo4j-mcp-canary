// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import "fmt"

// IntInRange returns def when n is 0 (not supplied) and an actionable error
// when n falls outside [min, max].
func IntInRange(name string, n, def, lo, hi int) (int, error) {
	if n == 0 {
		return def, nil
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s must be between %d and %d (got %d); the default is %d", name, lo, hi, n, def)
	}
	return n, nil
}

// Direction values accepted by tools that traverse relationships.
const (
	DirectionOutgoing = "OUTGOING"
	DirectionIncoming = "INCOMING"
	DirectionBoth     = "BOTH"
)

// QuantifiedRel renders a quantified relationship such as
// "-[:`T`]->{1,3}" for the given direction, type expression and bounds.
// Bounds are validated ints rendered inline: quantifier bounds can't be
// bound parameters.
func QuantifiedRel(direction, typeExpr string, minDepth, maxDepth int) (string, error) {
	q := fmt.Sprintf("{%d,%d}", minDepth, maxDepth)
	switch direction {
	case DirectionOutgoing, "":
		return fmt.Sprintf("-[%s]->%s", typeExpr, q), nil
	case DirectionIncoming:
		return fmt.Sprintf("<-[%s]-%s", typeExpr, q), nil
	case DirectionBoth:
		return fmt.Sprintf("-[%s]-%s", typeExpr, q), nil
	}
	return "", fmt.Errorf("direction must be OUTGOING, INCOMING or BOTH (got %q)", direction)
}

// SingleRel renders a one-hop relationship pattern bound to relVar.
func SingleRel(direction, relVar, typeExpr string) (string, error) {
	switch direction {
	case DirectionOutgoing, "":
		return fmt.Sprintf("-[%s%s]->", relVar, typeExpr), nil
	case DirectionIncoming:
		return fmt.Sprintf("<-[%s%s]-", relVar, typeExpr), nil
	case DirectionBoth:
		return fmt.Sprintf("-[%s%s]-", relVar, typeExpr), nil
	}
	return "", fmt.Errorf("direction must be OUTGOING, INCOMING or BOTH (got %q)", direction)
}
