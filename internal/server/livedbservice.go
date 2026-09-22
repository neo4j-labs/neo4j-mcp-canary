// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"sync"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
)

// liveDBService holds the server's current database.Service — plus the
// cleanup func that releases whatever connection/driver backs it (returned
// alongside the Service by BuildDatabaseService) — behind a mutex, so both
// can be swapped at runtime (see Neo4jMCPServer.Apply's Tier 3 path, which
// rebuilds the connection against a new URI/database) without every read
// site needing its own synchronization. A plain mutex is deliberate here
// over something lock-free: swapping the service is a rare, heavyweight
// admin operation, not a hot path, so simplicity wins over the lock-free
// read liveConfig uses for the (much more frequently read) config.
type liveDBService struct {
	mu      sync.RWMutex
	svc     database.Service
	cleanup func()
}

func newLiveDBService(svc database.Service) *liveDBService {
	return &liveDBService{svc: svc}
}

// Get returns the current database.Service.
func (l *liveDBService) Get() database.Service {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.svc
}

// Set swaps in a new database.Service and its cleanup func, returning the
// previous cleanup so the caller can release the old connection/driver once
// it's no longer needed. There is no drain/grace-period mechanism here —
// matching the fact that none exists elsewhere in this codebase for driver
// closes either — so the caller should expect in-flight requests against
// the old service to potentially fail once its cleanup runs, not degrade
// gracefully.
func (l *liveDBService) Set(svc database.Service, cleanup func()) (oldCleanup func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	oldCleanup = l.cleanup
	l.svc = svc
	l.cleanup = cleanup
	return oldCleanup
}
