# 0015. JSON API at /api/v1 with household bearer tokens

- **Status:** Accepted
- **Date:** 2026-10-10
- **Source:** [design plan](../design/plan.md) §11.1 ("JSON API: seam only")

## Context

Bulk work (backfilling entries, setting up several kids) and automation (scripts, Home Assistant, AI harnesses) are clumsy through HTMX forms. The `service` layer already owns every money rule, so an API is a new `web`-layer sibling, not a refactor.

## Decision

- **Surface.** `/api/v1`, JSON in and out, a hand-maintained OpenAPI 3.1 spec in `internal/api/openapi.json` (embedded and served at `/api/v1/openapi.json`; JSON so there is no YAML dependency). A test fails when the spec and the routes differ. Handlers call `service` only, never the store.
- **v1 scope.** Kids, jars, ledger, goals, deposits and payouts, reversals, and bulk entries. Investing, schedules and notifications are later.
- **Auth.** Bearer tokens (`Authorization: Bearer gb_...`), not session cookies, so no CSRF surface. Tokens are random 256-bit, stored as SHA-256 hashes (like sessions), shown once, revocable, with a label and last-used time.
- **Ownership.** A token belongs to the admin (parent) account that minted it and acts as that user, so ledger entries and the audit log attribute to them (the audit log also records the token id). Each admin manages their own tokens; any admin can revoke another's from the user page, next to "revoke sessions". Disabling an admin disables their tokens. Tokens never outrank their owner: a token can do only what that admin can.
- **Scopes.** `read`, `write` (jars, goals; no money movement) and `money` (post, remove and reverse entries). Scopes are cumulative only by explicit grant: `money` does not imply `write`. Every token has `read`; the UI offers `write` and `money` as extra ticks. Disabling the owner revokes their tokens for good.
- **Money safety.** Every posting needs an idempotency key (the `Idempotency-Key` header or a body field; one per batch item), stored under an `api:` prefix so a client key can't collide with the ones Goldberry makes (`reverse:…`, `request:…`). A replay returns the original entries, flagged `replayed`; a key reused for a different amount, direction, jar or transfer is a 422, not a silent replay. `?dry_run=true` runs every check inside the transaction and rolls it back.
- **Bulk.** `POST /api/v1/entries:batch` takes up to 100 items and is all-or-nothing in one transaction, locking every kid involved in a fixed order (`Store.TxLocked`). Items apply in order. A failure names the item and posts nothing. Duplicate keys within a batch are refused.
- **Audit.** Postings, reversals, transfers and request decisions made through a token also write an audit row naming the token; the ledger entry itself names the owner.
- **Edits are PUT.** Goals are replaced, not patched, so the service's validation sees the whole object.
- **Errors.** Reuse `service` errors: `UserError` becomes 422 with the field, `ErrForbidden` 403, `ErrNotFound` 404, a refused overdraft 422 `insufficient_funds` with the available amount. Malformed JSON and unknown fields are 400. No stack text.
- **Throttling.** Per-token request limit, and failed-token attempts are throttled by source address.

## Consequences

Scripts and agents get a stable contract. The money rules stay in one place. A leaked read token exposes balances, not the ability to move money. Every API action traces to a named parent, at the cost of tokens dying with that account.
