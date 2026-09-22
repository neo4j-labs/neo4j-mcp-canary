// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package readiness

import (
	"context"
	"log"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
)

// Result reports what Checker.Verify discovered about the connected Neo4j
// instance.
type Result struct {
	// GDSInstalled reports whether the Graph Data Science plugin is
	// available. GDS is optional, so its absence is not an error.
	GDSInstalled bool
}

// Checker verifies that a Neo4j connection is ready for the server to use.
// It is transport-agnostic: it only calls database.Service, so it behaves
// identically whether that service is Bolt- or Query-API-backed.
type Checker struct {
	db database.Service
}

// NewChecker creates a Checker backed by the given database.Service.
func NewChecker(db database.Service) *Checker {
	return &Checker{db: db}
}

// Verify checks the Neo4j requirements:
//   - A valid connection with a Neo4j instance.
//   - The ability to perform a read query (database name is correctly defined).
//   - In case GDS is not installed, Result.GDSInstalled is false rather than an error.
func (c *Checker) Verify(ctx context.Context) (Result, error) {
	if err := c.db.VerifyConnectivity(ctx); err != nil {
		return Result{}, err
	}

	// Call gds.version procedure to determine if GDS is installed
	records, err := c.db.ExecuteReadQuery(ctx, "RETURN gds.version() as gdsVersion", nil)
	if err != nil {
		// GDS is optional, so we log a warning and continue, assuming it's not installed.
		log.Print("Impossible to verify GDS installation.")
		return Result{GDSInstalled: false}, nil
	}

	if len(records) == 1 && len(records[0].Values) == 1 {
		if _, ok := records[0].Values[0].(string); ok {
			return Result{GDSInstalled: true}, nil
		}
	}

	return Result{GDSInstalled: false}, nil
}
