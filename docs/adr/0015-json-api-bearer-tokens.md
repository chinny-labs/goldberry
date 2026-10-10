# 0015. JSON API at /api/v1 with household bearer tokens

- **Status:** Accepted
- **Date:** 2026-10-10
- **Source:** [design plan](../design/plan.md) §11.1 ("JSON API: seam only")

## Context

Bulk work (backfilling entries, setting up several kids) and automation (scripts, Home Assistant, AI harnesses) are clumsy through HTMX forms. The `service` layer already owns every money rule, so an API is a new `web`-layer sibling, not a refactor.

## Decision

- **Surface.** `/api/v1`, JSON in and out, a hand-written OpenAPI 3.1 spec in `docs/api/openapi.yaml`, also served at `/api/v1/openapi.json`. Handlers call `service` only, never the store.
- **v1 scope.** Kids, jars, ledger, goals, deposits and payouts, reversals, and bulk entries. Investing, schedules and notifications are later.
- **Auth.** Bearer tokens (`Authorization: Bearer gb_...`), not session cookies, so no CSRF surface. Tokens are random 256-bit, stored as SHA-256 hashes (like sessions), shown once, revocable, with a label and last-used time.
- **Ownership.** A token belongs to the admin (parent) account that minted it and acts as that user, so ledger entries and the audit log attribute to them (the audit log also records the token id). Each admin manages their own tokens; any admin can revoke another's from the user page, next to "revoke sessions". Disabling an admin disables their tokens. Tokens never outrank their owner: a token can do only what that admin can.
- **Scopes.** `read`, `write` (jars, goals; no money movement) and `money` (post, remove and reverse entries). Scopes are cumulative only by explicit grant: `money` does not imply `write`. The UI defaults new tokens to `read`.
- **Bulk.** `POST /api/v1/entries:batch` is all-or-nothing in one transaction. Each item carries an idempotency key; retrying a batch posts nothing twice. `?dry_run=true` validates and returns the would-be result without writing.
- **Errors.** Reuse `service` errors: `UserError` becomes 422 with the field, `ErrForbidden` 403, `ErrNotFound` 404. No stack text.
- **Throttling.** Per-token request limit, and failed-token attempts are throttled by source address.

## Consequences

Scripts and agents get a stable contract. The money rules stay in one place. A leaked read token exposes balances, not the ability to move money. Every API action traces to a named parent, at the cost of tokens dying with that account.
