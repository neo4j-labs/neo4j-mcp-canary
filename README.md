<!-- mcp-name: io.github.neo4j-labs/neo4j-mcp-canary -->

# Neo4j MCP Canary — _The canary goes first so the rest of us know what's coming_

Neo4j MCP Canary is a fast-moving, experimental release of the Neo4j MCP server for customers who want to explore emerging capabilities before they are considered for the official server.

Built on the source of the official Model Context Protocol (MCP) server for Neo4j, this variant is here for exploring potential new capabilities with experimentation.

As it is a labs project, be aware that:

- It is not supported
- It may contain breaking changes between its own releases and with the official Neo4j MCP server
- It should be tested before using

> Do not assume the canary will work for your situation. Test first.

## Prerequisites

- A running Neo4j database instance; options include [Aura](https://neo4j.com/product/auradb/), [Neo4j Desktop](https://neo4j.com/download/), or [self-managed](https://neo4j.com/deployment-center/#gdb-tab).
- APOC plugin installed in the Neo4j instance (required — `get-schema` uses `apoc.meta.schema`).
- Any MCP-compatible client (e.g. [VSCode](https://code.visualstudio.com/) with [MCP support](https://code.visualstudio.com/docs/copilot/customization/mcp-servers)).

> **⚠️ Known Issue**: Neo4j **5.26.18** has a bug in APOC that causes the `get-schema` tool to fail. This is fixed in **5.26.19** and above. If you're on 5.26.18, please upgrade. See [#136](https://github.com/neo4j-labs/neo4j-mcp-canary/issues/136) for details.

## Startup Checks & Adaptive Operation

The server performs several checks to ensure your environment is correctly configured.

**STDIO Mode — Mandatory Requirements**
In STDIO mode, the server verifies the following. If any check fails (e.g. invalid configuration, incorrect credentials, missing APOC), the server will not start:

- A valid connection to your Neo4j instance.
- The ability to execute queries.
- The presence of the APOC plugin.
- Minimum version check for use of Query API to communicate with the datatbase

**HTTP Mode — Verification Skipped**
In HTTP mode, startup verification checks are skipped because credentials come from per-request auth headers. The server starts immediately without connecting to Neo4j. The one exception is [Query API mode](#connecting-via-the-query-api-instead-of-bolt): its minimum-version check runs at startup in both transport modes, since it only needs an unauthenticated GET and doesn't depend on per-request credentials.

**Optional Requirements**
If an optional dependency is missing, the server starts in adaptive mode. For instance, if the Graph Data Science (GDS) library is not detected, the server still launches but automatically disables GDS-dependent tools such as `list-gds-procedures`. Likewise, the `graph`, `traversal` and `patterns` tools are disabled when the connected server does not support Cypher 25. All other tools remain available.

## Installation (Binary)

Releases: [Canary MCP releases](https://github.com/neo4j-labs/neo4j-mcp-canary/releases)

1. Download the archive for your OS/arch.
2. Extract and place `neo4j-mcp-canary` on your `PATH`.

Mac / Linux:

> On Mac, you may be warned the first time you try to run the binary. If so, approve it via **System Settings → Privacy & Security**.

```bash
chmod +x neo4j-mcp-canary
sudo mv neo4j-mcp-canary /usr/local/bin/
```

Windows (PowerShell / cmd):

```powershell
move neo4j-mcp-canary.exe C:\Windows\System32
```

Verify the installation:

```bash
neo4j-mcp-canary -v
```

Should print the installed version.

## Building from Source

Requires Go 1.26+ (see `go.mod`).

Build for your current platform with [Task](https://taskfile.dev):

```bash
task build
```

This produces `bin/neo4j-mcp-canary`. Without Task, the equivalent is:

```bash
go build -C cmd/neo4j-mcp -o ../../bin/
```

### Cross-compiling for macOS / Linux

Cross-compile by setting `GOOS`/`GOARCH` and disabling cgo (the codebase is
pure Go, so `CGO_ENABLED=0` produces a fully static binary with no runtime
dependencies on the target machine):

```bash
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -C cmd/neo4j-mcp -o ../../dist/neo4j-mcp-canary_darwin_amd64
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -C cmd/neo4j-mcp -o ../../dist/neo4j-mcp-canary_darwin_arm64
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -C cmd/neo4j-mcp -o ../../dist/neo4j-mcp-canary_linux_amd64
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -C cmd/neo4j-mcp -o ../../dist/neo4j-mcp-canary_linux_arm64
```

To stamp a version into the binary (`-v` / `--version`), pass an `ldflags`
override — this is what the release pipeline does for tagged builds:

```bash
go build -C cmd/neo4j-mcp -o ../../dist/neo4j-mcp-canary \
  -ldflags "-X 'main.Version=$(git rev-parse --short HEAD)'"
```

Without it, `Version` defaults to `"development"`, which also disables
telemetry regardless of `NEO4J_TELEMETRY` (see [Telemetry](#telemetry)).

Official multi-platform release archives (including Windows) are built by
[GoReleaser](https://goreleaser.com/) per `.goreleaser.yaml` — see
[Installation (Binary)](#installation-binary) to download those instead of
building locally.

## MCP Transport Modes

The Neo4j MCP Canary server supports two communication transport modes

- **STDIO** (default): Standard MCP communication via stdin/stdout for desktop clients (Claude Desktop, VSCode).
- **HTTP**: RESTful HTTP server with per-request Bearer token or Basic Authentication for web-based clients and multi-tenant scenarios. Where the standard `Authorization` header cannot be used, a custom header name can be configured.

### Key Differences

| Aspect               | STDIO                                                      | HTTP                                                                       |
| -------------------- | ---------------------------------------------------------- | -------------------------------------------------------------------------- |
| Startup verification | Required — server verifies APOC, connectivity, queries     | Skipped — server starts immediately                                        |
| Credentials          | Set via environment variables                              | Per-request via Bearer token or Basic Auth headers                         |
| Telemetry            | Collects Neo4j version, edition, Cypher version at startup | Reports `unknown-http-mode` — per-request credentials prevent introspection |

See the [Client Setup Guide](docs/CLIENT_SETUP.md) for configuration instructions for both modes. HTTP mode also supports an alternative **multi-instance** configuration, where one server fronts several Neo4j instances at once — see [docs/MULTI_INSTANCE.md](docs/MULTI_INSTANCE.md).

## Unauthenticated MCP Client Requests

By default, there are four requests a MCP client can send without authentication when using HTTP(S) transport. Some integrations (AWS AgentCore, AWS Gateway, etc.) rely on this as an initial health-check mechanism:

- `ping`
- `initialize`
- `tools/list`
- `notifications/initialize`

If you do not need these, enforce authentication individually via the variables below.

| Environment Variable                                         | CLI Flag                                                   | Default | Purpose                                            |
| ------------------------------------------------------------ | ---------------------------------------------------------- | ------- | -------------------------------------------------- |
| `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_PING`                      | `--neo4j-http-allow-unauthenticated-ping`                  | `true`  | Allow unauthenticated ping health checks           |
| `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_TOOLS_LIST`                | `--neo4j-http-allow-unauthenticated-tools-list`            | `true`  | Allow unauthenticated tool listing                 |
| `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_INITIALIZE`                | `--neo4j-http-allow-unauthenticated-initialize`            | `true`  | Allow unauthenticated initialize                   |
| `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE`  | `--neo4j-http-allow-unauthenticated-notifications-initialize` | `true` | Allow unauthenticated `notifications/initialize` |

## TLS/HTTPS Configuration

When using HTTP transport, enable TLS for secure communication via the variables below.

| Environment Variable            | CLI Flag                       | Default                                  | Purpose                                   |
| ------------------------------- | ------------------------------ | ---------------------------------------- | ----------------------------------------- |
| `NEO4J_MCP_HTTP_TLS_ENABLED`    | `--neo4j-http-tls-enabled`     | `false`                                  | Enable TLS/HTTPS                          |
| `NEO4J_MCP_HTTP_TLS_CERT_FILE`  | `--neo4j-http-tls-cert-file`   | —                                        | Path to TLS certificate (required w/ TLS) |
| `NEO4J_MCP_HTTP_TLS_KEY_FILE`   | `--neo4j-http-tls-key-file`    | —                                        | Path to TLS private key (required w/ TLS) |
| `NEO4J_MCP_HTTP_PORT`           | `--neo4j-http-port`            | `443` with TLS, `80` without             | HTTP server port                          |
| `NEO4J_HTTP_AUTH_HEADER_NAME`   | `--neo4j-http-auth-header-name`| `Authorization`                          | Header name to read credentials from      |
| `NEO4J_MCP_HTTP_TOOLS_HEADER_NAME` | `--neo4j-mcp-http-tools-header-name` | `X-MCP-Tools`                  | Header a client uses to select tools by name for one request |
| `NEO4J_MCP_HTTP_TOOL_CATEGORIES_HEADER_NAME` | `--neo4j-mcp-http-tool-categories-header-name` | `X-MCP-Tool-Categories` | Header a client uses to select tools by category for one request |

### Security Configuration

- **Minimum TLS Version:** TLS 1.2 (TLS 1.3 negotiated when available)
- **Cipher Suites:** Go's secure default cipher suites
- **Default Port:** Automatically uses 443 when TLS is enabled

**Example**

```bash
export NEO4J_URI="bolt://localhost:7687"
export NEO4J_TRANSPORT_MODE="http"
export NEO4J_MCP_HTTP_TLS_ENABLED="true"
export NEO4J_MCP_HTTP_TLS_CERT_FILE="/path/to/cert.pem"
export NEO4J_MCP_HTTP_TLS_KEY_FILE="/path/to/key.pem"

neo4j-mcp-canary
# Server listens on https://127.0.0.1:443 by default
```

**Production Usage:** use certificates from a trusted CA (Let's Encrypt, your organisation's CA, etc.) for production deployments.

For detailed instructions on certificate generation, TLS testing, and production deployment, see [CONTRIBUTING.md](CONTRIBUTING.md#tlshttps-configuration).

## Configuration Options

The `neo4j-mcp-canary` server is configured via environment variables, CLI flags, and/or an optional config file. **CLI flags take precedence over environment variables, which take precedence over an optional config file.**

### Environment Variables

Core connection and behaviour:

| Environment Variable                | Default   | Purpose                                                                        |
| ------------------------------------ | --------- | ------------------------------------------------------------------------------ |
| `NEO4J_URI`                          | —         | Neo4j connection URI (required)                                                |
| `NEO4J_USERNAME`                     | —         | Database username (required in STDIO mode; must be unset in HTTP mode)        |
| `NEO4J_PASSWORD`                     | —         | Database password (required in STDIO mode; must be unset in HTTP mode)        |
| `NEO4J_DATABASE`                     | `neo4j`   | Database name                                                                  |
| `NEO4J_READ_ONLY`                    | `false`   | When `true`, the `write-cypher` tool is not registered                        |
| `NEO4J_MCP_ENABLED_TOOLS`            | _(all)_   | Comma-separated tool names to enable; empty enables every tool                |
| `NEO4J_MCP_ENABLED_TOOL_CATEGORIES`  | _(all)_   | Comma-separated categories (`cypher`, `gds`, `feedback`, `search`, `graph`, `traversal`, `patterns`) to enable             |
| `NEO4J_TELEMETRY`                    | `true`    | Enable/disable anonymous telemetry                                            |
| `NEO4J_SCHEMA_SAMPLE_SIZE`           | `1000`    | Nodes per label APOC examines when inferring schema                           |
| `NEO4J_LOG_LEVEL`                    | `info`    | `debug`, `info`, `notice`, `warning`, `error`, `critical`, `alert`, `emergency` |
| `NEO4J_LOG_FORMAT`                   | `text`    | `text` or `json`                                                               |
| `NEO4J_OUTPUT_FORMAT`                | `json`    | Tool response format sent to the LLM client: `json`, `toon`, or `markdown`     |
| `NEO4J_TRANSPORT_MODE`               | `stdio`   | `stdio` or `http` (supersedes the deprecated `NEO4J_MCP_TRANSPORT`)            |
| `NEO4J_MCP_EMBEDDING_PROVIDER`       | —         | GenAI embedding provider for `set-vector-property`'s `text` field / `check-embedding-dimensions`: `openai`, `azure-openai`, `vertexai`, or `bedrock-titan`; unset disables both |
| `NEO4J_MCP_EMBEDDING_CONFIGURATION`  | —         | Comma-separated `key=value` settings for the provider above (e.g. `token=sk-xxx,model=text-embedding-3-small`) — see [Multi-Instance HTTP Mode](docs/MULTI_INSTANCE.md#embedding-provider-optional) for the required keys per provider; mutually exclusive with `neo4j_instances` (configure embedding per instance there instead) |

#### Connecting via the Query API instead of Bolt

`NEO4J_URI`'s scheme determines which wire protocol the server uses to talk
to Neo4j — no separate flag is needed:

- `bolt://`, `bolt+s://`, `neo4j://`, `neo4j+s://`, etc. → the Bolt driver (default, unchanged behaviour).
- `http://` or `https://` → the [Neo4j Query API](https://neo4j.com/docs/query-api/current/), Neo4j's HTTP-based query interface. Useful for deployments that only expose HTTP or otherwise prefer not to use Bolt.

Query API mode requires Neo4j **2026.07** or newer (calendar-versioned
releases) or **5.26-aura** or newer (classic-versioned Aura releases only —
a bare classic version with no `-aura` suffix is not supported, since
self-managed classic servers predate the typed-JSON media type version this
server's Query API client depends on). This floor is one release past the
Query API's own general availability (2026.06): read-cypher's write-query
rejection depends on the `queryType` field in the query response, which
Neo4j only introduced in 2026.07 — a 2026.06 server has no reliable signal
to classify a query as read-only before running it. The server checks the
connected instance's reported version against this floor at startup (via an
unauthenticated GET to the base URI) and refuses to start if it's too old,
with an error naming the version it found and the minimum required.

`NEO4J_USERNAME`/`NEO4J_PASSWORD` and per-request Basic/Bearer credentials
work the same way in Query API mode as they do for Bolt — see
[Transport Modes](#transport-modes) and
[Authentication Methods (HTTP Mode)](#authentication-methods-http-mode).

Cypher execution safeguards (see [Cypher Execution Safeguards](#cypher-execution-safeguards)):

| Environment Variable                | Default     | Purpose                                                                 |
| ----------------------------------- | ----------- | ----------------------------------------------------------------------- |
| `NEO4J_CYPHER_MAX_ROWS`             | `1000`      | Per-call row cap on `read-cypher` / `write-cypher`; `0` disables        |
| `NEO4J_CYPHER_MAX_BYTES`            | `900000`    | Combined per-call byte budget (~900 KB) for text+structured output; `0` disables |
| `NEO4J_CYPHER_TIMEOUT`              | `30`        | Execution timeout in seconds; `0` disables                              |
| `NEO4J_CYPHER_MAX_ESTIMATED_ROWS`   | `1000000`   | EXPLAIN-time planner estimate above which `read-cypher` refuses a query; `0` disables |

HTTP transport, TLS, and auth (see tables above).

### CLI Flags

You can override any environment variable using CLI flags:

```bash
neo4j-mcp-canary \
  --neo4j-uri "bolt://localhost:7687" \
  --neo4j-username "neo4j" \
  --neo4j-password "password" \
  --neo4j-database "neo4j" \
  --neo4j-read-only false \
  --neo4j-telemetry true
```

Available flags:

**Connection & behaviour**

- `--neo4j-uri` — overrides `NEO4J_URI`
- `--neo4j-username` — overrides `NEO4J_USERNAME`
- `--neo4j-password` — overrides `NEO4J_PASSWORD`
- `--neo4j-database` — overrides `NEO4J_DATABASE`
- `--neo4j-read-only` — overrides `NEO4J_READ_ONLY` (`true` / `false`)
- `--neo4j-mcp-enabled-tools` — overrides `NEO4J_MCP_ENABLED_TOOLS` (comma-separated tool names)
- `--neo4j-mcp-enabled-tool-categories` — overrides `NEO4J_MCP_ENABLED_TOOL_CATEGORIES` (comma-separated categories)
- `--neo4j-telemetry` — overrides `NEO4J_TELEMETRY` (`true` / `false`)
- `--neo4j-schema-sample-size` — overrides `NEO4J_SCHEMA_SAMPLE_SIZE`
- `--neo4j-output-format` — overrides `NEO4J_OUTPUT_FORMAT` (`json` / `toon` / `markdown`)
- `--neo4j-mcp-embedding-provider` — overrides `NEO4J_MCP_EMBEDDING_PROVIDER`
- `--neo4j-mcp-embedding-configuration` — overrides `NEO4J_MCP_EMBEDDING_CONFIGURATION` (comma-separated `key=value` pairs)

**Cypher execution safeguards**

- `--neo4j-cypher-max-rows` — overrides `NEO4J_CYPHER_MAX_ROWS` (`0` disables)
- `--neo4j-cypher-max-bytes` — overrides `NEO4J_CYPHER_MAX_BYTES` (`0` disables)
- `--neo4j-cypher-timeout` — overrides `NEO4J_CYPHER_TIMEOUT` (seconds; `0` disables)
- `--neo4j-cypher-max-estimated-rows` — overrides `NEO4J_CYPHER_MAX_ESTIMATED_ROWS` (`0` disables)

**Transport / HTTP**

- `--neo4j-transport-mode` — `stdio` or `http`
- `--neo4j-http-host` — overrides `NEO4J_MCP_HTTP_HOST`
- `--neo4j-http-port` — overrides `NEO4J_MCP_HTTP_PORT`
- `--neo4j-http-allowed-origins` — overrides `NEO4J_MCP_HTTP_ALLOWED_ORIGINS` (comma-separated CORS origins)
- `--neo4j-http-tls-enabled` — overrides `NEO4J_MCP_HTTP_TLS_ENABLED`
- `--neo4j-http-tls-cert-file` — overrides `NEO4J_MCP_HTTP_TLS_CERT_FILE`
- `--neo4j-http-tls-key-file` — overrides `NEO4J_MCP_HTTP_TLS_KEY_FILE`
- `--neo4j-http-auth-header-name` — overrides `NEO4J_HTTP_AUTH_HEADER_NAME`
- `--neo4j-mcp-http-tools-header-name` — overrides `NEO4J_MCP_HTTP_TOOLS_HEADER_NAME`
- `--neo4j-mcp-http-tool-categories-header-name` — overrides `NEO4J_MCP_HTTP_TOOL_CATEGORIES_HEADER_NAME`
- `--neo4j-http-allow-unauthenticated-ping` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_PING`
- `--neo4j-http-allow-unauthenticated-tools-list` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_TOOLS_LIST`
- `--neo4j-http-allow-unauthenticated-initialize` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_INITIALIZE`
- `--neo4j-http-allow-unauthenticated-notifications-initialize` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE`

Run `neo4j-mcp-canary --help` to see the complete list with descriptions.

### Configuration File

As a lowest-priority alternative to environment variables, `neo4j-mcp-canary` can read configuration from an optional JSON or YAML file:

```bash
neo4j-mcp-canary --config-file /etc/neo4j-mcp/config.yaml
# or
NEO4J_CONFIG_FILE=/etc/neo4j-mcp/config.yaml neo4j-mcp-canary
```

Keys are the lower-cased form of the environment variable they correspond to:

```yaml
neo4j_uri: bolt://localhost:7687
neo4j_username: neo4j
neo4j_password: password
neo4j_read_only: false
neo4j_transport_mode: http
neo4j_http_tls_enabled: true
neo4j_cypher_max_rows: 500
```

The equivalent JSON is also accepted (`.json` extension). Only scalar values (strings, numbers, booleans) are supported for every key except one — a nested object or list under any other key is a startup error. Values from CLI flags or environment variables always take precedence over the config file; a `--config-file` that fails to read or parse is a startup error.

The one exception is `neo4j_instances`, a list of Neo4j instance definitions that turns on **multi-instance HTTP mode**, letting one server front several Neo4j instances at once, each reachable at its own URL path. It's configurable only via the config file — see [docs/MULTI_INSTANCE.md](docs/MULTI_INSTANCE.md) for the full config shape, per-instance auth types, and how a client selects an instance.

Adding a new configuration parameter to the server (env var + CLI flag + config-file key, all at once) means adding one entry to the `fields` slice in [`internal/config/schema.go`](internal/config/schema.go) — see that file's doc comments for the shape.

### Response Format (JSON, TOON, or Markdown)

Tool responses (every tool in the table above) are rendered as JSON by default. Set `NEO4J_OUTPUT_FORMAT` (or `--neo4j-output-format`) to `toon` or `markdown` to render them differently instead:

```bash
neo4j-mcp-canary --neo4j-output-format toon
# or
NEO4J_OUTPUT_FORMAT=toon neo4j-mcp-canary
```

A `read-cypher` result as JSON:

```json
{
  "rows": [
    { "name": "Alice", "age": 30 },
    { "name": "Bob", "age": 25 }
  ],
  "rowCount": 2,
  "truncated": false
}
```

The same result as [TOON](https://github.com/toon-format/toon-go) (Token-Oriented Object Notation) — a compact, still human-readable format that cuts LLM token usage versus JSON, especially for the tabular row shapes these tools return:

```
rowCount: 2
rows[2]{age,name}:
  30,Alice
  25,Bob
truncated: false
```

The same result as `markdown` — a uniform array of flat rows renders as a table, nested/non-uniform data (e.g. `get-schema`'s output) renders as nested bullets:

```
- **rowCount**: 2
- **rows**:
| age | name |
| --- | --- |
| 30 | Alice |
| 25 | Bob |
- **truncated**: false
```

`markdown` is a complement to `toon`, not a strict upgrade over it: independent benchmarks on tabular data found Markdown tables scoring higher _accuracy_ than TOON despite using more tokens, while TOON still wins on raw token count. Prefer `toon` when token budget matters most; prefer `markdown` when result accuracy matters most.

An invalid value falls back to `json` with a warning on stderr, the same way `NEO4J_LOG_FORMAT` does.

### Structured output (`structuredContent` / `outputSchema`)

Every tool call result also carries [MCP's structured-output extension](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#structured-content): alongside the text block described above (in whichever format `NEO4J_OUTPUT_FORMAT` selects), the result's `structuredContent` field always carries the same data as canonical JSON, matched by an advertised `outputSchema` on the tool definition. A client that wants to consume results programmatically can read `structuredContent` directly instead of re-parsing the text block, regardless of which text format is configured.

Because `structuredContent` duplicates the payload already in the text block, `read-cypher`/`write-cypher` enforce `NEO4J_CYPHER_MAX_BYTES` as a **combined** budget: the byte cap actually applied during streaming is half the configured value, so text+structured together still stay under the ~900 KB/1 MB intent described below rather than doubling it.

## Cypher Execution Safeguards

`read-cypher` and `write-cypher` are protected by four layered safeguards that together keep an overeager LLM from hanging the MCP transport or exhausting the database. Each layer catches a different failure mode; together they act as defence in depth.

| Layer                 | Setting                           | Default     | When it fires                                           |
| --------------------- | --------------------------------- | ----------- | ------------------------------------------------------- |
| Planner estimate      | `NEO4J_CYPHER_MAX_ESTIMATED_ROWS` | `1000000`   | Before execution — query refused if the planner's root `EstimatedRows` exceeds the threshold |
| Execution timeout     | `NEO4J_CYPHER_TIMEOUT`            | `30s`       | During execution — query cancelled after the deadline  |
| Row cap               | `NEO4J_CYPHER_MAX_ROWS`           | `1000`      | During streaming — response truncated at the row limit |
| Byte cap              | `NEO4J_CYPHER_MAX_BYTES`          | `900000`    | During streaming — response truncated when the envelope grows past half the configured value (see [Structured output](#structured-output-structuredcontent--outputschema)) |

Set any value to `0` to disable that specific layer.

### Truncation envelope

When either the row cap or the byte cap fires, the tool returns the rows it has already collected plus a truncation envelope:

```json
{
  "rows": [ /* ... */ ],
  "rowCount": 1000,
  "truncated": true,
  "truncationReason": "rows",
  "maxRows": 1000,
  "hint": "Results were truncated at 1000 rows. Add a LIMIT clause or a more selective filter and retry for a complete result."
}
```

Callers (including LLM agents) can read `truncated` / `truncationReason` / `hint` programmatically and retry with a tighter query rather than seeing an opaque transport-level failure.

### Timeout and cancellation errors

When `NEO4J_CYPHER_TIMEOUT` fires, the tool returns a classified error that names the configured limit and offers tool-specific remediation (bound variable-length patterns, add `WHERE` filters, or `LIMIT` for `read-cypher`; reduce batch size, narrow the `MATCH`, or use `apoc.periodic.iterate` for `write-cypher`). Caller cancellation (as distinct from timeout) surfaces as a concise `cancelled` message without remediation guidance.

### Planner estimate refusal

The planner-estimate guard reads the root `EstimatedRows` of an `EXPLAIN` plan before the query runs. Because Neo4j folds `LIMIT` into the root estimate, a legitimate `MATCH ... LIMIT 100` query passes cleanly with an estimate of ~100, while a bare `MATCH` on a multi-million-row label is refused before it starts.

## Authentication Methods (HTTP Mode)

When using HTTP transport mode, the Neo4j MCP Canary server supports two authentication methods to accommodate different deployment scenarios. (This section covers single-instance HTTP mode; multi-instance mode has its own per-instance auth types, including server-verified Bearer tokens — see [docs/MULTI_INSTANCE.md](docs/MULTI_INSTANCE.md).)

### Bearer Token Authentication

Bearer token authentication enables seamless integration with **Neo4j Enterprise Edition** and **Neo4j Aura** environments that use SSO/OAuth/OIDC for identity management. This method is ideal for:

- Enterprise deployments with centralised identity providers (Okta, Azure AD, etc.)
- Neo4j Aura databases configured with SSO
- Organisations requiring OAuth 2.0 compliance
- Multi-factor authentication scenarios

**Example:**

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..." \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

The bearer token is obtained from your identity provider and passed to Neo4j for authentication. The MCP server acts as a pass-through, forwarding the token to Neo4j's authentication system.

### Basic Authentication

Traditional username/password authentication suitable for:

- Neo4j Community Edition
- Development and testing environments
- Direct database credentials without SSO

**Example:**

```bash
curl -X POST http://localhost:8080/mcp \
  -u neo4j:password \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

## Client Configuration

To configure MCP clients (VSCode, Claude Desktop, etc.) to use the Neo4j MCP Canary server, see:

📘 **[Client Setup Guide](docs/CLIENT_SETUP.md)** – Complete configuration for STDIO and HTTP modes.

## Tools & Usage

Provided tools:

| Tool                            | Category   | ReadOnly | Purpose                                                            | Notes                                                                                                                                      |
| -------------------------------- | ---------- | -------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `get-schema`                    | `cypher`   | `true`   | Introspect labels, relationship types, property keys               | Uses `apoc.meta.schema`. Sampling controlled by `NEO4J_SCHEMA_SAMPLE_SIZE`.                                                                |
| `read-cypher`                   | `cypher`   | `true`   | Execute arbitrary read-only Cypher                                  | Rejects writes, schema/admin DDL, `EXPLAIN`, and `PROFILE`. See [Cypher Execution Safeguards](#cypher-execution-safeguards).               |
| `write-cypher`                  | `cypher`   | `false`  | Execute arbitrary Cypher (write mode)                                | **Caution:** LLM-generated queries can cause harm. Use only in development environments. Rejects `EXPLAIN`/`PROFILE` prefixes (see below). Not registered when `NEO4J_READ_ONLY=true`. |
| `explain-cypher`                | `cypher`   | `true`   | Return a query's plan without executing it                          | Works for read and write statements alike, since `EXPLAIN` never executes. See [Query planning and profiling](#query-planning-and-profiling). |
| `profile-cypher`                | `cypher`   | `false`  | Execute a query and return results plus a profiled plan with runtime stats | `dbHits`/`rows`/`time`/page-cache stats per operator. Not registered when `NEO4J_READ_ONLY=true`.                                          |
| `list-constraints-and-indexes`  | `cypher`   | `true`   | List the database's constraints and indexes                         | Structured discovery — includes VECTOR/FULLTEXT rows if present. See [Schema management tools](#schema-management-tools).                 |
| `create-constraint`             | `cypher`   | `false`  | Create a UNIQUENESS, KEY, or PROPERTY_EXISTENCE constraint            | `KEY`/`PROPERTY_EXISTENCE` require Enterprise Edition. Structured fields only — no Cypher required.                                        |
| `drop-constraint`               | `cypher`   | `false`  | Drop a constraint by name                                            | Idempotent (`DROP CONSTRAINT ... IF EXISTS`).                                                                                              |
| `create-index`                  | `cypher`   | `false`  | Create a RANGE, TEXT, POINT, or LOOKUP index                          | VECTOR/FULLTEXT indexes are created via the `search` category's `create-vector-index`/`create-fulltext-index` instead.                     |
| `drop-index`                    | `cypher`   | `false`  | Drop an index by name                                                | Works for any index type, including vector/fulltext.                                                                                       |
| `vector-search`                 | `search`   | `true`   | Approximate nearest-neighbor similarity search against a vector index | Auto-detects the target label/relationship type from the index. See [Vector and full-text search tools](#vector-and-full-text-search-tools). |
| `fulltext-search`               | `search`   | `true`   | Lucene-backed full-text search against a fulltext index               | Same auto-detection as `vector-search`.                                                                                                     |
| `create-vector-index`           | `search`   | `false`  | Create a vector index for similarity search                          | `filterableProperties` registers properties usable later in `vector-search`'s `filters`.                                                   |
| `create-fulltext-index`         | `search`   | `false`  | Create a fulltext index for text search                              | Supports multiple labels/relationship types.                                                                                                |
| `set-vector-property`           | `search`   | `false`  | Set a node or relationship's embedding vector property                | Uses `db.create.setNodeVectorProperty`/`setRelationshipVectorProperty`. Requires at least one filter. Accepts a pre-computed `vector`, or `text` to embed server-side if the instance has an embedding provider configured — see [Multi-Instance HTTP Mode](docs/MULTI_INSTANCE.md#embedding-provider-optional). |
| `check-embedding-dimensions`    | `search`   | `true`   | Validate a configured embedding provider/model against a vector index's dimensions | Generates one throwaway embedding, writes nothing. Requires an embedding provider configured on the instance.                  |
| `create-node` | `graph` | `false` | Create a node with labels and properties | `mergeOn` makes it an idempotent upsert. Returns the node's `id`. See [Graph operation tools](#graph-operation-tools). |
| `find-nodes` | `graph` | `true` | Find nodes by label, property values and/or filters | Paginated (`limit`/`skip`); `returnProperties` trims the output. |
| `get-node` | `graph` | `true` | Get one node plus its relationship counts by type and direction | Select by `id` or a unique key. |
| `update-node` | `graph` | `false` | Set/remove properties and add/remove labels on existing nodes | Guarded by `maxAffected` (default 1) and `dryRun`; needs `id`, `match` or `filters`. |
| `delete-node` | `graph` | `false` | Delete nodes | **Destructive.** `detach` required to remove related nodes; guarded by `maxAffected` and `dryRun`. |
| `count-nodes` | `graph` | `true` | Count nodes matching labels/properties/filters | Cheaper than `find-nodes` when only a number is needed. |
| `create-relationship` | `graph` | `false` | Create a directed relationship between two existing nodes | Each end must select exactly one node. Reuses an identical relationship unless `allowDuplicate`. |
| `find-relationships` | `graph` | `true` | Find relationships by type, end nodes and/or properties | Returns end-node summaries so no follow-up lookup is needed. |
| `update-relationship` | `graph` | `false` | Set/remove properties on existing relationships | Guarded by `maxAffected` and `dryRun`. |
| `delete-relationship` | `graph` | `false` | Delete relationships (end nodes are kept) | **Destructive.** Guarded by `maxAffected` and `dryRun`. |
| `traverse` | `traversal` | `true` | Walk outward from a node: reachable nodes with depth, or the paths | Bounded by `maxDepth` (max 10). For dependencies, hierarchies, impact analysis. |
| `expand-neighbors` | `traversal` | `true` | Everything exactly one hop from a node | Direction is reported relative to the start node. |
| `find-shortest-path` | `traversal` | `true` | Shortest route (or all tied shortest) between two nodes | Uses Cypher 25 `ANY`/`ALL SHORTEST`. |
| `find-all-paths` | `traversal` | `true` | Enumerate routes between two nodes, shortest first | Bounded by `maxDepth` (max 8) and `limit`. |
| `match-pattern` | `patterns` | `true` | Find occurrences of a described pattern; return rows, a count, or existence | Pattern given as nodes + relationships, never Cypher. Must be connected. |
| `find-common-connections` | `patterns` | `true` | Nodes shared by 2–5 given nodes (shared neighbors) | Collaboration, shared dependencies, mutual connections. |
| `find-motif` | `patterns` | `true` | Detect `star`, `fan-in`, `fan-out`, `triangle` or `cycle` shapes | Name `relationshipTypes`/`nodeLabels` to keep it fast; `start` anchors it. |
| `find-relationship-chain` | `patterns` | `true` | Follow an exact, ordered sequence of relationships from a node | Each step names the type, direction and optional target node criteria. |
| `find-subgraph` | `patterns` | `true` | Extract the neighborhood around a node as nodes + relationships | Capped by `maxDepth` (max 4) and `maxNodes` (max 200). |
| `list-gds-procedures`           | `gds`      | `true`   | List GDS procedures available in the Neo4j instance                  | Disabled automatically if GDS is not installed.                                                                                             |
| `give-feedback`                 | `feedback` | `true`   | Submit free-text feedback about the MCP server itself                | For feedback on the server (tools, behaviour, docs), not on Cypher/database issues. Limited to 300 characters. See [Feedback](#feedback). |

### Selecting which tools are exposed

Every tool belongs to exactly one category (`cypher`, `gds`, `feedback`, `search`, `graph`, `traversal`, or `patterns`, per the table above) and carries a label — its MCP title annotation (e.g. "Read Cypher"), shown to clients that display a friendly tool name.

Tool selection can be narrowed in two ways, which combine with the existing `NEO4J_READ_ONLY`/GDS-availability/search-version/Cypher-25 filtering (a selection can only narrow the set further, never re-enable a tool those filters already excluded):

**Statically, at startup**, via `NEO4J_MCP_ENABLED_TOOLS` and/or `NEO4J_MCP_ENABLED_TOOL_CATEGORIES` (comma-separated; a tool is kept if it matches either list — see [Configuration Options](#configuration-options)):

```bash
# Only expose the two Cypher read tools:
export NEO4J_MCP_ENABLED_TOOLS="read-cypher,get-schema"

# Only expose the cypher category (equivalent to the above plus write-cypher):
export NEO4J_MCP_ENABLED_TOOL_CATEGORIES="cypher"
```

**Per-request, in HTTP mode**, via the `X-MCP-Tools` and `X-MCP-Tool-Categories` headers (names configurable via `NEO4J_MCP_HTTP_TOOLS_HEADER_NAME` / `NEO4J_MCP_HTTP_TOOL_CATEGORIES_HEADER_NAME`). A request that omits both headers sees the full statically-enabled tool set as usual; a request that sets either header sees only the matching tools for that single request, and calling an excluded tool fails the same way calling a nonexistent one would:

```bash
curl -X POST https://your-server/mcp \
  -H "Authorization: Basic <credentials>" \
  -H "X-MCP-Tool-Categories: gds" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

### Read-only mode flag

Enable read-only mode by setting `NEO4J_READ_ONLY=true` (accepted: `true` / `false`; default: `false`).

You can also use the CLI flag:

```bash
neo4j-mcp-canary \
  --neo4j-uri "bolt://localhost:7687" \
  --neo4j-username "neo4j" \
  --neo4j-password "password" \
  --neo4j-read-only true
```

When enabled, write tools (e.g. `write-cypher`) are not exposed to clients.

### Query classification

`read-cypher` prepends `EXPLAIN` to the caller's query to classify it as read or write before executing. Consequences:

- **Write operations** (`CREATE`, `MERGE`, `DELETE`, `SET`, `REMOVE`, ...) — rejected with a message directing the caller to `write-cypher`.
- **Schema/DDL operations** (`CREATE INDEX`, `DROP CONSTRAINT`, ...) — rejected, same message.
- **Admin commands** (`SHOW USERS`, `SHOW DATABASES`, ...) — rejected, same message.
- **`EXPLAIN` prefix** — rejected with a message pointing at `explain-cypher` for the query plan, or `profile-cypher` for a profiled plan with runtime statistics.
- **`PROFILE` prefix** — rejected with a message pointing at `profile-cypher`, which executes the query and returns both the results and a profiled plan. `write-cypher` also rejects an `EXPLAIN`/`PROFILE` prefix, for the same reason — see [Query planning and profiling](#query-planning-and-profiling).
- **Read-only `SHOW` commands** (`SHOW INDEXES`, `SHOW CONSTRAINTS`, `SHOW PROCEDURES`, `SHOW FUNCTIONS`) — allowed.

If the wrapped query produces a syntax error, the server strips the internal `EXPLAIN ` prefix from the error text, column offset, and caret alignment before returning — so the error reads as if the caller's original query had been submitted directly.

### Query planning and profiling

`explain-cypher` and `profile-cypher` cover the query-tuning workflow `read-cypher`/`write-cypher` deliberately don't handle: `explain-cypher` returns the planner's plan tree for any statement — read or write — without executing it, since `EXPLAIN` never runs the query. `profile-cypher` executes the statement and returns both the results and a profiled plan with per-operator runtime statistics (`dbHits`, `rows`, `time`, page cache hit ratio). Neither tool accepts a query already prefixed with `EXPLAIN`/`PROFILE` — each adds its own prefix internally and rejects a caller-supplied one with a message pointing at the other tool if that's what was actually wanted.

### Schema management tools

`list-constraints-and-indexes`, `create-constraint`, `drop-constraint`, `create-index`, and `drop-index` manage constraints and indexes via structured fields only — none of them accept or return raw Cypher. `create-constraint` supports `UNIQUENESS`, `KEY`, and `PROPERTY_EXISTENCE` constraints (`KEY`/`PROPERTY_EXISTENCE` require Enterprise Edition; Community Edition rejects them at creation time — retrying won't help). `create-index` supports `RANGE`, `TEXT`, `POINT`, and `LOOKUP` indexes; vector and fulltext indexes are created via the `search` category's `create-vector-index`/`create-fulltext-index` instead. `drop-index` works for any index type, including vector and fulltext ones. Every `create-*` tool always includes `IF NOT EXISTS`/`IF EXISTS` in the generated statement, so retrying with the same arguments is safe; when `name` is omitted, a generated name is returned in the response for later use with the matching `drop-*` tool.

### Vector and full-text search tools

The `search` category — `vector-search`, `fulltext-search`, `create-vector-index`, `create-fulltext-index`, `set-vector-property`, `check-embedding-dimensions` — surfaces Cypher 25's `SEARCH` clause through structured fields only; no tool in this category accepts or returns raw Cypher. `vector-search`/`fulltext-search` auto-detect the target node label or relationship type from the index itself, so the caller only needs the index name. `vector-search`'s `filters` only work on properties registered as filterable at index-creation time via `create-vector-index`'s `filterableProperties` — filtering on any other property is a hard Neo4j limitation, not a tool restriction. `set-vector-property` takes either a caller-supplied embedding (`vector`) or raw `text` to embed — the latter only works when the connected Neo4j instance has an embedding provider configured, via `NEO4J_MCP_EMBEDDING_PROVIDER`/`NEO4J_MCP_EMBEDDING_CONFIGURATION` in single-instance mode (STDIO or single-instance HTTP) or via each entry's own `embedding` block in multi-instance HTTP mode (see [Multi-Instance HTTP Mode](docs/MULTI_INSTANCE.md#embedding-provider-optional) for the full provider/required-key table, which applies to both). Who actually generates it depends on the provider: `openai` is generated by this MCP server itself via a direct HTTP call (its `baseUrl` config key can redirect this to a local OpenAI-compatible server like LM Studio or Ollama, with no Neo4j-side involvement at all), while `azure-openai`/`vertexai`/`bedrock-titan` are generated by Neo4j's own GenAI plugin (`ai.text.embed`) instead, reusing the auth schemes it already implements. Either way, if a matching vector index already exists, the resulting vector's dimensions are checked against it before writing, and `check-embedding-dimensions` lets a caller validate a provider/model against an index up front without writing anything.

This category requires a server on calendar version >= `2026.09.0` or classic Aura >= `5.27-aura` (native full-text `SEARCH`-clause support shipped in Neo4j's 2026.09 release; vector search alone works from `2026.01`, but the whole category is gated behind the higher floor for consistency). Below that floor, the six tools are automatically excluded from `tools/list` rather than failing at call time — the same way `list-gds-procedures` is excluded when GDS isn't installed.

### Graph operation tools

The `graph`, `traversal` and `patterns` categories let an agent create, read, update, delete and explore graph data **without writing any Cypher**. Every input is a structured field; no tool in these categories accepts or returns raw Cypher. Values are always bound as query parameters, labels/types/property keys are backtick-escaped, and filter operators and enums are checked against a fixed list before anything reaches the database.

- **`graph`** — node and relationship CRUD: `create-node`, `find-nodes`, `get-node`, `update-node`, `delete-node`, `count-nodes`, `create-relationship`, `find-relationships`, `update-relationship`, `delete-relationship`.
- **`traversal`** — navigating connections: `traverse` (reachability, dependencies, hierarchies), `expand-neighbors` (one hop), `find-shortest-path`, `find-all-paths`.
- **`patterns`** — structural questions: `match-pattern`, `find-common-connections`, `find-motif`, `find-relationship-chain`, `find-subgraph`.

How the tools fit together:

- Every node and relationship is returned with an `id` (its `elementId`) that every other tool accepts, so results chain without any query writing. Nodes are selected the same way everywhere: `id`, `labels`, an exact-equality `match` map, and/or `filters` (`{property, operator, value}` with `=`, `<>`, `<`, `>`, `<=`, `>=`, `IN`, `CONTAINS`, `STARTS WITH`, `ENDS WITH`, `IS NULL`, `IS NOT NULL`).
- Results are lean: a property you ask for that a node or relationship doesn't have is omitted rather than returned as `null`.
- Everything is bounded: results have a default and a hard-capped `limit`, variable-length tools have a capped `maxDepth`, and the usual row cap, byte cap and `NEO4J_CYPHER_TIMEOUT` apply. A timeout error tells the caller how to narrow the request.
- Write tools are safe by construction. `update-*` and `delete-*` refuse to run on a label or type alone, change at most `maxAffected` entities (default 1, max 1000) and report `performed: false` with the match count instead of acting when the selection is larger, and accept `dryRun` to preview the effect. `delete-node` will not delete a node that still has relationships unless `detach` is set. All write tools are annotated as such, so they are not registered when `NEO4J_READ_ONLY=true`.
- Tool descriptions say when to use a tool instead of its siblings, and error messages say what was wrong, what is valid, and what to do next.

These tools emit Cypher 25. When the connected server does not support it (calendar-versioned releases before `2025.06`, self-managed classic 5.x servers, or classic Aura before `5.26-aura`), the three categories are excluded from `tools/list` rather than failing at call time — the same way `list-gds-procedures` is excluded when GDS isn't installed.

### Tool design principles

New tools follow these principles. They are also listed in [AGENTS.md](AGENTS.md) for AI coding agents working in this repository.

1. **Intent** — a tool solves one specific task a user would name. Name it verb-noun in kebab-case, and say in its description when to use it instead of a sibling tool.
2. **Context** — a tool must not confuse or bloat the model. Keep parameter lists short, prefer enums to free text, and say what the tool returns and does not do. Every tool is context every client pays for, so merge overlapping tools instead of adding near-duplicates, and place tools in categories so deployments can switch off what they don't need.
3. **Granularity** — combine the 3–4 atomic operations that always travel together into one high-level tool (resolve the target, check the guard, write, return the result). Combine by *task*, not by piling modes behind a flag.
4. **Efficiency** — return only the fields the LLM needs: ids for chaining, summaries by default, projection options for the rest, and a default and hard-capped `limit`/`maxDepth`.
5. **Safety** — a dangerous tool (delete, overwrite, anything irreversible) must be declared as such: `readOnly: false`, a destructive-hint annotation, and removal under `NEO4J_READ_ONLY`. Keep read and write in separate tools. Require an exact target (never "everything with label X"), cap the blast radius (`maxAffected`) and offer a `dryRun`.
6. **Error handling** — an error must tell the LLM how to fix its mistake: what was wrong, what the valid values are, what to try next, and whether retrying can help at all.
7. **Parameters only** — a tool takes structured fields, never Cypher. Values are bound as parameters, identifiers are escaped, operators and enums are whitelisted.
8. **Bounded** — every tool has hard caps on rows, depth and time so no single call can run away.
9. **Idempotent** — prefer operations that are safe to retry (`MERGE`, `IF NOT EXISTS`, set-to-value), and say in the description when a retry is safe.
10. **Composable** — outputs have a stable shape and carry the ids other tools accept as inputs.
11. **Verified** — the Cypher a tool generates is built by a pure function with unit tests, and every tool that touches the database has an integration test.

### Response format for `read-cypher` / `write-cypher`

Driver types are wrapped in camelCase JSON shapes matching Cypher conventions:

- **Nodes:** `{ "elementId": "...", "labels": [...], "properties": {...} }`
- **Relationships:** `{ "elementId": "...", "startElementId": "...", "endElementId": "...", "type": "...", "properties": {...} }`
- **Paths:** `{ "nodes": [...], "relationships": [...] }`
- **Points:** `{ "x": ..., "y": ..., "srid": ... }` (and `z` for 3D)
- **Date / Time / DateTime / LocalTime / LocalDateTime / Duration:** ISO 8601 strings

Deprecated numeric `id` / `startId` / `endId` identifiers are **not** surfaced — `elementId` / `startElementId` / `endElementId` are the only identifiers returned.

### Feedback

`give-feedback` lets an agent submit free-text feedback about the MCP server itself — positive or negative — as a single `feedback` string argument, capped at 300 characters (enforced both in the advertised tool schema and by the handler, in case a client doesn't validate the schema before sending). It's for feedback on the server's tools, behaviour, or documentation, not for reporting Cypher/database errors.

Feedback is sent as a Mixpanel event alongside the server's other telemetry, so it is only recorded when telemetry is enabled (see [Telemetry](#telemetry)) — the tool call itself always succeeds either way.

## Usage Guidance

Lessons from canary testing that help an LLM (or a human) get the most out of `read-cypher`:

1. **Aggregate in the database.** `count`, `sum`, `avg`, `collect`, `reduce`, `percentileCont`, `stDev`, and similar reductions collapse to one row and are unaffected by the row cap. A query like `UNWIND range(1, 50000) AS i RETURN sum(i)` runs cleanly; the same range streamed row-by-row is truncated at the row cap.
2. **Always use `LIMIT` for exploratory queries.** The row cap will truncate bare `MATCH` returns; the truncation envelope's `hint` field will tell the caller to add a `LIMIT`. Prefer a `LIMIT` you picked over one the server imposed.
3. **Narrow the `RETURN` projection for wide nodes.** When a record carries many properties (e.g. a full Company node with 19 fields), the byte cap fires before the row cap. Return only the fields you need (`RETURN c.name, c.companyNumber`) rather than the whole node.
4. **Use parameters, including nested maps.** Parameter placeholders (`$name`) are bound from the `params` object; nested access works (`$config.thresholds.pr`). Missing required parameters produce a clear `ParameterMissing` error; extra parameters are silently ignored.
5. **Be explicit about types in comparisons.** Cross-type comparisons like `t.amount > "foo"` evaluate to null and silently filter everything out — no error, just an empty result set. Validate incoming parameter types on the caller side when the result shape surprises you.
6. **`SHOW INDEXES` / `SHOW CONSTRAINTS` are allowed.** Useful before writing a query that depends on an index, or for debugging why a match is slow.
7. **`EXPLAIN` and `PROFILE` are not exposed on `read-cypher`/`write-cypher`.** Runaway-query protection is already handled by the planner-estimate guard and execution timeout. Use `explain-cypher` for the query plan, or `profile-cypher` for a profiled plan with runtime stats.
8. **Watch for duplicated payloads when returning paths.** `RETURN p, nodes(p), relationships(p)` triples the serialised payload. Return the path or its components, not both.
9. **Long-running queries return a classified error.** When `NEO4J_CYPHER_TIMEOUT` fires, the error names the timeout value and suggests remediation (bound variable-length patterns, add `WHERE` filters, use `LIMIT`) instead of a raw `context deadline exceeded` from the driver.
10. **`OPTIONAL MATCH` for missing data.** When looking up by ID where some IDs may not exist, `OPTIONAL MATCH` returns nulls for misses instead of dropping rows — better for batch lookups.
11. **Defaults are calibrated, not arbitrary.** `1000` rows / `~900 KB` / `30s` / `1M` planner estimate cover the overwhelming majority of exploratory and production queries. Increase them for bulk export workloads; reduce them when serving high-traffic agent deployments.

## Example Natural Language Prompts

Prompts to try in Copilot or any other MCP client:

- "What does my Neo4j instance contain? List all node labels, relationship types, and property keys."
- "Find all Person nodes and show their top relationships, limited to 50 results."
- "What indexes and constraints exist on my database?"
- "Summarise the transaction graph: total count, average amount, and the top 5 customers by PageRank."

## Security tips

- Use a restricted Neo4j user for exploration.
- Review LLM-generated Cypher before executing it in production databases.
- Keep `NEO4J_READ_ONLY=true` for any deployment that shouldn't mutate the graph.
- Leave the Cypher safeguards at their defaults unless you have a specific reason to change them.

## Logging

The server uses structured logging with support for multiple log levels and output formats.

### Configuration

**Log Level** (`NEO4J_LOG_LEVEL`, default: `info`)

Controls verbosity. Supports all [MCP log levels](https://modelcontextprotocol.io/specification/2025-03-26/server/utilities/logging#log-levels): `debug`, `info`, `notice`, `warning`, `error`, `critical`, `alert`, `emergency`.

**Log Format** (`NEO4J_LOG_FORMAT`, default: `text`)

- `text` — human-readable (default)
- `json` — structured JSON (useful for log aggregation)

## Telemetry

By default, `neo4j-mcp-canary` collects anonymous usage data to help improve the product. This includes information such as the tools being used, the operating system, and CPU architecture. No personal or sensitive information is collected.

To disable telemetry, set `NEO4J_TELEMETRY=false` (accepted: `true` / `false`; default: `true`). You can also use the `--neo4j-telemetry` CLI flag.

## Documentation

📘 **[Client Setup Guide](docs/CLIENT_SETUP.md)** – Configure VSCode, Claude Desktop, and other MCP clients (STDIO and HTTP modes)
📚 **[Contributing Guide](CONTRIBUTING.md)** – Contribution workflow, development environment, mocks & testing

Issues / feedback: open a GitHub issue with reproduction details (omit sensitive data).
