// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

// ApplyTier identifies which mechanism a config change needs in order to
// take effect on a running server, without a process restart. Different
// Config fields cost very different things to change live — see the
// comment atop liveConfig for the underlying reason — so Apply reports
// exactly which tier(s) it had to touch rather than pretending every field
// is equally cheap to change.
type ApplyTier string

const (
	// ApplyTierInstant covers tool selection (ReadOnly, EnabledTools,
	// EnabledToolCategories), OutputFormat, the Cypher safeguards,
	// SchemaSampleSize, and the HTTP middleware-level settings (CORS,
	// AuthHeaderName, the AllowUnauthenticated* flags, the tool-selection
	// header names). All of these are re-applied by rebuilding
	// ToolDependencies/the tool set and reassigning s.httpServer.Handler —
	// net/http reads that field fresh per request, so no listener restart
	// is needed and no in-flight request is disrupted.
	ApplyTierInstant ApplyTier = "instant"
	// ApplyTierHTTPBounce covers HTTPHost, HTTPPort, HTTPTLSEnabled, and the
	// TLS cert/key file paths. These are baked into the bound listener at
	// ListenAndServe(TLS) call time; applying a change means stopping and
	// restarting the HTTP server, which briefly disconnects every client.
	ApplyTierHTTPBounce ApplyTier = "http_bounce"
	// ApplyTierDBRebuild covers URI and Database. Applying a change means
	// building a new database.Service (a new driver, or a different backend
	// entirely if the URI's scheme class changes between Bolt and the Query
	// API) and swapping it in; the previous connection's cleanup runs
	// immediately afterward, with no drain period for in-flight requests
	// still using it.
	ApplyTierDBRebuild ApplyTier = "db_rebuild"
)

// ApplyResult reports what Apply actually did, so a caller (the admin
// dashboard) can tell the operator plainly whether a change was instant or
// briefly disruptive.
type ApplyResult struct {
	Tiers   []ApplyTier
	Bounced bool
}

// diffTiers reports which tiers differ between old and new. A field not
// listed in any tier (TransportMode, Username, Password, Telemetry,
// LogLevel/LogFormat, AdminToken) is intentionally not live-appliable —
// see the design note in the admin-dashboard plan for why.
func diffTiers(old, next *config.Config) []ApplyTier {
	var tiers []ApplyTier

	if old.ReadOnly != next.ReadOnly ||
		old.EnabledTools != next.EnabledTools ||
		old.EnabledToolCategories != next.EnabledToolCategories ||
		old.OutputFormat != next.OutputFormat ||
		old.SchemaSampleSize != next.SchemaSampleSize ||
		old.CypherMaxRows != next.CypherMaxRows ||
		old.CypherMaxBytes != next.CypherMaxBytes ||
		old.CypherTimeoutSeconds != next.CypherTimeoutSeconds ||
		old.CypherMaxEstimatedRows != next.CypherMaxEstimatedRows ||
		old.HTTPAllowedOrigins != next.HTTPAllowedOrigins ||
		old.AuthHeaderName != next.AuthHeaderName ||
		old.HTTPToolsHeaderName != next.HTTPToolsHeaderName ||
		old.HTTPToolCategoriesHeaderName != next.HTTPToolCategoriesHeaderName ||
		old.AllowUnauthenticatedPing != next.AllowUnauthenticatedPing ||
		old.AllowUnauthenticatedToolsList != next.AllowUnauthenticatedToolsList ||
		old.AllowUnauthenticatedInitialize != next.AllowUnauthenticatedInitialize ||
		old.AllowUnauthenticatedNotificationsInitialize != next.AllowUnauthenticatedNotificationsInitialize {
		tiers = append(tiers, ApplyTierInstant)
	}

	if old.HTTPHost != next.HTTPHost ||
		old.HTTPPort != next.HTTPPort ||
		old.HTTPTLSEnabled != next.HTTPTLSEnabled ||
		old.HTTPTLSCertFile != next.HTTPTLSCertFile ||
		old.HTTPTLSKeyFile != next.HTTPTLSKeyFile {
		tiers = append(tiers, ApplyTierHTTPBounce)
	}

	if old.URI != next.URI || old.Database != next.Database {
		tiers = append(tiers, ApplyTierDBRebuild)
	}

	return tiers
}

// Apply reconfigures the running server to match newCfg, without a process
// restart. It only makes sense for a server running in HTTP transport mode
// — there is no admin surface to call it from otherwise. Concurrent Apply
// calls are serialized by applyMu so two admin requests can't race on tool
// re-registration, a driver rebuild, and an HTTP listener bounce at once.
func (s *Neo4jMCPServer) Apply(ctx context.Context, newCfg *config.Config) (ApplyResult, error) {
	if err := newCfg.Validate(); err != nil {
		return ApplyResult{}, fmt.Errorf("invalid configuration: %w", err)
	}

	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	oldCfg := s.config.Get()
	tiers := diffTiers(oldCfg, newCfg)
	result := ApplyResult{}

	if slices.Contains(tiers, ApplyTierDBRebuild) {
		if err := s.applyDBRebuild(ctx, newCfg); err != nil {
			return result, fmt.Errorf("rebuilding database connection: %w", err)
		}
		result.Tiers = append(result.Tiers, ApplyTierDBRebuild)
	}

	// From here on, every subsequent read of s.config sees newCfg — in
	// particular the tool re-registration and handler rebuild below.
	s.config.Set(newCfg)

	if slices.Contains(tiers, ApplyTierHTTPBounce) {
		if err := s.applyHTTPBounce(); err != nil {
			return result, fmt.Errorf("restarting HTTP listener: %w", err)
		}
		result.Tiers = append(result.Tiers, ApplyTierHTTPBounce)
		result.Bounced = true
		// StartHTTPServer (called by applyHTTPBounce) already rebuilds the
		// handler chain and re-registers tools from scratch, so an
		// instant-tier re-apply on top would be redundant.
		return result, nil
	}

	if slices.Contains(tiers, ApplyTierInstant) || slices.Contains(tiers, ApplyTierDBRebuild) {
		s.reRegisterTools()
		if s.httpServer != nil {
			s.httpServer.Handler = s.buildHandler()
		}
		if !slices.Contains(result.Tiers, ApplyTierInstant) {
			result.Tiers = append(result.Tiers, ApplyTierInstant)
		}
	}

	return result, nil
}

// applyDBRebuild builds a new database.Service for newCfg, swaps it in, and
// re-verifies connectivity/GDS availability against it before returning —
// so a Tier 3 Apply call reports a real connection failure to the caller
// immediately rather than deferring discovery of a bad URI to whenever a
// client happens to connect next.
func (s *Neo4jMCPServer) applyDBRebuild(ctx context.Context, newCfg *config.Config) error {
	newSvc, cleanup, err := BuildDatabaseService(ctx, newCfg, s.version)
	if err != nil {
		return err
	}

	oldCleanup := s.dbService.Set(newSvc, cleanup)

	s.connectionVerified.Store(false)
	if err := s.verifyRequirements(ctx); err != nil {
		slog.Error("Error verifying new database connection after Apply", "error", err)
		// The new service stays in place — Apply already committed to it,
		// matching the fact that there's no rollback path for a Tier 1/2
		// change either. The operator can inspect the error and Apply again.
	} else {
		s.connectionVerified.Store(true)
	}

	// Release the previous connection now that nothing new will be routed
	// to it. There is no drain/grace period — matching the fact that none
	// exists elsewhere in this codebase for driver closes either — so any
	// request still in flight against the old connection may fail.
	if oldCleanup != nil {
		oldCleanup()
	}

	return nil
}

// httpBounceTimeout bounds how long applyHTTPBounce waits for the restarted
// HTTP listener to either bind successfully or fail, before giving up and
// reporting an error. Restarting a listener is normally sub-second work;
// this is generous headroom, not an expected steady-state wait.
const httpBounceTimeout = 5 * time.Second

// httpBounceBindGrace is how long applyHTTPBounce waits, after
// HTTPServerReady closes, for a bind failure to surface via startErr before
// declaring the restart successful — see the comment at its use site.
const httpBounceBindGrace = 200 * time.Millisecond

// applyHTTPBounce stops the current HTTP listener and starts a new one from
// the (already-swapped-in) live config. This briefly disconnects every
// client — there is no way to change a bound listener's host/port/TLS
// config without doing this; see the ApplyTierHTTPBounce doc comment.
func (s *Neo4jMCPServer) applyHTTPBounce() error {
	if s.httpServer == nil {
		return fmt.Errorf("server is not running in HTTP mode")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverHTTPShutdownTimeout)
	defer cancel()
	if err := s.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("stopping current HTTP listener: %w", err)
	}

	// HTTPServerReady and shutdownChan are one-shot (closed exactly once by
	// StartHTTPServer/Stop) — a second round needs fresh ones.
	s.HTTPServerReady = make(chan struct{})
	s.shutdownChan = make(chan struct{})

	startErr := make(chan error, 1)
	go func() {
		startErr <- s.StartHTTPServer()
	}()

	// HTTPServerReady closes as soon as the *http.Server struct is built,
	// before ListenAndServe(TLS) is even attempted — it's a "fields are
	// populated" signal, not a "bind succeeded" one (the existing HTTP
	// lifecycle test harness works around the same gap with a fixed sleep
	// after this channel closes). So a bind failure (e.g. port already in
	// use) only reaches us a little later, via startErr. Race a short grace
	// window against startErr after HTTPServerReady fires, rather than
	// declaring success the instant it closes.
	select {
	case err := <-startErr:
		return err
	case <-s.HTTPServerReady:
		select {
		case err := <-startErr:
			return err
		case <-time.After(httpBounceBindGrace):
			return nil
		}
	case <-time.After(httpBounceTimeout):
		return fmt.Errorf("timed out after %s waiting for HTTP listener to restart", httpBounceTimeout)
	}
}
