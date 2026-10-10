# 0016. MCP server layered on the service layer

- **Status:** Accepted
- **Date:** 2026-10-10
- **Source:** [ADR 0015](0015-json-api-bearer-tokens.md)

## Decision

- **Where.** Streamable HTTP at `/mcp` in the same binary and container, off unless `GOLDBERRY_MCP_ENABLED=true`.
- **Auth.** The same bearer tokens and scopes as the API. Tools above a token's scope are not listed.
- **Implementation.** A thin adapter over `service`, using the official Go MCP SDK. It never reimplements a money rule and never calls the store.
- **Tools.** Curated, task-shaped (`list_kids`, `get_balances`, `post_entry`, `post_entries`, `reverse_entry`), not a mechanical mirror of every endpoint. Money-moving tools default to `dry_run` and take idempotency keys.
- **Not now.** No stdio mode; a harness that can't speak HTTP can use the REST API.

## Consequences

Agents get typed discovery and safer defaults. MCP ships after the REST API, which is its contract. Disabled by default, so it adds no surface for households that don't want it.
