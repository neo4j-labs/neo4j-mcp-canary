# Multi-Instance HTTP Mode

Multi-instance mode lets a single running server hold connection details for
**several** Neo4j instances at once, each reachable at its own URL path. It's
an alternative to the single-instance `NEO4J_URI`/`NEO4J_USERNAME`/
`NEO4J_PASSWORD` configuration described in the [Client Setup
Guide](CLIENT_SETUP.md) — the two are mutually exclusive on the same server.

It exists for the case where one MCP server process should front multiple
Neo4j databases (different environments, different customers, different
teams) without the operator running one server process per database.

## Enabling it

Multi-instance mode is configured **only** via the config file's
`neo4j_instances` key — it cannot be set with an environment variable or CLI
flag, since it's a list of objects rather than a single value (see
[README.md's Configuration File
section](../README.md#configuration-file)). Setting `neo4j_instances` also
requires `neo4j_transport_mode: http` and forbids the top-level
`neo4j_uri`/`neo4j_username`/`neo4j_password` fields — the instances list
replaces them entirely.

```bash
neo4j-mcp-canary --config-file /etc/neo4j-mcp/config.yaml
```

```yaml
# /etc/neo4j-mcp/config.yaml
neo4j_transport_mode: http

neo4j_instances:
  - name: prod
    uri: neo4j+s://prod.databases.neo4j.io
    database: neo4j
    auth:
      type: basic
      username: mcp_service
      password: ${NEO4J_PROD_PASSWORD}
      api_keys:
        - ${MCP_PROD_API_KEY}
    embedding: # optional — see "Embedding provider (optional)" below
      provider: openai
      configuration:
        token: ${OPENAI_API_KEY}
        model: text-embedding-3-small

  - name: staging
    uri: neo4j://staging.internal:7687
    database: neo4j
    auth:
      type: basic_passthrough

  - name: analytics
    uri: neo4j+s://analytics.databases.neo4j.io
    database: neo4j
    auth:
      type: bearer
      issuer: https://login.microsoftonline.com/xxxx/v2.0
      jwks_uri: https://login.microsoftonline.com/xxxx/discovery/v2.0/keys
      audience: api://neo4j-mcp
```

`${VAR}` references are resolved from the process environment at startup —
if a referenced variable isn't set, the server refuses to start rather than
silently connecting with an empty secret. A field can also just be a
plaintext value; the two styles can be mixed freely within one config file.

Each instance's `database` defaults to `neo4j` if omitted. Each `name` must
be a safe, single URL path segment (letters, digits, `-`, `_`; no `/`) and
unique across the list — it becomes part of the URL a client connects to,
below. The `embedding` block on `prod` above is optional — see "Embedding
provider (optional)" further down for what it does and the other supported
providers; an instance with no `embedding` block still works, it just can't
use `set-vector-property`'s `text` field.

## How a client reaches an instance

A client connects to `{host}/{name}/mcp` instead of the single-instance
`{host}/mcp` — for the example above, `https://mcp.example.com/prod/mcp`,
`https://mcp.example.com/staging/mcp`, and
`https://mcp.example.com/analytics/mcp`. An unconfigured path segment simply
404s. Every configured instance exposes the same MCP tools
(`read-cypher`, `write-cypher`, etc.) — only the Neo4j instance a call
actually runs against differs by which route it came in on.

## Connection-auth types

Each instance's `auth.type` controls both how *this server* authenticates
to Neo4j, and what a client must present to use that instance at all:

### `basic` — static service account + required API key

The username/password are this server's own fixed Neo4j credentials for
that instance — a client never sends Neo4j credentials for it. Because
there's otherwise nothing distinguishing one caller from another, a client
**must** present one of the configured `api_keys` in the API-key header
(default `X-Neo4j-MCP-Api-Key`, configurable via
`NEO4J_MCP_HTTP_API_KEY_HEADER_NAME`) or the request is rejected — this
closes the gap a bare static service account would otherwise leave, where
anyone who could reach the server at all could use it unauthenticated.

```bash
curl -X POST https://mcp.example.com/prod/mcp \
  -H "X-Neo4j-MCP-Api-Key: <one of the configured api_keys>" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

### `basic_passthrough` — client's own Basic credentials

Behaves exactly like single-instance HTTP mode's Basic Auth today: the
client's own `Authorization: Basic ...` credentials are required and
forwarded to Neo4j as-is for every call. No `username`/`password`/`api_keys`
are configured on the instance itself — there's nothing for this server to
supply.

```bash
curl -X POST https://mcp.example.com/staging/mcp \
  -u someuser:somepassword \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

### `bearer` — verified client token, forwarded to Neo4j

The client presents `Authorization: Bearer <token>`. This server verifies
the token itself — signature (against `jwks_uri`), `issuer`, `audience`,
and expiry — before forwarding the same token to Neo4j, which must
separately be configured to trust that identity provider (Neo4j Enterprise
or Aura with SSO/OIDC). Verification is restricted to asymmetric signing
algorithms (RS/ES/PS-family); a symmetric or `alg: none` token is always
rejected regardless of what it claims.

This server never runs the login/redirect flow itself — that's the MCP
client's responsibility, per the [MCP Authorization
spec](https://modelcontextprotocol.io/specification/draft/basic/authorization).
To make that possible, each `bearer`-type instance serves its own [RFC
9728](https://www.rfc-editor.org/rfc/rfc9728) protected-resource metadata
document at
`/.well-known/oauth-protected-resource/{name}/mcp`, naming that instance's
`resource` and its one trusted `authorization_servers` entry (`issuer`). A
request with a missing or invalid token gets a `401` whose
`WWW-Authenticate` header points at that document
(`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/analytics/mcp"`),
so a spec-compliant client can discover where to log in.

```bash
curl https://mcp.example.com/.well-known/oauth-protected-resource/analytics/mcp
# {"resource":"https://mcp.example.com/analytics/mcp","authorization_servers":["https://login.microsoftonline.com/xxxx/v2.0"]}

curl -X POST https://mcp.example.com/analytics/mcp \
  -H "Authorization: Bearer <token from that identity provider>" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

## Embedding provider (optional)

An instance can optionally configure an embedding provider so the `search`
category's `set-vector-property` tool can generate an embedding server-side
(its `text` field) instead of requiring the caller to supply a pre-computed
`vector`; `check-embedding-dimensions` also uses it to validate a
provider/model against an index without writing anything. Add an
`embedding` block to that instance's entry:

(Running single-instance mode instead — STDIO or single-instance HTTP? The
same feature is configured via the top-level `NEO4J_MCP_EMBEDDING_PROVIDER`/
`NEO4J_MCP_EMBEDDING_CONFIGURATION` environment variables/CLI flags instead
— see [README.md's Configuration
Options](../README.md#configuration-options). The `provider`/`configuration`
table below applies identically there; `NEO4J_MCP_EMBEDDING_CONFIGURATION`
just uses comma-separated `key=value` pairs instead of a YAML map.)

```yaml
neo4j_instances:
  - name: prod
    uri: neo4j+s://prod.databases.neo4j.io
    auth:
      type: basic
      username: mcp_service
      password: ${NEO4J_PROD_PASSWORD}
      api_keys:
        - ${MCP_PROD_API_KEY}
    embedding:
      provider: openai
      configuration:
        token: ${OPENAI_API_KEY}
        model: text-embedding-3-small
```

`provider` is one of `openai`, `azure-openai`, `vertexai`, `bedrock-titan`.
**Who actually generates the embedding differs by provider:**

- **`openai`** — generated by **this MCP server itself**, via a direct HTTP
  call to an OpenAI-compatible `/embeddings` endpoint. Neo4j is not
  involved at all: no GenAI plugin, no server-side config. `configuration`
  supports an optional `baseUrl` key (default `https://api.openai.com/v1`)
  that redirects this to any OpenAI-compatible **local** server instead —
  verified end-to-end against [LM Studio](https://lmstudio.ai/) serving
  `nomic-embed-text-v1.5`, and Ollama's OpenAI-compatible mode works the
  same way:

  ```yaml
  embedding:
    provider: openai
    configuration:
      token: unused # still required, but most local servers ignore its value
      model: text-embedding-nomic-embed-text-v1.5@q8_0
      baseUrl: http://localhost:1234/v1 # LM Studio's default port
  ```

  If this MCP server itself runs in a container and the local model server
  runs on the host, `localhost` won't resolve to the host from inside the
  container — use `host.containers.internal` (Podman) or
  `host.docker.internal` (Docker Desktop) instead.
- **`azure-openai`, `vertexai`, `bedrock-titan`** — generated by **Neo4j's
  own `ai.text.embed` function**, reached from the Neo4j server itself
  (never from this MCP server). Reimplementing Azure's OAuth, GCP's
  service-account tokens, and AWS's SigV4 signing client-side wasn't worth
  it just for embedding generation, so these three keep using what Neo4j's
  GenAI plugin already implements. `configuration` is passed straight
  through as that function's configuration map, so `baseUrl` has no effect
  here — the plugin's own `genai.azure.openai.baseurl` server setting is
  the equivalent for Azure, but there's no per-call override.

Required `configuration` keys per provider:

| Provider        | Required `configuration` keys                                | Generated by          |
| --------------- | ------------------------------------------------------------ | --------------------- |
| `openai`        | `token`, `model` (`baseUrl` optional)                        | This MCP server       |
| `azure-openai`  | `token`, `resource`, `model`                                 | Neo4j `ai.text.embed` |
| `vertexai`      | `model`, `project`, `region`, and one of `apiKey` or `token` | Neo4j `ai.text.embed` |
| `bedrock-titan` | `model`, `region`, `accessKeyId`, `secretAccessKey`          | Neo4j `ai.text.embed` |

Like every other secret-bearing field in this file, `configuration` values
support `${VAR}` interpolation. An instance with no `embedding` block can
still store vectors via `set-vector-property`'s `vector` field — it just
can't use that tool's `text` field.

## Known limitations

- **Query API instances aren't supported yet.** Every instance's `uri` must
  be a Bolt-scheme URI (`bolt://`, `neo4j://`, and their `+s`/`+ssc`
  variants) — an `http`/`https` (Query API) instance URI is rejected at
  startup with a clear error, rather than silently misbehaving.
- **GDS/search-tool availability is checked against one instance only.**
  Which optional tools (GDS procedures, the `search` category) are
  registered is decided once, from whichever instance happens to receive
  the server's first request — not per instance. If your instances have
  different Neo4j versions or plugin installs, a tool might appear
  registered but fail against a specific instance. Keep instance capability
  homogeneous across a single server for now.
