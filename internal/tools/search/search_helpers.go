// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package search

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Filter is the shared structured-predicate shape every filter-taking tool
// in this package uses: vector-search's in-index WHERE, set-vector-property's
// match predicate. Operator is restricted to the exact set Cypher's SEARCH
// clause itself allows for in-index vector filtering (=,<,>,<=,>=,IN) —
// rejecting anything else here, before ever touching the database, gives a
// clear tool-level error instead of a confusing server syntax error.
type Filter struct {
	Property string `json:"property"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

var allowedFilterOperators = map[string]bool{
	"=": true, "<": true, ">": true, "<=": true, ">=": true, "IN": true,
}

// buildFilterClauses AND-joins filters into "<varName>.`prop` OP $fN"
// clauses and a matching parameter map. Returns ("", nil, nil) for a nil or
// empty input — every caller treats that as "no WHERE clause needed".
func buildFilterClauses(varName string, filters []Filter) (clause string, params map[string]any, err error) {
	if len(filters) == 0 {
		return "", nil, nil
	}

	parts := make([]string, 0, len(filters))
	params = make(map[string]any, len(filters))
	for i, f := range filters {
		if !allowedFilterOperators[f.Operator] {
			return "", nil, fmt.Errorf(
				"filter operator %q is not supported; use one of =, <, >, <=, >=, IN", f.Operator,
			)
		}
		if f.Property == "" {
			return "", nil, fmt.Errorf("filter property must not be empty")
		}
		paramName := fmt.Sprintf("f%d", i)
		parts = append(parts, fmt.Sprintf("%s.%s %s $%s", varName, quoteIdentifier(f.Property), f.Operator, paramName))
		params[paramName] = f.Value
	}
	return strings.Join(parts, " AND "), params, nil
}

// resolveSingleLabelOrType validates and resolves a NODE-or-RELATIONSHIP
// tool input's label/relationshipType pair down to the single value
// entityPatternMulti needs. This check has to happen here, before
// entityPatternMulti: once collapsed into a single-element []string,
// entityPatternMulti can no longer tell "the caller set the field that
// doesn't match the declared entityType" (e.g. entityType NODE with only
// relationshipType set, label left empty) apart from "the caller passed
// nothing" — both look like an empty label to it. Every tool that accepts
// a single label-or-relationshipType pair (create-vector-index,
// set-vector-property) must call this rather than resolving the pair
// inline, since the inline version this helper replaces silently produced
// an empty backtick-quoted identifier instead of rejecting the mismatch.
func resolveSingleLabelOrType(entityType, label, relationshipType string) (string, error) {
	switch entityType {
	case "NODE":
		if label == "" {
			return "", fmt.Errorf("label is required when entityType is NODE")
		}
		if relationshipType != "" {
			return "", fmt.Errorf("relationshipType must not be set when entityType is NODE")
		}
		return label, nil
	case "RELATIONSHIP":
		if relationshipType == "" {
			return "", fmt.Errorf("relationshipType is required when entityType is RELATIONSHIP")
		}
		if label != "" {
			return "", fmt.Errorf("label must not be set when entityType is RELATIONSHIP")
		}
		return relationshipType, nil
	default:
		return "", fmt.Errorf("entityType must be NODE or RELATIONSHIP, got %q", entityType)
	}
}

// entityPatternMulti returns the Cypher entity pattern and the variable
// name to use in SEARCH/WHERE/RETURN clauses for a NODE or RELATIONSHIP
// entity carrying one or more labels/relationship types, e.g.
// entityPatternMulti("NODE", []string{"Document", "Article"}) →
// ("(n:`Document`|`Article`)", "n"). This is a multi-label variant of
// internal/tools/cypher/schema_ddl.go's single-label entityPattern —
// needed here because fulltext indexes (CREATE FULLTEXT INDEX ... FOR
// (n:Label1|Label2) ...) and catalog-auto-detected search patterns
// (SHOW INDEXES' labelsOrTypes column) can both span more than one label,
// unlike cypher's DDL tools which only ever create against exactly one.
func entityPatternMulti(entityType string, labelsOrTypes []string) (pattern, varName string, err error) {
	if len(labelsOrTypes) == 0 {
		return "", "", fmt.Errorf("at least one label or relationship type is required")
	}
	quoted := make([]string, len(labelsOrTypes))
	for i, l := range labelsOrTypes {
		quoted[i] = quoteIdentifier(l)
	}
	joined := strings.Join(quoted, "|")

	switch entityType {
	case "NODE":
		return fmt.Sprintf("(n:%s)", joined), "n", nil
	case "RELATIONSHIP":
		return fmt.Sprintf("()-[r:%s]-()", joined), "r", nil
	default:
		return "", "", fmt.Errorf("entityType must be NODE or RELATIONSHIP, got %q", entityType)
	}
}

// bracketList renders a bracketed, comma-joined list of varName.property
// references, e.g. bracketList("n", []string{"title", "body"}) →
// "[n.`title`, n.`body`]" — the ON EACH [...] shape CREATE FULLTEXT INDEX
// and the WITH [...] filterable-properties clause both need.
func bracketList(varName string, properties []string) string {
	refs := make([]string, len(properties))
	for i, p := range properties {
		refs[i] = fmt.Sprintf("%s.%s", varName, quoteIdentifier(p))
	}
	return "[" + strings.Join(refs, ", ") + "]"
}

// mapProjection renders a Cypher map-projection expression restricting a
// RETURN to just the named properties, e.g. mapProjection("n",
// []string{"title", "body"}) → "n{.`title`, .`body`}" — vector-search's
// returnProperties uses this to let a caller exclude the (often
// high-dimensional) embedding property from results. Note this returns a
// plain map, not a tagged node — it loses elementId/labels, an accepted
// trade-off of asking for a narrower projection.
func mapProjection(varName string, properties []string) string {
	refs := make([]string, len(properties))
	for i, p := range properties {
		refs[i] = "." + quoteIdentifier(p)
	}
	return varName + "{" + strings.Join(refs, ", ") + "}"
}

// quoteIdentifier backtick-quotes a Cypher identifier (label, relationship
// type, property key, or index name), doubling any embedded backtick.
// Duplicated from internal/tools/cypher/schema_ddl.go's identical helper
// rather than imported: this package and cypher are sibling tool
// categories with otherwise zero coupling, and a one-line escaping rule is
// exactly what AGENTS.md's "prefer three similar lines over a premature
// helper" is about.
func quoteIdentifier(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// quoteStringLiteral single-quotes a Cypher string literal for embedding in
// a CREATE ... INDEX OPTIONS map, where the value position doesn't reliably
// accept a bound $-parameter across server versions (mirroring why the
// existing cypher-category DDL tools never parameterize their generated
// text either). Escapes backslash and single-quote.
func quoteStringLiteral(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return "'" + escaped + "'"
}

// generatedSchemaName returns a short, unique index name to use when the
// caller doesn't supply one — same convention and rationale as
// internal/tools/cypher/schema_ddl.go's identical helper (duplicated for
// the same sibling-package reason as quoteIdentifier above): every create
// call always puts an explicit name into the generated Cypher, never
// relying on Neo4j's own auto-naming, so the response can always echo a
// name usable with drop-index later.
func generatedSchemaName() string {
	return "mcp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}
