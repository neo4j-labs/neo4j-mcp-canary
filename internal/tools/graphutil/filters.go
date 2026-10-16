// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
)

// Filter is a structured property predicate. Operator must be one of
// FilterOperators; IS NULL / IS NOT NULL take no value.
type Filter struct {
	Property string `json:"property"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}

// UnmarshalJSON decodes with UseNumber so whole-number values stay integers
// (as cypher.Params does) rather than becoming float64, which would lose
// precision for large ids and bind as floats.
func (f *Filter) UnmarshalJSON(data []byte) error {
	var raw struct {
		Property string `json:"property"`
		Operator string `json:"operator"`
		Value    any    `json:"value"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*f = Filter{Property: raw.Property, Operator: raw.Operator, Value: cypher.ConvertNumbers(raw.Value)}
	return nil
}

// FilterOperators is the whitelist of operators a Filter may use, mapped to
// the Cypher they render as. Anything else is rejected before a query is built.
var FilterOperators = map[string]string{
	"=": "=", "<>": "<>", "<": "<", ">": ">", "<=": "<=", ">=": ">=",
	"IN": "IN", "CONTAINS": "CONTAINS", "STARTS WITH": "STARTS WITH", "ENDS WITH": "ENDS WITH",
	"IS NULL": "IS NULL", "IS NOT NULL": "IS NOT NULL",
}

const filterOperatorList = "=, <>, <, >, <=, >=, IN, CONTAINS, STARTS WITH, ENDS WITH, IS NULL, IS NOT NULL"

// FilterConditions renders filters as AND-able conditions on varExpr.
func FilterConditions(varExpr string, filters []Filter, b *Builder) ([]string, error) {
	conds := make([]string, 0, len(filters))
	for i, f := range filters {
		if strings.TrimSpace(f.Property) == "" {
			return nil, fmt.Errorf("filters[%d].property must not be empty; name the property to filter on", i)
		}
		op, ok := FilterOperators[strings.ToUpper(strings.TrimSpace(f.Operator))]
		if !ok {
			return nil, fmt.Errorf("filters[%d].operator %q is not supported; use one of %s", i, f.Operator, filterOperatorList)
		}
		prop := fmt.Sprintf("%s.%s", varExpr, QuoteIdentifier(f.Property))
		if op == "IS NULL" || op == "IS NOT NULL" {
			conds = append(conds, prop+" "+op)
			continue
		}
		if f.Value == nil {
			return nil, fmt.Errorf("filters[%d].value is required for operator %s; to test for a missing property use IS NULL or IS NOT NULL", i, op)
		}
		if op == "IN" {
			if _, isList := f.Value.([]any); !isList {
				return nil, fmt.Errorf("filters[%d].value must be an array when operator is IN", i)
			}
		}
		conds = append(conds, fmt.Sprintf("%s %s %s", prop, op, b.Param(f.Value)))
	}
	return conds, nil
}

// MatchConditions renders an exact-equality property map as AND-able
// conditions on varExpr, in sorted key order so generated Cypher is stable.
func MatchConditions(varExpr string, match map[string]any, b *Builder) ([]string, error) {
	keys := make([]string, 0, len(match))
	for k := range match {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	conds := make([]string, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("match contains an empty property name; use property names as keys")
		}
		if match[k] == nil {
			return nil, fmt.Errorf("match[%q] is null; to test for a missing property use a filter with operator IS NULL", k)
		}
		conds = append(conds, fmt.Sprintf("%s.%s = %s", varExpr, QuoteIdentifier(k), b.Param(match[k])))
	}
	return conds, nil
}
