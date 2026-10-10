# Troubleshooting

**Every page redirects to /setup.** No parent exists yet. Get the setup token from the logs (`docker compose logs goldberry | grep -i "setup token"`).

**Signing in loops back to the login page.** The browser dropped the `Secure` session cookie because the site is plain HTTP. Put Goldberry behind HTTPS, or for a quick trial set `GOLDBERRY_INSECURE_COOKIES=true`.

**"That form expired."** The CSRF token didn't match: usually a page left open across a restart or sign-out. Reload and try again.

**The API says `401 unauthorized`.** The token is missing, mistyped, or revoked, or the parent who owns it was disabled (disabling a parent revokes their tokens for good). Make a new one under People → your name → API tokens, and send it as `Authorization: Bearer gb_…`.

**The API says `429 rate_limited`.** Either a token went over about 10 requests a second, or the *address* sent 10 bad tokens in a minute and is blocked for the rest of that minute, which also turns away good tokens from the same address. Wait for `Retry-After`, and fix any script holding a stale token. Behind a proxy, set `GOLDBERRY_TRUSTED_PROXIES` so the address is the caller's.

**`/mcp` returns 404.** The MCP server is off by default. Set `GOLDBERRY_MCP_ENABLED=true` (Helm: `mcp.enabled: true`) and restart. Once on, it answers 401 without a valid token, like the API.

**An AI tool can't see the money tools.** Tools above a token's scopes are hidden. Give the token the `money` scope (or `write` for jars and goals), or make a new one.

**A kid is locked out.** Ten wrong PINs hard-lock a kid account. A parent unlocks it (People → the kid → Unlock) or resets the PIN; nothing unlocks on a timer.

**Every parent is locked out / forgot the password.** Run `goldberry admin reset-password <username>` in the container; it prints a new password.

**"permission denied" creating /data/goldberry.db.** The container runs as UID 65532. With a host folder (`./data:/data`), run `sudo chown 65532:65532 data` first, or use the named volume from the Compose file. In Kubernetes the chart sets `fsGroup: 65532`.

**Startup warns about a network filesystem.** `/data` is on NFS/SMB/FUSE. Move it to a local or block-backed volume before you store real data: SQLite's WAL can corrupt on network filesystems.

**The allowance didn't post.** It posts on the first scheduler tick after midnight of the paying day in the household time zone (Settings). Check the allowance isn't paused, and that the household time zone is right. After downtime it catches up to 8 missed days automatically.

**Interest is lower than expected.** It's on the *average daily* balance for the month, so money added late in the month earns little. Each interest entry's comment shows the average, the rate and the number of days.

**Health checks.** `/healthz` (process up), `/readyz` (database reachable), `/metrics` (Prometheus). `goldberry healthcheck` exits 0 when the local server answers, for Docker's `HEALTHCHECK`.

**Logs.** JSON by default (`GOLDBERRY_LOG_FORMAT=text` for humans). Set `GOLDBERRY_DEBUG=1` for per-request logs of static files and health checks.
