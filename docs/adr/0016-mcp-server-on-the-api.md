# 0016. MCP server layered on the service layer

- **Status:** Accepted
- **Date:** 2026-10-10
- **Source:** [ADR 0015](0015-json-api-bearer-tokens.md)

## Decision

- **Where.** Streamable HTTP at `/mcp` in the same binary and container, off unless `GOLDBERRY_MCP_ENABLED=true` (Helm: `mcp.enabled`).
- **Auth.** The same bearer tokens, limits and failed-token throttling as the API. Each request builds a server holding only the tools its token's scopes allow, so tools above a token's scope are neither listed nor callable. The handler is stateless: no session outlives a token.
- **Implementation.** The official Go SDK (`github.com/modelcontextprotocol/go-sdk`), in `internal/api` next to the REST handlers so both share the DTOs, the error mapping and the service calls. It never reimplements a money rule and never calls the store.
- **Tools.** Curated and task-shaped, not a mirror of every endpoint: six read tools, five write tools (`add_jar`, `rename_jar`, `set_split`, `create_goal`, `archive_goal`), four money tools (`post_entry`, `post_entries`, `reverse_entry`, `decide_request`). Transfers between jars stay REST-only for now.
- **Money safety.** Money tools default to `dry_run: true` and a model must pass `false` to act. Postings require idempotency keys. Failures return as tool errors with the API's stable codes (`insufficient_funds: item 2: …`).
- **Not now.** No stdio mode; a harness that can't speak HTTP can use the REST API.

## Consequences

Agents get typed discovery and safer defaults. MCP ships after the REST API, which is its contract. Disabled by default, so it adds no surface for households that don't want it.
