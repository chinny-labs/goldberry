# MCP server (for AI tools)

Goldberry can speak the [Model Context Protocol](https://modelcontextprotocol.io), so tools like Claude can look at balances and, if you allow it, post money. It is **off by default**. See [ADR 0016](../adr/0016-mcp-server-on-the-api.md).

## Turn it on

1. Set `GOLDBERRY_MCP_ENABLED=true` (Compose: `environment`; Helm: `--set mcp.enabled=true`) and restart.
2. Make an [API token](api-tokens.md). Give an AI tool `read` first.
3. Point the tool at `https://your-goldberry/mcp` (streamable HTTP) with the header `Authorization: Bearer gb_…`.

For Claude Code: `claude mcp add --transport http goldberry https://goldberry.example.com/mcp --header "Authorization: Bearer gb_…"`.

The server is stateless and every call is checked against the token, so revoking a token cuts a tool off at once. It shares the [API's limits](api.md).

## What tools a token sees

Tools above a token's scopes are not listed, and can't be called.

| Scope | Tools |
| --- | --- |
| `read` | `get_household`, `list_kids`, `get_kid`, `get_ledger`, `list_goals`, `list_requests` |
| `write` | `add_jar`, `rename_jar`, `set_split`, `create_goal`, `archive_goal` |
| `money` | `post_entry`, `post_entries`, `reverse_entry`, `decide_request` |

Kids are named by username (`ava`). Amounts are minor units (cents for USD).

## Money tools look before they leap

- **They default to a dry run.** `post_entry`, `post_entries`, `reverse_entry` and `decide_request` show what would happen and change nothing until the tool is called again with `dry_run: false`. A good prompt is "show me what you'd post, and wait for my OK."
- **Postings need an `idempotency_key`.** Reusing a key retries safely: nothing posts twice. The tool descriptions tell the model to pick stable keys that name the payment.
- **Batches are all-or-nothing** (up to 100 entries); an error names the item to fix.
- **Everything is audited** with the token's name, on the parent's **Account history**.

Moving money between jars isn't offered over MCP; use the [API](api.md) for that.

## Safety notes

- Treat a `money` token like the parent's PIN: anyone holding it can post as that parent. Revoke it when a tool is retired.
- MCP exposes the same data as the API, including parents' private notes on entries.
- Put Goldberry behind TLS, as for the rest of the app.
