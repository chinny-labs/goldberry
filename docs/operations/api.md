# JSON API

Goldberry has a JSON API at `/api/v1` for scripts, Home Assistant and AI tools. It calls the same service layer as the web app, so every rule (holds, locks, no negative balances) applies. See [ADR 0015](../adr/0015-json-api-bearer-tokens.md).

- **Spec:** `GET /api/v1/openapi.json` (no token needed). The source is [`internal/api/openapi.json`](../../internal/api/openapi.json); a test fails if it drifts from the routes.
- **Auth:** `Authorization: Bearer gb_…`. Make a token under **People → your name** ([API tokens](api-tokens.md)). `read` tokens can use every endpoint on this page.
- **Money:** whole numbers in minor units (cents for USD). `GET /api/v1/household` says what the currency is.
- **Times:** RFC 3339, UTC. Date filters also take `YYYY-MM-DD`.
- **Kids:** wherever a path says `{kid}`, use the kid's ID or username (`ava`).
- **Errors:** `{"error": {"code": "...", "message": "...", "field": "..."}}`. `code` is stable: `unauthorized`, `insufficient_scope`, `forbidden`, `not_found`, `invalid`, `insufficient_funds`, `already_decided`, `conflict`, `rate_limited`, `internal`.
- **Limits:** about 10 requests a second per token (bursts of 40). An address that sends 10 bad tokens in a minute is blocked for the rest of that minute. Both answer `429` with `Retry-After`.

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
