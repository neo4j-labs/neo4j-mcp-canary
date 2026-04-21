<!-- mcp-name: io.github.neo4j-labs/neo4j-mcp-canary -->

# Neo4j MCP Canary — _The canary goes first so the rest of us know what's coming_

Neo4j MCP Canary is a fast-moving, experimental release of the Neo4j MCP server for customers who want to explore emerging capabilities before they are considered for the official server.

Built on the source of the official Model Context Protocol (MCP) server for Neo4j, this variant is here for exploring potential new capabilities with experimentation.

As it is a labs project, be aware that:

- It is not supported.
- It may contain breaking changes between its own releases and with the official Neo4j MCP server.
- It should be tested before using.

You are welcome to contribute — we are always open to new ideas, especially in this canary channel.

> Do not assume the canary will work for your situation. Test first.

## Prerequisites

- A running Neo4j database instance; options include [Aura](https://neo4j.com/product/auradb/), [Neo4j Desktop](https://neo4j.com/download/), or [self-managed](https://neo4j.com/deployment-center/#gdb-tab).
- APOC plugin installed in the Neo4j instance (required — `get-schema` uses `apoc.meta.schema`).
- Any MCP-compatible client (e.g. [VSCode](https://code.visualstudio.com/) with [MCP support](https://code.visualstudio.com/docs/copilot/customization/mcp-servers)).

> **⚠️ Known Issue**: Neo4j **5.26.18** has a bug in APOC that causes the `get-schema` tool to fail. This is fixed in **5.26.19** and above. If you're on 5.26.18, please upgrade. See [#136](https://github.com/neo4j-labs/neo4j-mcp-canary/issues/136) for details.

## Startup Checks & Adaptive Operation

The server performs several pre-flight checks at startup to ensure your environment is correctly configured.

**STDIO Mode — Mandatory Requirements**
In STDIO mode, the server verifies the following. If any check fails (e.g. invalid configuration, incorrect credentials, missing APOC), the server will not start:

- A valid connection to your Neo4j instance.
- The ability to execute queries.
- The presence of the APOC plugin.

**HTTP Mode — Verification Skipped**
In HTTP mode, startup verification checks are skipped because credentials come from per-request auth headers. The server starts immediately without connecting to Neo4j.

**Optional Requirements**
If an optional dependency is missing, the server starts in adaptive mode. For instance, if the Graph Data Science (GDS) library is not detected, the server still launches but automatically disables GDS-dependent tools such as `list-gds-procedures`. All other tools remain available.

## Installation (Binary)

Releases: https://github.com/neo4j-labs/neo4j-mcp-canary/releases

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

## Transport Modes

The Neo4j MCP Canary server supports two transport modes:

- **STDIO** (default): Standard MCP communication via stdin/stdout for desktop clients (Claude Desktop, VSCode).
- **HTTP**: RESTful HTTP server with per-request Bearer token or Basic Authentication for web-based clients and multi-tenant scenarios. Where the standard `Authorization` header cannot be used, a custom header name can be configured.

### Key Differences

| Aspect               | STDIO                                                      | HTTP                                                                       |
| -------------------- | ---------------------------------------------------------- | -------------------------------------------------------------------------- |
| Startup verification | Required — server verifies APOC, connectivity, queries     | Skipped — server starts immediately                                        |
| Credentials          | Set via environment variables                              | Per-request via Bearer token or Basic Auth headers                         |
| Telemetry            | Collects Neo4j version, edition, Cypher version at startup | Reports `unknown-http-mode` — per-request credentials prevent introspection |

See the [Client Setup Guide](docs/CLIENT_SETUP.md) for configuration instructions for both modes.

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
| `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE`  | `--neo4j-http-allow-unauthenticated-notifications-initialize` | `true`  | Allow unauthenticated `notifications/initialize`   |

## TLS/HTTPS Configuration

When using HTTP transport, enable TLS for secure communication via the variables below.

| Environment Variable            | CLI Flag                       | Default                                  | Purpose                                   |
| ------------------------------- | ------------------------------ | ---------------------------------------- | ----------------------------------------- |
| `NEO4J_MCP_HTTP_TLS_ENABLED`    | `--neo4j-http-tls-enabled`     | `false`                                  | Enable TLS/HTTPS                          |
| `NEO4J_MCP_HTTP_TLS_CERT_FILE`  | `--neo4j-http-tls-cert-file`   | —                                        | Path to TLS certificate (required w/ TLS) |
| `NEO4J_MCP_HTTP_TLS_KEY_FILE`   | `--neo4j-http-tls-key-file`    | —                                        | Path to TLS private key (required w/ TLS) |
| `NEO4J_MCP_HTTP_PORT`           | `--neo4j-http-port`            | `443` with TLS, `80` without             | HTTP server port                          |
| `NEO4J_HTTP_AUTH_HEADER_NAME`   | `--neo4j-http-auth-header-name`| `Authorization`                          | Header name to read credentials from      |

**Security Configuration**

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

The `neo4j-mcp-canary` server is configured via environment variables and/or CLI flags. **CLI flags take precedence over environment variables.**

### Environment Variables

Core connection and behaviour:

| Environment Variable              | Default   | Purpose                                                                  |
| --------------------------------- | --------- | ------------------------------------------------------------------------ |
| `NEO4J_URI`                       | —         | Neo4j connection URI (required)                                          |
| `NEO4J_USERNAME`                  | —         | Database username (required in STDIO mode; must be unset in HTTP mode)   |
| `NEO4J_PASSWORD`                  | —         | Database password (required in STDIO mode; must be unset in HTTP mode)   |
| `NEO4J_DATABASE`                  | `neo4j`   | Database name                                                            |
| `NEO4J_READ_ONLY`                 | `false`   | When `true`, the `write-cypher` tool is not registered                   |
| `NEO4J_TELEMETRY`                 | `true`    | Enable/disable anonymous telemetry                                       |
| `NEO4J_SCHEMA_SAMPLE_SIZE`        | `1000`    | Nodes per label APOC examines when inferring schema                      |
| `NEO4J_LOG_LEVEL`                 | `info`    | `debug`, `info`, `notice`, `warning`, `error`, `critical`, `alert`, `emergency` |
| `NEO4J_LOG_FORMAT`                | `text`    | `text` or `json`                                                         |
| `NEO4J_TRANSPORT_MODE`            | `stdio`   | `stdio` or `http` (supersedes the deprecated `NEO4J_MCP_TRANSPORT`)      |

Cypher execution safeguards (see [Cypher Execution Safeguards](#cypher-execution-safeguards)):

| Environment Variable                | Default     | Purpose                                                                 |
| ----------------------------------- | ----------- | ----------------------------------------------------------------------- |
| `NEO4J_CYPHER_MAX_ROWS`             | `1000`      | Per-call row cap on `read-cypher` / `write-cypher`; `0` disables        |
| `NEO4J_CYPHER_MAX_BYTES`            | `900000`    | Per-call byte cap (~900 KB) on the response envelope; `0` disables      |
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
- `--neo4j-telemetry` — overrides `NEO4J_TELEMETRY` (`true` / `false`)
- `--neo4j-schema-sample-size` — overrides `NEO4J_SCHEMA_SAMPLE_SIZE`

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
- `--neo4j-http-allow-unauthenticated-ping` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_PING`
- `--neo4j-http-allow-unauthenticated-tools-list` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_TOOLS_LIST`
- `--neo4j-http-allow-unauthenticated-initialize` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_INITIALIZE`
- `--neo4j-http-allow-unauthenticated-notifications-initialize` — overrides `NEO4J_HTTP_ALLOW_UNAUTHENTICATED_NOTIFICATIONS_INITIALIZE`

Run `neo4j-mcp-canary --help` to see the complete list with descriptions.

## Cypher Execution Safeguards

`read-cypher` and `write-cypher` are protected by four layered safeguards that together keep an overeager LLM from hanging the MCP transport or exhausting the database. Each layer catches a different failure mode; together they act as defence in depth.

| Layer                 | Setting                           | Default     | When it fires                                           |
| --------------------- | --------------------------------- | ----------- | ------------------------------------------------------- |
| Planner estimate      | `NEO4J_CYPHER_MAX_ESTIMATED_ROWS` | `1000000`   | Before execution — query refused if the planner's root `EstimatedRows` exceeds the threshold |
| Execution timeout     | `NEO4J_CYPHER_TIMEOUT`            | `30s`       | During execution — query cancelled after the deadline  |
| Row cap               | `NEO4J_CYPHER_MAX_ROWS`           | `1000`      | During streaming — response truncated at the row limit |
| Byte cap              | `NEO4J_CYPHER_MAX_BYTES`          | `900000`    | During streaming — response truncated when the envelope grows past ~900 KB |

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

When using HTTP transport mode, the Neo4j MCP Canary server supports two authentication methods to accommodate different deployment scenarios.

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

| Tool                  | ReadOnly | Purpose                                              | Notes                                                                                                                          |
| --------------------- | -------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `get-schema`          | `true`   | Introspect labels, relationship types, property keys | Uses `apoc.meta.schema`. Sampling controlled by `NEO4J_SCHEMA_SAMPLE_SIZE`.                                                    |
| `read-cypher`         | `true`   | Execute arbitrary read-only Cypher                   | Rejects writes, schema/admin DDL, `EXPLAIN`, and `PROFILE`. See [Cypher Execution Safeguards](#cypher-execution-safeguards).   |
| `write-cypher`        | `false`  | Execute arbitrary Cypher (write mode)                | **Caution:** LLM-generated queries can cause harm. Use only in development environments. Not registered when `NEO4J_READ_ONLY=true`. |
| `list-gds-procedures` | `true`   | List GDS procedures available in the Neo4j instance  | Disabled automatically if GDS is not installed.                                                                                |

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
- **`EXPLAIN` prefix** — rejected with a dedicated message noting that runaway-query protection is already provided by the planner-estimate guard and the execution timeout, and pointing at `write-cypher` for a profiled plan.
- **`PROFILE` prefix** — rejected with a message directing the caller to `write-cypher`.
- **Read-only `SHOW` commands** (`SHOW INDEXES`, `SHOW CONSTRAINTS`, `SHOW PROCEDURES`, `SHOW FUNCTIONS`) — allowed.

If the wrapped query produces a syntax error, the server strips the internal `EXPLAIN ` prefix from the error text, column offset, and caret alignment before returning — so the error reads as if the caller's original query had been submitted directly.

### Response format for `read-cypher` / `write-cypher`

Driver types are wrapped in camelCase JSON shapes matching Cypher conventions:

- **Nodes:** `{ "elementId": "...", "labels": [...], "properties": {...} }`
- **Relationships:** `{ "elementId": "...", "startElementId": "...", "endElementId": "...", "type": "...", "properties": {...} }`
- **Paths:** `{ "nodes": [...], "relationships": [...] }`
- **Points:** `{ "x": ..., "y": ..., "srid": ... }` (and `z` for 3D)
- **Date / Time / DateTime / LocalTime / LocalDateTime / Duration:** ISO 8601 strings

Deprecated numeric `id` / `startId` / `endId` identifiers are **not** surfaced — `elementId` / `startElementId` / `endElementId` are the only identifiers returned.

## Usage Guidance

Lessons from canary testing that help an LLM (or a human) get the most out of `read-cypher`:

1. **Aggregate in the database.** `count`, `sum`, `avg`, `collect`, `reduce`, `percentileCont`, `stDev`, and similar reductions collapse to one row and are unaffected by the row cap. A query like `UNWIND range(1, 50000) AS i RETURN sum(i)` runs cleanly; the same range streamed row-by-row is truncated at the row cap.
2. **Always use `LIMIT` for exploratory queries.** The row cap will truncate bare `MATCH` returns; the truncation envelope's `hint` field will tell the caller to add a `LIMIT`. Prefer a `LIMIT` you picked over one the server imposed.
3. **Narrow the `RETURN` projection for wide nodes.** When a record carries many properties (e.g. a full Company node with 19 fields), the byte cap fires before the row cap. Return only the fields you need (`RETURN c.name, c.companyNumber`) rather than the whole node.
4. **Use parameters, including nested maps.** Parameter placeholders (`$name`) are bound from the `params` object; nested access works (`$config.thresholds.pr`). Missing required parameters produce a clear `ParameterMissing` error; extra parameters are silently ignored.
5. **Be explicit about types in comparisons.** Cross-type comparisons like `t.amount > "foo"` evaluate to null and silently filter everything out — no error, just an empty result set. Validate incoming parameter types on the caller side when the result shape surprises you.
6. **`SHOW INDEXES` / `SHOW CONSTRAINTS` are allowed.** Useful before writing a query that depends on an index, or for debugging why a match is slow.
7. **`EXPLAIN` and `PROFILE` are not exposed on `read-cypher`.** Runaway-query protection is already handled by the planner-estimate guard and execution timeout. If you need a profiled plan with runtime stats, use `write-cypher` with `PROFILE`.
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
