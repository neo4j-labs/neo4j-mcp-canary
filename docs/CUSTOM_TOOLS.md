# Custom Tools

Custom tools let you add your own tools to the server — a fixed Cypher
statement behind a name, a description and typed parameters. Use them for
operations the built-in tools can't express, such as the lookups and updates of
a CRM graph. Because the statement is written by you and values are bound as
Cypher parameters, the model never writes or edits Cypher.

Custom tools are configured only through the config file (see
[Configuration File](../README.md#configuration-file)), under the top-level
`custom_tools` key. They apply to every transport and, in multi-instance HTTP
mode, to every instance.

## Example

```yaml
custom_tools:
  - name: find-customer
    category: crm
    access: read
    description: >-
      Find a customer by email address and return their name, tier and open
      deal count. Use this when the user names a customer; use find-nodes for
      anything else.
    statement: |
      MATCH (c:Customer {email: $email})
      OPTIONAL MATCH (c)-[:HAS_DEAL]->(d:Deal {status: 'open'})
      RETURN c.name AS name, c.tier AS tier, count(d) AS openDeals
    parameters:
      - name: email
        type: string
        description: The customer's email address
        required: true

  - name: set-customer-tier
    category: crm
    access: readwrite
    description: >-
      Change a customer's tier. Idempotent: setting the same tier twice has no
      further effect.
    statement: |
      MATCH (c:Customer {email: $email})
      SET c.tier = $tier
      RETURN c.email AS email, c.tier AS tier
    parameters:
      - name: email
        type: string
        description: The customer's email address
        required: true
      - name: tier
        type: string
        description: The new tier
        required: true
        enum: [bronze, silver, gold]
```

Write `statement` as a YAML block scalar (`|`): an unquoted Cypher map such as
`{email: $email}` is not valid YAML on one line.

## Fields

| Field         | Required | Notes |
|---------------|----------|-------|
| `name`        | yes      | Lower-case kebab-case, at most 64 characters, unique, and not the name of a built-in tool. |
| `description` | yes      | Shown to the model. Say what it does, when to use it instead of a sibling tool, and what it returns. |
| `category`    | yes      | Your own label (`[a-z][a-z0-9_-]*`, at most 32 characters), usable in `NEO4J_MCP_ENABLED_TOOL_CATEGORIES` and the HTTP tool-category header. Must not be a built-in category (`cypher`, `gds`, `feedback`, `search`, `graph`, `traversal`, `patterns`). |
| `access`      | yes      | `read` or `readwrite`. |
| `statement`   | yes      | Cypher. Add a `CYPHER 25` prefix yourself if you need that version. |
| `parameters`  | no       | List of `name`, `type` (`string`, `integer`, `number`, `boolean`), `description` (required), `required`, `enum`, `default`. |

Every `$name` in the statement must be a declared parameter, and every declared
parameter must be used; a mismatch is a startup error. An invalid definition,
an unknown key, or a name or category that collides with a built-in stops the
server at startup with a message naming the tool.

## Access and safety

- `read` tools are classified before they run and executed in a read
  transaction. If the statement writes, the call is refused with an error and
  nothing is changed — set `access: readwrite` if that is intended.
- `readwrite` tools are advertised as non-read-only and destructive, and are
  removed entirely when `NEO4J_READ_ONLY=true`.
- Results use the same row, byte and timeout limits as the Cypher tools
  (`NEO4J_CYPHER_MAX_ROWS`, `NEO4J_CYPHER_MAX_BYTES`, `NEO4J_CYPHER_TIMEOUT_SECONDS`).
  Add a `LIMIT` to statements that can return many rows.

## Validating your configuration

```bash
neo4j-mcp-canary --config-file config.yaml --validate-config
```

`--validate-config` checks the whole configuration, not only custom tools, and
prints every problem it finds instead of stopping at the first. Each line is
`ERROR`, `WARN` or `INFO`; the exit status is 1 if there is any `ERROR`, so it
works as a CI or deploy gate.

- **Always (no database):** every invalid or missing setting, the custom tool
  definitions, name and category collisions, config file keys that match no
  setting (usually a typo, which would otherwise silently leave the default in
  place), and tool selection that names unknown tools or categories, selects
  write tools under `NEO4J_READ_ONLY`, or leaves no tools enabled.
- **With a database connection (STDIO mode):** connects to Neo4j, `EXPLAIN`s every
  custom tool statement with sample values of each parameter's type (nothing is
  executed or changed) and reports Neo4j syntax errors, a `read` tool that
  writes (error), a `readwrite` tool that only reads (warning), and selected
  tools the server can't offer (no GDS plugin, version too old). If it can't
  connect, that is an error.
- **Skipped (INFO):** in HTTP and multi-instance modes credentials arrive per
  request, so the database checks are skipped and the report says so.

Unknown config file keys are also logged as warnings every time the server
starts.

## Telemetry

Each call to a custom tool sends a `CUSTOM_TOOL` event with the tool's `name`
and `success` (true unless the call returned an error), and no arguments,
statement or results. It is sent instead of the regular tool-usage event, and
like all telemetry it is disabled by `NEO4J_TELEMETRY=false`.
