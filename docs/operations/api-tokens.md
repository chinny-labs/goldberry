# API tokens

Tokens let scripts and AI tools use Goldberry as a parent. They are the credential for the JSON API and the MCP server that follow in later releases ([ADR 0015](../adr/0015-json-api-bearer-tokens.md), [ADR 0016](../adr/0016-mcp-server-on-the-api.md)).

## Make one

1. Sign in as a parent and open **People → your name**.
2. Under **API tokens**, give it a label (where it will be used) and tick what it may do.
3. **Copy the token when it appears.** It is shown once and can't be recovered; only a hash is kept. Make a new one if you lose it.

Tokens start with `gb_`. Send one as `Authorization: Bearer gb_…`.

## What a token can do

| Scope | Allows |
| --- | --- |
| `read` | Balances, history, goals. Every token has it. |
| `write` | Change jars and goals. Does not move money. |
| `money` | Add, remove and reverse ledger entries. Does not include `write`. |

Give an AI tool `read` first. Add `money` only for a tool you trust to post entries.

## Ownership and revoking

- A token belongs to the parent who made it and acts as that parent: ledger entries name them, and changes made through the API show in the account history of the person affected, as “Mom via <token label>”.
- You can see and revoke your own tokens. Any parent can revoke another parent's from their page.
- **Disabling a parent revokes their tokens for good**; enabling them again does not bring them back.
- Each parent can hold 20 live tokens.

## Backups

Tokens are credentials, so `goldberry export` leaves them out, as it does sessions. After a restore, make new tokens.
