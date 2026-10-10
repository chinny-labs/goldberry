-- Investing: an Invest jar, a simulated brokerage that mirrors real prices,
-- and parent-paid earnings (plan §7.6, ADR 0014).
-- Kept in lockstep with migrations/postgres/00004_investing.sql.
--
-- SQLite can't change a CHECK constraint in place, so jars and ledger_entries
-- are rebuilt with the new kinds, as the SQLite docs describe: foreign keys
-- off, copy into a new table, drop, rename, check. Migrations run on the
-- single-connection write pool, so the pragma and the transaction below share
-- one connection.

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TABLE jars_new (
    id          TEXT PRIMARY KEY,
    kid_id      TEXT NOT NULL REFERENCES users (id),
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('spend', 'save', 'give', 'invest', 'custom')),
    sort_order  INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT
);
INSERT INTO jars_new (id, kid_id, name, kind, sort_order, archived_at)
    SELECT id, kid_id, name, kind, sort_order, archived_at FROM jars;
DROP TABLE jars;
ALTER TABLE jars_new RENAME TO jars;
CREATE INDEX jars_kid ON jars (kid_id);

-- APPEND-ONLY: the application never UPDATEs or DELETEs ledger rows. This
-- copy keeps every row and column as it was.
CREATE TABLE ledger_entries_new (
    id              TEXT PRIMARY KEY,
    kid_id          TEXT NOT NULL REFERENCES users (id),
    jar_id          TEXT NOT NULL REFERENCES jars (id),
    amount          INTEGER NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('deposit', 'withdrawal', 'adjustment', 'allowance',
                                                  'interest', 'transfer_in', 'transfer_out', 'reversal',
                                                  'invest_buy', 'invest_sell', 'invest_gain')),
    comment         TEXT,
    private_note    TEXT,
    actor_id        TEXT REFERENCES users (id),
    reverses_id     TEXT UNIQUE REFERENCES ledger_entries (id),
    transfer_id     TEXT,
    batch_id        TEXT,
    request_id      TEXT,
    schedule_id     TEXT,
    idempotency_key TEXT UNIQUE,
    effective_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
INSERT INTO ledger_entries_new SELECT id, kid_id, jar_id, amount, kind, comment, private_note, actor_id,
    reverses_id, transfer_id, batch_id, request_id, schedule_id, idempotency_key, effective_at, created_at
    FROM ledger_entries;
DROP TABLE ledger_entries;
ALTER TABLE ledger_entries_new RENAME TO ledger_entries;
CREATE INDEX ledger_jar_effective ON ledger_entries (jar_id, effective_at);
CREATE INDEX ledger_kid_created ON ledger_entries (kid_id, created_at);

-- Names and kinds of symbols kids have looked at or bought.
CREATE TABLE securities (
    symbol     TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('stock', 'fund')),
    updated_at TEXT NOT NULL
);

-- Last price seen per symbol. Prices are micro-dollars (1/1,000,000 USD).
CREATE TABLE quotes (
    symbol     TEXT PRIMARY KEY,
    price      INTEGER NOT NULL,
    open       INTEGER NOT NULL,
    prev_close INTEGER NOT NULL,
    quoted_at  TEXT NOT NULL,
    fetched_at TEXT NOT NULL
);

-- Buy and sell orders. A buy debits the Invest jar when it's placed; a queued
-- one that's cancelled is reversed. Shares are micro-shares.
CREATE TABLE orders (
    id              TEXT PRIMARY KEY,
    kid_id          TEXT NOT NULL REFERENCES users (id),
    jar_id          TEXT NOT NULL REFERENCES jars (id),
    symbol          TEXT NOT NULL REFERENCES securities (symbol),
    side            TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    amount          INTEGER NOT NULL DEFAULT 0 CHECK (amount >= 0),
    shares          INTEGER NOT NULL DEFAULT 0 CHECK (shares >= 0),
    status          TEXT NOT NULL CHECK (status IN ('queued', 'filled', 'cancelled')),
    price           INTEGER,
    proceeds        INTEGER,
    cost_basis      INTEGER,
    ledger_entry_id TEXT REFERENCES ledger_entries (id),
    placed_by       TEXT REFERENCES users (id),
    idempotency_key TEXT UNIQUE,
    created_at      TEXT NOT NULL,
    filled_at       TEXT,
    cancelled_at    TEXT
);
CREATE INDEX orders_kid ON orders (kid_id, created_at);
CREATE INDEX orders_queued ON orders (status) WHERE status = 'queued';

-- Profit from a sale, owed by the parents until one of them pays it.
CREATE TABLE payouts (
    id              TEXT PRIMARY KEY,
    kid_id          TEXT NOT NULL REFERENCES users (id),
    jar_id          TEXT NOT NULL REFERENCES jars (id),
    order_id        TEXT NOT NULL UNIQUE REFERENCES orders (id),
    amount          INTEGER NOT NULL CHECK (amount > 0),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'paid')),
    paid_by         TEXT REFERENCES users (id),
    paid_at         TEXT,
    ledger_entry_id TEXT REFERENCES ledger_entries (id),
    created_at      TEXT NOT NULL
);
CREATE INDEX payouts_kid ON payouts (kid_id, created_at);

-- A kid's investments at the end of each household day, for the graph.
CREATE TABLE portfolio_snapshots (
    kid_id TEXT NOT NULL REFERENCES users (id),
    day    TEXT NOT NULL,
    value  INTEGER NOT NULL,
    cost   INTEGER NOT NULL,
    PRIMARY KEY (kid_id, day)
);

COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- The wider CHECKs on jars and ledger_entries stay: every older row passes them.
PRAGMA foreign_keys = OFF;
BEGIN;
DROP TABLE portfolio_snapshots;
DROP TABLE payouts;
DROP TABLE orders;
DROP TABLE quotes;
DROP TABLE securities;
COMMIT;
PRAGMA foreign_keys = ON;
