// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"sync"
	"testing"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

func TestLiveConfig_GetReturnsWhatWasSet(t *testing.T) {
	original := &config.Config{ReadOnly: false}
	lc := newLiveConfig(original)

	if lc.Get() != original {
		t.Fatalf("Get() = %p, want the original pointer %p", lc.Get(), original)
	}

	updated := &config.Config{ReadOnly: true}
	lc.Set(updated)

	if lc.Get() != updated {
		t.Fatalf("Get() after Set() = %p, want %p", lc.Get(), updated)
	}
}

// TestLiveConfig_ConcurrentGetSet exercises the race detector (`go test -race`):
// concurrent Get/Set must never panic or be flagged as a data race, since this
// is exactly the access pattern between in-flight requests reading config and
// an admin Apply call swapping it.
func TestLiveConfig_ConcurrentGetSet(t *testing.T) {
	lc := newLiveConfig(&config.Config{})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = lc.Get().ReadOnly
		}()
		go func(v bool) {
			defer wg.Done()
			lc.Set(&config.Config{ReadOnly: v})
		}(i%2 == 0)
	}
	wg.Wait()

	if lc.Get() == nil {
		t.Fatal("Get() returned nil after concurrent Set calls")
	}
}
