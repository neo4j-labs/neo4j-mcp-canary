# AGENTS.md

Guidance for AI coding agents working in this repository. Humans should read
[CONTRIBUTING.md](CONTRIBUTING.md) and [README.md](README.md) instead — this
file exists to give an agent the commands and conventions it needs without
re-deriving them from scratch.

## What this is

`neo4j-mcp-canary` is an experimental MCP (Model Context Protocol) server for
Neo4j, written in Go. It exposes a small set of tools (`get-schema`,
`read-cypher`, `write-cypher`, `list-gds-procedures`, `give-feedback`) over
stdio or streamable HTTP.

## Build / test / lint

```bash
go build ./...
go vet ./...
gofmt -l .                       # should print nothing
golangci-lint run                 # not enforced in CI yet, but run it anyway

go test ./...                     # unit tests only (fast, no Docker needed)
go test -tags=integration ./test/integration/...   # needs a container runtime
go test -tags=e2e ./test/e2e/...                    # needs a container runtime
```

Equivalent `task` targets exist (`task test`, `task test:unit`, `task
test:int`, `task test:e2e`, `task lint`, `task build`) — see `Taskfile.yml`.

Unit tests (`internal/...`) are fully mocked (gomock) and never touch a real
database — safe to run anytime. Integration and e2e tests are gated behind
build tags and spin up a real Neo4j via testcontainers-go; only run them when
a container runtime is available and the task actually needs that level of
verification.

### Container runtime: Docker or Podman

testcontainers-go works with either. If only Podman is available:

```bash
podman machine start   # if not already running
export DOCKER_HOST="$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}' | sed 's#^#unix://#')"
export TESTCONTAINERS_RYUK_DISABLED=true   # ryuk (the reaper) needs this under rootless podman
```

The shared test container (`test/containerrunner/container_runner.go`) waits
for both the Bolt port (7687) and the HTTP/Query API port (7474) before
returning — don't remove either wait condition, both are load-bearing (a
Bolt-only wait let `TestQueryAPIVersionGate` intermittently race against a
not-yet-serving HTTP port).

**Image version matters.** `NEO4J_IMAGE` (default `neo4j:2026.09-community`)
must be a calendar-versioned release ≥ `2026.07` or a classic-Aura release ≥
`5.26-aura` — the Query API tests depend on `queryapi.CheckMinimumVersion`'s
floor and fail fast against anything older or a bare classic version. The
default is pinned at `2026.09` rather than the bare `2026.07` floor because
`search_lifecycle_test.go` (and the `search` category's tools in general)
depend on the SEARCH clause's full-text support, which only shipped in
2026.09 — an older image below that (even one that satisfies the Query
API's own `2026.07` floor) will fail those specific tests with a genuine
Neo4j syntax error, not a graceful skip, since integration tests call tool
handlers directly and bypass the runtime version gate
(`internal/readiness.Checker`) that only governs MCP tool registration.

## Architecture conventions an agent must respect

- **`internal/mcpsdk` is the only package allowed to import an MCP SDK
  directly** (currently `github.com/modelcontextprotocol/go-sdk`). Every
  other package — tool specs/handlers, the server, all test tiers — goes
  through `internal/mcpsdk`'s re-exported types (`mcpsdk.Tool`,
  `mcpsdk.ServerTool`, `mcpsdk.NewTool`, etc.) or, in tests, through
  `internal/mcpsdk/mcpsdktest`. This absorbs future MCP SDK breakage in one
  place. Do not add a second import of the SDK anywhere else.
- **Tool specs are hand-built, not reflected.** `mcpsdk.NewTool` +
  `mcpsdk.WithString`/`WithObject`/etc. declare the advertised JSON schema
  explicitly; see the rationale comment atop `internal/tools/cypher/read_cypher_spec.go`.
  Handler-side arg binding still happens via `request.BindArguments` into a
  plain Go struct — the two are deliberately decoupled.
- **Every tool belongs to exactly one category** (`tools.CategoryCypher`,
  `tools.CategoryGDS`, `tools.CategoryFeedback` — see
  `internal/tools/category.go`) and is registered as a `ToolDefinition`
  (`Category`, `definition mcpsdk.ServerTool`, `readonly bool`) in the single
  literal slice returned by `getAllToolsDefs` in
  `internal/server/tools_register.go`. A tool's label is its existing MCP
  title annotation (`mcpsdk.WithTitleAnnotation(...)`) — don't add a separate
  display-name field for it.
- **Tool registration is filter-pipeline based.** `getEnabledTools` builds
  the full list then runs it through a chain of `toolFilter`s (read-only,
  GDS-availability, static name/category selection via
  `NEO4J_MCP_ENABLED_TOOLS`/`NEO4J_MCP_ENABLED_TOOL_CATEGORIES`). Add new
  cross-cutting selection logic as another filter in that chain, not as a
  special case elsewhere.
- **Config is schema-driven from one table.** `internal/config/schema.go`'s
  `fields` slice is the single source of truth for env var name, CLI flag,
  config-file key, default, and validation for every `Config` struct field.
  Adding a parameter means adding a `Config` field *and* a matching `Field{}`
  entry — nothing else (CLI registration, `--help` text, docs generation)
  needs to change. `TestFields_MatchConfigStruct` fails the build if the two
  drift apart; don't skip running tests after touching config.
- **HTTP request-scoped state goes through `internal/auth`**, following the
  existing `WithBasicAuth`/`GetBasicAuthCredentials` context-key pattern
  (see also `WithToolSelection`/`GetToolSelection` for the per-request tool
  headers). Add new per-request state the same way rather than inventing a
  new mechanism.
- **Per-request MCP-protocol behavior (filtering `tools/list`, rejecting
  `tools/call`, hooking `initialize`) is implemented as scoped receiving
  middleware in `internal/mcpsdk/hooks.go`**, since the official SDK exposes
  one generic `Server.AddReceivingMiddleware` chain rather than typed hooks.
  Follow the existing `beforeMethodMiddleware`/`afterCallToolMiddleware`
  pattern rather than reaching into `sdk.Server` directly.

## Code style

- Always create a new branch before making changes
- No comments unless they explain a non-obvious *why* (a hidden constraint,
  a workaround, a subtle invariant) — this codebase already follows that
  discipline; match it rather than adding narration comments.
- Don't add abstractions, config knobs, or error handling for scenarios that
  can't happen. Prefer three similar lines over a premature helper.
- Every `.go` file starts with the two-line copyright header:

  ```go
  // Copyright (c) "Neo4j"
  // Neo4j Sweden AB [http://neo4j.com]
  ```

  (see `addlicense` at the repo root for the canonical text).
- Run `gofmt -w` before finishing — CI/reviewers expect clean formatting.
  
## Changelog (changie)

User-facing changes (new features, behavior changes, fixes to shipped
behavior) need a fragment in `.changes/unreleased/`:

```bash
changie new --kind Added    # or Changed / Deprecated / Removed / Fixed / Security
```

Match the existing fragments' style (`kind: X` + a `body: >-` folded
paragraph explaining what changed and why it matters to a user reading the
changelog). **Test-only or CI-only fixes with no shipped-behavior change do
not get a fragment** — that's the established precedent in this repo's
history (e.g. flaky-test and stale-assertion fixes never added one).

## Git / PR conventions

- Commit messages: `type: short imperative description` (`fix: ...`,
  `feat: ...`, `chore: ...`), matching `git log`'s existing style. Only
  create a commit when explicitly asked to.
- External contributors must sign Neo4j's CLA (linked in CONTRIBUTING.md);
  forks are currently disabled — branch on the repo directly.
- If a change affects end-user configuration (new env var, new tool), update
  `manifest.json` too — see `docs/BUILD_MCPB.md`.
