# goldberry

A self-hosted allowance tracker for kids. Parents add and remove money with a comment; kids sign in with a PIN, see what they can spend, and ask for money. Requests show up in the parents' notification bell to approve or deny.

One container, one volume: a Go binary serving server-rendered HTML (HTMX), with SQLite on the volume by default or Postgres if you point it at one. *Named for Goldberry, the River-daughter in The Lord of the Rings.*

**[Website and docs →](https://chinny.github.io/goldberry/)**

![The parent dashboard: two requests waiting for approval and a card per kid with their Spend, Save, Give and Invest jars](docs/screenshots/parent-dashboard.png)

| A kid's home | Breaking your own lock | Investing |
| --- | --- | --- |
| <img src="docs/screenshots/kid-home.png" width="260" alt="A kid's home: the Spend jar, Save and Give with lock badges, the Invest jar, money waiting for approval"> | <img src="docs/screenshots/gauntlet.png" width="260" alt="The red lock-breaking screen with the kid's own reason, the goal it would empty and a countdown"> | <img src="docs/screenshots/invest.png" width="260" alt="The Invest page: money ready to invest, what's invested and whether it's up, and the market status"> |

| One page per kid | Money over time |
| --- | --- |
| <img src="docs/screenshots/parent-kid.png" alt="A parent's view of one kid: four jars with locks and interest, a pending request, the ledger, last month's sparkline and investing"> | <img src="docs/screenshots/parent-graph.png" alt="A kid's money over three months, one line per jar"> |

## Quickstart (Docker Compose, 60 seconds)

```sh
mkdir goldberry && cd goldberry
curl -fsSLO https://raw.githubusercontent.com/chinny/goldberry/main/deploy/compose/compose.yaml
openssl rand -base64 32 > goldberry_key.txt
docker compose up -d
docker compose logs goldberry | grep -i "setup token"
```

Open `https://<host>/setup`, paste the setup token, and create the first parent account. Then add your kids.

> Sessions use `Secure` cookies, so put Goldberry behind a TLS reverse proxy (Caddy, Traefik, a Gateway). For a quick plain-HTTP trial on your LAN, set `GOLDBERRY_INSECURE_COOKIES: "true"` in `compose.yaml`.

On Kubernetes: `helm install goldberry oci://ghcr.io/chinny/charts/goldberry`. See [docs/operations](docs/operations/install.md) for install, [upgrade](docs/operations/upgrade.md), [backup and restore](docs/operations/backup-restore.md), [JSON API](docs/operations/api.md), [MCP server](docs/operations/mcp.md), [API tokens](docs/operations/api-tokens.md) and [troubleshooting](docs/operations/troubleshooting.md).

## What it does

- **Accounts:** parents sign in with a password, kids with a 4–6 digit PIN on a big PIN pad. Throttling is per account; kids hard-lock after 10 wrong PINs and only a parent can unlock them. First-run setup needs a one-time token from the log.
- **Money:** an append-only ledger in integer cents. Add or remove funds with a comment the kid sees and a private note they don't. Undo is a visible reversal, never an edit.
- **Jars:** Spend, Save and Give, split 70/20/10 (leftover cents to Spend). Parents rename, add and archive jars and change the split; kids move money between their own jars.
- **Jar locks:** a parent's lock is hard. A kid can lock their own jar ("Saving for a Switch") and break it only through the gauntlet: a loud warning, a 10-second countdown and a 3-second press-and-hold, enforced by the server ([ADR 0011](docs/adr/0011-server-enforced-gauntlet.md)).
- **Requests:** a kid asks for money from what's available; it's held until a parent approves (optionally less, with a note), denies, the kid cancels, or it expires. The first parent to decide wins.
- **Recurring allowance:** weekly, every two weeks or monthly, on the household's calendar. Catches up after downtime, never double-pays, can be paused.
- **Goals:** "Nintendo Switch, $300" fills from the Save jar's money in priority order. Reaching one tells everyone and offers "Ask to buy it".
- **Parent-paid interest:** a monthly rate on the average daily balance (so a deposit on the 30th earns almost nothing), with an optional cap and a "leave it for 12 months" projection.
- **Balance graph:** a kid's money over the last week, month or three months, or any pair of dates, as the total, every jar, or one jar. Each change is a dot that leads to what happened ([ADR 0013](docs/adr/0013-server-drawn-svg-charts.md)).
- **Investing:** kids buy and sell real US stocks and funds at live prices with a fourth **Invest** jar that fills from the split. Orders placed after hours wait for the open. Losses come off by themselves; profits wait for a parent to pay them. Every investing word has a kid-friendly "?" tip ([operations](docs/operations/investing.md), [ADR 0014](docs/adr/0014-simulated-brokerage-real-prices.md)).
- **Notifications:** an in-app bell with Approve/Deny inline.
- **API and AI tools:** a JSON API at `/api/v1` for scripts, Home Assistant and bulk work (atomic batches, dry runs, idempotency keys, backdated entries for loading balances tracked elsewhere), plus an opt-in MCP server at `/mcp` so tools like Claude can check balances and, if you allow it, post money. Parents mint scoped tokens (`read`, `write`, `money`) on the People page ([tokens](docs/operations/api-tokens.md), [API](docs/operations/api.md), [MCP](docs/operations/mcp.md), [ADR 0015](docs/adr/0015-json-api-bearer-tokens.md), [ADR 0016](docs/adr/0016-mcp-server-on-the-api.md)).
- **Operations:** nightly SQLite snapshots, a portable export/import between SQLite and Postgres, a Helm chart, `/healthz`, `/readyz`, `/metrics`, signed multi-arch images.
- Light and dark themes, phone-first, installable as an app (PWA), works without JavaScript, no CDN (works offline on a LAN).

Email notifications (Phase 3 of the [design plan](docs/design/plan.md#14-phasing)) are next.

## Configuration

Environment variables (or `/data/config.env`). Any variable can also be given as `NAME_FILE` pointing at a file.

| Variable | Default | Notes |
| --- | --- | --- |
| `GOLDBERRY_DATABASE_URL` | `sqlite:///data/goldberry.db` | Or `postgres://user:pass@host:5432/goldberry` |
| `GOLDBERRY_SECRET_KEY` | *(none)* | 32+ random bytes, base64. Will encrypt SMTP secrets (Phase 3). |
| `GOLDBERRY_BASE_URL` | *(none)* | Public URL, for email links |
| `GOLDBERRY_LISTEN` | `:8080` | |
| `GOLDBERRY_TRUSTED_PROXIES` | *(none)* | CIDRs whose `X-Forwarded-For` is trusted for logging |
| `GOLDBERRY_INSECURE_COOKIES` | `false` | Plain-HTTP testing only |
| `GOLDBERRY_MCP_ENABLED` | `false` | Serve the [MCP server](docs/operations/mcp.md) at `/mcp` for AI tools |
| `GOLDBERRY_BACKUP_SCHEDULE` | `03:15` | Time of the nightly SQLite snapshot (in `TZ`) |
| `GOLDBERRY_BACKUP_RETAIN` | `14` | Snapshots to keep in `/data/backups` |
| `GOLDBERRY_LOG_FORMAT` | `json` | `json` or `text` |
| `GOLDBERRY_FINNHUB_API_KEY` | *(none)* | Free key from finnhub.io; turns on [investing](docs/operations/investing.md) |
| `TZ` | `UTC` | Default household time zone at setup |

**Never put the SQLite file on NFS or SMB**: use a local or block-backed volume. Goldberry warns at startup if `/data` is a network filesystem.

### Operations

- Health: `GET /healthz` (liveness), `GET /readyz` (database), `GET /metrics` (Prometheus).
- Locked out as the only parent: `docker compose exec goldberry goldberry admin reset-password <username>` prints a new password.
- `goldberry backup`, `goldberry export -o dump.jsonl`, `goldberry import dump.jsonl`: see [backup and restore](docs/operations/backup-restore.md).
- API: `GET /api/v1/openapi.json` is the spec (no token needed). Tokens are credentials: revoke one on the People page, and note `goldberry export` leaves them out.
- `goldberry migrate` applies migrations without starting the server (they also run at startup).

## Development

Needs Go (see `go.mod`) and [`just`](https://github.com/casey/just).

```sh
just run        # http://localhost:8080, SQLite in ./data
just test       # unit tests + the store contract suite on SQLite
just pg-up && just test-pg   # the same contract suite on Postgres
just lint       # gofmt, vet, golangci-lint, migration parity
just helm       # lint and render the Helm chart
just e2e        # build the image and run the Playwright smoke test against it
just demo       # serve a made-up household with three months of history on :18081
just screenshots  # retake docs/screenshots from the demo household
just site       # build the website (site/ plus docs/) into site/_site
```

The screenshots come from `e2e/demo`, which seeds "The Riverbanks" through the real service layer on a moved clock, with made-up stock prices. Retake them with `just screenshots` when a screen changes. The website is `site/`: a landing page plus every page in `docs/operations`, `docs/adr` and the design plan, rendered from the Markdown and published to [GitHub Pages](https://chinny.github.io/goldberry/) by `.github/workflows/pages.yml` on every push to `main` that touches them. The first time, set **Settings → Pages → Source** to **GitHub Actions**.

Layout follows the plan (§13.2): `cmd/goldberry` (CLI), `internal/{config,money,allowance,auth,service,scheduler,notify,backup,store,web}`, `migrations/{sqlite,postgres}` (kept in lockstep), `deploy/{compose,helm/goldberry}`, `e2e/` (smoke test, demo household, screenshots), `site/` (the website), and `docs/` with the [design plan](docs/design/plan.md), the [design board](docs/design/README.md) and [ADRs](docs/adr/README.md).

The service layer owns every money rule; handlers never touch the store. `internal/store/storetest` is the contract suite both database engines must pass — it is the real definition of "pluggable".

Releases: merging the release-please PR tags `vX.Y.Z`, and the `release` workflow publishes signed multi-arch images (with SBOM and provenance) to `ghcr.io/chinny/goldberry` and the chart to `oci://ghcr.io/chinny/charts/goldberry` ([ADR 0012](docs/adr/0012-release-images-with-buildx.md)). Dependabot keeps Go modules, Actions and base images current. release-please needs **Settings → Actions → General → Allow GitHub Actions to create and approve pull requests** turned on; without it, run the `release` workflow by hand with the version.

## Licence

[AGPL-3.0](LICENSE). Every page links to the exact source of the running build. Fonts (Atkinson Hyperlegible Next, Bricolage Grotesque) are SIL OFL 1.1; HTMX is BSD-2-Clause.
