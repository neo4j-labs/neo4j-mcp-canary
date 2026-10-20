---
name: neo4j-mcp-canary
description: Working policy for reading and changing a Neo4j graph through the neo4j-mcp-canary MCP tools. Use whenever a task involves querying, updating or deleting graph data through these tools (get-schema, find-nodes, match-pattern, traverse, read-cypher, write-cypher, update-node, delete-node, custom tools), especially anything that writes.
---

The tool descriptions say how each tool works. This is the policy for choosing between them and for staying safe.

- **See what you have.** The tool list varies: read-only mode removes write tools, older servers lack some categories, operators add custom tools. Use only tools you can see, and if one you need is missing, tell the user rather than substituting `write-cypher`.
- **Orient once.** Call `get-schema` at the start of a graph task and reuse it for labels, relationship types and directions.
- **Structured tools first.** Use `find-nodes`, `match-pattern`, `traverse` and the other parameter-only tools for what they cover; they bind values, cap results and refuse unbounded writes. Use `read-cypher` for aggregations and joins they can't express, with `$params`, a `LIMIT`, and named columns instead of whole nodes. `write-cypher` is a last resort.
- **Custom tools first of all.** If an operator's custom tool matches the question, use it over composing your own query. Its description is the contract.
- **Never use Cypher to get around a refusal.** If a structured write says the selection is too broad or nothing matched, narrow it (use an id) or ask the user. Don't rewrite it as `write-cypher`.

When you change data:

1. Find the exact targets first (`find-nodes`, `find-relationships`) and use their ids.
2. For anything selected by criteria, run with `dryRun: true`. Raise `maxAffected` only to the count the dry run reported, and only if the user asked for a bulk change.
3. Do everything the request implies and nothing more. Moving someone to a new company means deleting the old relationship and creating the new one. Confirm with the user before deleting more than they clearly asked for.
4. Read the counts in the response (`matched`, `updated`, `deleted`), not just `performed`, then re-read the data to verify before saying it worked.

Report in the user's terms (names, amounts), say how many things changed, and say so when a result was `truncated`.
