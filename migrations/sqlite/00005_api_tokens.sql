-- API tokens for /api/v1 and /mcp (ADR 0015, ADR 0016).
-- Kept in lockstep with migrations/postgres/00005_api_tokens.sql.
-- A token belongs to the admin who minted it; only its SHA-256 is stored.

-- +goose Up
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id),
    label        TEXT NOT NULL,
    scopes       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX api_tokens_user ON api_tokens (user_id);

-- +goose Down
DROP TABLE api_tokens;
