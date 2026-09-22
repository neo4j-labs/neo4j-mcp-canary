// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"sync/atomic"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

// liveConfig holds the server's current effective configuration behind an
// atomic pointer. Reads (Get) never block and never observe a torn/partial
// config value — every config change is a single atomic pointer swap (Set),
// visible to all subsequent reads immediately. This is what makes the admin
// dashboard's "Apply" possible: nothing needs the process to restart to see
// a new config value, though — see Neo4jMCPServer.Apply — reacting to that
// new value (re-registering tools, rebuilding the HTTP handler, rebuilding
// the Neo4j driver) is a separate, explicit step this type does not do.
type liveConfig struct {
	ptr atomic.Pointer[config.Config]
}

// newLiveConfig builds a liveConfig already holding cfg. cfg must not be nil.
func newLiveConfig(cfg *config.Config) *liveConfig {
	lc := &liveConfig{}
	lc.ptr.Store(cfg)
	return lc
}

// Get returns the current effective config.
func (lc *liveConfig) Get() *config.Config {
	return lc.ptr.Load()
}

// Set atomically swaps in a new effective config.
func (lc *liveConfig) Set(cfg *config.Config) {
	lc.ptr.Store(cfg)
}
