# Upgrade

Goldberry follows semantic versioning. Image tags: `1.2.3`, `1.2`, `1`, `latest`. Pin to a major (`:1`) to get fixes and features without breaking changes.

1. **Back up first.** SQLite: `goldberry backup` (or copy last night's file from `/data/backups`). Postgres: `pg_dump`.
2. **Pull and restart.**
   - Compose: `docker compose pull && docker compose up -d`
   - Helm: `helm upgrade goldberry oci://ghcr.io/chinny-labs/charts/goldberry --version <new> --reuse-values`
3. Migrations run on startup (under an advisory lock on Postgres) and are logged. `goldberry migrate` runs them on their own if you prefer an explicit step.

Downgrades aren't supported once a newer version has migrated the database: restore the backup you took in step 1 instead.

Release notes and the changelog are on the [GitHub releases page](https://github.com/chinny-labs/goldberry/releases). Images are signed with cosign (keyless); verify with:

```sh
cosign verify ghcr.io/chinny-labs/goldberry:1.4.1 \
  --certificate-identity-regexp 'https://github.com/chinny-labs/goldberry/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

### Moving from `ghcr.io/chinny/…`

The project moved from the `chinny` account to the `chinny-labs` org after 1.4.0. Releases up to 1.4.0 stay at `ghcr.io/chinny/goldberry` and `oci://ghcr.io/chinny/charts/goldberry`, signed as `https://github.com/chinny/goldberry/`. From 1.4.1 on, images and the chart are published under `ghcr.io/chinny-labs/…`. To upgrade, change the image repository (compose) or the chart URL (Helm) and keep your data volume. Nothing else changes.

