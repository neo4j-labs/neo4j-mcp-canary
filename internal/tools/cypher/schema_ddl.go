// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cypher

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// entityPattern returns the Cypher entity pattern and the variable name to
// use in REQUIRE/ON clauses for the given entity type, e.g.
// ("(n:`Person`)", "n") or ("()-[r:`REVIEWED`]-()", "r"). Shared by the
// constraint and index create tools, which both need to turn an
// entityType/label/relationshipType triple into the same pattern shape.
func entityPattern(entityType, label, relationshipType string) (pattern, varName string, err error) {
	switch entityType {
	case "NODE":
		if label == "" {
			return "", "", fmt.Errorf("label is required when entityType is NODE")
		}
		if relationshipType != "" {
			return "", "", fmt.Errorf("relationshipType must not be set when entityType is NODE")
		}
		return fmt.Sprintf("(n:%s)", quoteIdentifier(label)), "n", nil
	case "RELATIONSHIP":
		if relationshipType == "" {
			return "", "", fmt.Errorf("relationshipType is required when entityType is RELATIONSHIP")
		}
		if label != "" {
			return "", "", fmt.Errorf("label must not be set when entityType is RELATIONSHIP")
		}
		return fmt.Sprintf("()-[r:%s]-()", quoteIdentifier(relationshipType)), "r", nil
	default:
		return "", "", fmt.Errorf("entityType must be NODE or RELATIONSHIP, got %q", entityType)
	}
}

// parenList renders a parenthesized, comma-joined list of varName.property
// references, e.g. parenList("n", []string{"tenantId", "orderId"}) →
// "(n.`tenantId`, n.`orderId`)". Used by UNIQUENESS/KEY REQUIRE clauses and
// by every index type's ON clause.
func parenList(varName string, properties []string) string {
	refs := make([]string, len(properties))
	for i, p := range properties {
		refs[i] = fmt.Sprintf("%s.%s", varName, quoteIdentifier(p))
	}
	return "(" + strings.Join(refs, ", ") + ")"
}

// quoteIdentifier backtick-quotes a Cypher identifier (label, relationship
// type, property key, or constraint/index name), doubling any embedded
// backtick per Cypher's escaping rule. Every one of the DDL tools' inputs
// is an identifier position, not an expression, so none of these can be
// passed as a bound `$`-parameter — they must be interpolated into the
// generated Cypher text, and backtick-quoting is what makes that safe
// against special characters or reserved words in a caller-supplied name.
func quoteIdentifier(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// generatedSchemaName returns a short, unique constraint/index name to use
// when the caller doesn't supply one. Every create-constraint/create-index
// call always puts an explicit name into the generated Cypher — never
// relying on Neo4j's own auto-naming — so the response can always echo a
// name the caller can later pass to drop-constraint/drop-index with
// certainty.
func generatedSchemaName() string {
	return "mcp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}
