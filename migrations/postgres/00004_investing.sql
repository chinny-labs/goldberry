-- Investing: an Invest jar, a simulated brokerage that mirrors real prices,
-- and parent-paid earnings (plan §7.6, ADR 0014).
-- Kept in lockstep with migrations/sqlite/00004_investing.sql.

-- +goose Up
ALTER TABLE jars DROP CONSTRAINT jars_kind_check;
ALTER TABLE jars ADD CONSTRAINT jars_kind_check CHECK (kind IN ('spend', 'save', 'give', 'invest', 'custom'));
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN ('deposit', 'withdrawal', 'adjustment',
    'allowance', 'interest', 'transfer_in', 'transfer_out', 'reversal', 'invest_buy', 'invest_sell', 'invest_gain'));

-- Names and kinds of symbols kids have looked at or bought.
CREATE TABLE securities (
    symbol     TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('stock', 'fund')),
    updated_at TIMESTAMPTZ NOT NULL
);

-- Last price seen per symbol. Prices are micro-dollars (1/1,000,000 USD).
CREATE TABLE quotes (
    symbol     TEXT PRIMARY KEY,
    price      BIGINT NOT NULL,
    open       BIGINT NOT NULL,
    prev_close BIGINT NOT NULL,
    quoted_at  TIMESTAMPTZ NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL
);

-- Buy and sell orders. A buy debits the Invest jar when it's placed; a queued
-- one that's cancelled is reversed. Shares are micro-shares.
CREATE TABLE orders (
    id              TEXT PRIMARY KEY,
    kid_id          TEXT NOT NULL REFERENCES users (id),
    jar_id          TEXT NOT NULL REFERENCES jars (id),
    symbol          TEXT NOT NULL REFERENCES securities (symbol),
    side            TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    amount          BIGINT NOT NULL DEFAULT 0 CHECK (amount >= 0),
    shares          BIGINT NOT NULL DEFAULT 0 CHECK (shares >= 0),
    status          TEXT NOT NULL CHECK (status IN ('queued', 'filled', 'cancelled')),
    price           BIGINT,
    proceeds        BIGINT,
    cost_basis      BIGINT,
    ledger_entry_id TEXT REFERENCES ledger_entries (id),
    placed_by       TEXT REFERENCES users (id),
    idempotency_key TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL,
    filled_at       TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ
);
CREATE INDEX orders_kid ON orders (kid_id, created_at);
CREATE INDEX orders_queued ON orders (status) WHERE status = 'queued';

-- Profit from a sale, owed by the parents until one of them pays it.
CREATE TABLE payouts (
    id              TEXT PRIMARY KEY,
    kid_id          TEXT NOT NULL REFERENCES users (id),
    jar_id          TEXT NOT NULL REFERENCES jars (id),
    order_id        TEXT NOT NULL UNIQUE REFERENCES orders (id),
    amount          BIGINT NOT NULL CHECK (amount > 0),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'paid')),
    paid_by         TEXT REFERENCES users (id),
    paid_at         TIMESTAMPTZ,
    ledger_entry_id TEXT REFERENCES ledger_entries (id),
    created_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX payouts_kid ON payouts (kid_id, created_at);

-- A kid's investments at the end of each household day, for the graph.
CREATE TABLE portfolio_snapshots (
    kid_id TEXT NOT NULL REFERENCES users (id),
    day    TEXT NOT NULL,
    value  BIGINT NOT NULL,
    cost   BIGINT NOT NULL,
    PRIMARY KEY (kid_id, day)
);

-- +goose Down
DROP TABLE portfolio_snapshots;
DROP TABLE payouts;
DROP TABLE orders;
DROP TABLE quotes;
DROP TABLE securities;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN ('deposit', 'withdrawal', 'adjustment',
    'allowance', 'interest', 'transfer_in', 'transfer_out', 'reversal'));
ALTER TABLE jars DROP CONSTRAINT jars_kind_check;
ALTER TABLE jars ADD CONSTRAINT jars_kind_check CHECK (kind IN ('spend', 'save', 'give', 'custom'));
