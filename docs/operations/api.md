# JSON API

Goldberry has a JSON API at `/api/v1` for scripts, Home Assistant and AI tools. It calls the same service layer as the web app, so every rule (holds, locks, no negative balances) applies. See [ADR 0015](../adr/0015-json-api-bearer-tokens.md).

- **Spec:** `GET /api/v1/openapi.json` (no token needed). The source is [`internal/api/openapi.json`](../../internal/api/openapi.json); a test fails if it drifts from the routes.
- **Auth:** `Authorization: Bearer gb_…`. Make a token under **People → your name** ([API tokens](api-tokens.md)). `read` tokens can use the read endpoints; writes need `write` or `money` ([scopes](api-tokens.md#what-a-token-can-do)).
- **Money:** whole numbers in minor units (cents for USD). `GET /api/v1/household` says what the currency is.
- **Times:** RFC 3339, UTC. Date filters also take `YYYY-MM-DD`.
- **Kids:** wherever a path says `{kid}`, use the kid's ID or username (`ava`).
- **Errors:** `{"error": {"code": "...", "message": "...", "field": "..."}}`. `code` is stable: `unauthorized`, `insufficient_scope`, `forbidden`, `not_found`, `bad_request`, `invalid`, `insufficient_funds`, `already_decided`, `conflict`, `rate_limited`, `internal`.
- **Limits:** about 10 requests a second per token (bursts of 40). An address that sends 10 bad tokens in a minute is blocked for the rest of that minute. Both answer `429` with `Retry-After`.
- **The block is per address, not per token.** While it lasts it turns away *good* tokens from the same address too, so a script with a stale token can lock out your other tools on that host (or behind that NAT) for up to a minute. Revoke dead tokens and fix scripts that send them. Behind a proxy, set `GOLDBERRY_TRUSTED_PROXIES` so the address is the caller's, not the proxy's.

## Read

| Endpoint | Returns |
| --- | --- |
| `GET /me` | The token and the parent it acts as |
| `GET /household` | Name, currency, time zone |
| `GET /kids` | Kids with totals (`?include_disabled=true`) |
| `GET /kids/{kid}` | A kid with jars, balances, holds, locks, split |
| `GET /kids/{kid}/jars` | Just the jars |
| `GET /kids/{kid}/ledger` | Entries, newest first (`limit`, `jar_id`, `from`, `to`) |
| `GET /kids/{kid}/goals` | Active goals with progress |
| `GET /kids/{kid}/requests`, `GET /requests` | Withdrawal requests (`status`, `limit`) |

```sh
export GB=https://goldberry.example.com TOKEN=gb_…
curl -s -H "Authorization: Bearer $TOKEN" $GB/api/v1/kids/ava | jq '.jars[] | {name, available}'
```

## Write

Writes are PUT or POST with a JSON body. Unknown fields are rejected, so a typo can't silently do nothing.

| Endpoint | Scope | Does |
| --- | --- | --- |
| `POST /kids/{kid}/jars`, `PUT /jars/{id}`, `POST /jars/{id}/archive` | `write` | Add, rename, archive a jar |
| `PUT /kids/{kid}/split` | `write` | Set the split: `{"shares": {"<jar id>": 7000, …}}`, totalling 10000 |
| `POST /kids/{kid}/goals`, `PUT /goals/{id}`, `POST /goals/{id}/archive` | `write` | Add, replace (send every field), archive a goal |
| `POST /kids/{kid}/entries` | `money` | Add or remove funds |
| `POST /entries:batch` | `money` | Many entries, all or nothing |
| `POST /entries/{id}/reverse` | `money` | Undo an entry (a split deposit reverses together) |
| `POST /kids/{kid}/transfers` | `money` | Move money between a kid's jars |
| `POST /requests/{id}/decision` | `money` | Approve (optionally less) or deny a request |

Parents can already do all of this in the app, so a token can do no more than its owner. Jar locks that bind kids don't bind a parent's token.

### Posting money safely

- **Idempotency keys are required** on every posting (the `Idempotency-Key` header, or `idempotency_key` in the body; each item in a batch). Retrying with the same key posts nothing twice and returns the original entries with `"replayed": true` (HTTP 200 rather than 201). Pick a key that names the thing being paid, such as `allowance-2026-10-ava`, not a random one per attempt. A key reused for a *different* payment (another amount, direction, jar or transfer) is refused with `422` on `idempotency_key`, so a mix-up can't hide; only the comment may differ.
- **Dry run:** add `?dry_run=true` to an entry or a batch to run every check and get the entries that *would* post. Nothing is written and no key is used.
- **Batches** take up to 100 entries, each `{"kid": "ava", "amount": 500, "idempotency_key": "…"}` plus the same fields as a single entry. They apply in order in one transaction, so a later removal sees an earlier deposit. One failure rejects the whole batch, and the error names the item: `{"error": {"code": "insufficient_funds", "message": "Item 3: Only $4.00 is available in Spend.", "detail": {"item": 2, "available": 400}}}` (`item` is zero-based).
- Amounts are always positive; `"direction": "remove"` takes money out. Without `jar_id` the entry goes to the kid's first jar (Spend); `"use_split": true` splits a deposit by the kid's split.
- **Backdating:** add `effective_at` (RFC 3339, or `YYYY-MM-DD` for midnight in the household's time zone) to an entry, or to any item in a batch, to date it in the past. Use it to load money tracked elsewhere, such as an opening balance or earlier allowance. The ledger and history show the `effective_at` date; `created_at` is always the real time. It can't be in the future or before 2000-01-01, and a retry must repeat the same date or it's refused as a reused key. Interest is paid once per month for the month just ended, so backdating earns no retroactive interest for months already closed; the balance does count from the new date from then on.
- Every API posting, reversal, transfer and request decision writes an audit row. It shows under **Account history** on the *kid's* page (People → the kid) as `api.entry · Mom via “Home Assistant”`: the parent who owns the token, and the token's label. The ledger entry itself names only the parent.

```sh
# Pay two kids at once, safely retryable
curl -s -X POST $GB/api/v1/entries:batch -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "entries": [
    {"kid": "ava", "amount": 1000, "comment": "October chores", "idempotency_key": "chores-2026-10-ava"},
    {"kid": "leo", "amount": 800,  "comment": "October chores", "idempotency_key": "chores-2026-10-leo"}
  ]}' | jq

# Load an opening balance dated in the past
curl -s -X POST $GB/api/v1/kids/ava/entries -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "amount": 12500, "comment": "Opening balance", "effective_at": "2026-01-01", "idempotency_key": "opening-ava"}' | jq
```
