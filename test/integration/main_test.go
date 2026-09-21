// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/test/dbservice"
)

var dbs = dbservice.NewDBService()

func TestMain(m *testing.M) {
	ctx := context.Background()

	// dbs.Start also brings up the Query API's HTTP port on the same shared
	// container (see test/containerrunner/container_runner.go) — no separate
	// lifecycle call is needed; test files read it back via
	// dbs.GetQueryAPIBaseURL().
	dbs.Start(ctx)

	code := m.Run()

	dbs.Stop(ctx)

	os.Exit(code)
}
