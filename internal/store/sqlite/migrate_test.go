package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/chinny/goldberry/migrations"
)

// TestInvestingMigrationKeepsData upgrades a populated v3 database through
// 00004, which rebuilds jars and ledger_entries to widen their CHECKs.
func TestInvestingMigrationKeepsData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "goldberry.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	fsys, _ := fs.Sub(migrations.SQLite, "sqlite")
	p, err := goose.NewProvider(database.DialectSQLite3, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	const ts = "2026-03-07T15:00:00.000000Z"
	for _, q := range []string{
		`INSERT INTO users (id, role, username, display_name, pin_hash, created_at) VALUES ('k', 'kid', 'ava', 'Ava', 'x', '` + ts + `')`,
		`INSERT INTO jars (id, kid_id, name, kind, sort_order) VALUES ('j1', 'k', 'Spend', 'spend', 0), ('j2', 'k', 'Save', 'save', 1)`,
		`INSERT INTO split_rules (kid_id, jar_id, basis_points) VALUES ('k', 'j1', 10000)`,
		`INSERT INTO ledger_entries (id, kid_id, jar_id, amount, kind, idempotency_key, effective_at, created_at)
			VALUES ('e1', 'k', 'j1', 500, 'deposit', 'key-1', '` + ts + `', '` + ts + `')`,
		`INSERT INTO ledger_entries (id, kid_id, jar_id, amount, kind, reverses_id, effective_at, created_at)
			VALUES ('e2', 'k', 'j1', -500, 'reversal', 'e1', '` + ts + `', '` + ts + `')`,
		`INSERT INTO jar_locks (id, jar_id, set_by_role, set_by, created_at) VALUES ('l1', 'j2', 'kid', 'k', '` + ts + `')`,
		`INSERT INTO lock_overrides (id, lock_id, kid_id, action, ledger_entry_id, created_at) VALUES ('o1', 'l1', 'k', 'once', 'e1', '` + ts + `')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	st, err := Open(ctx, path, nil) // runs 00004
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	jars, err := st.ListJars(ctx, "k")
	if err != nil || len(jars) != 2 || jars[1].Name != "Save" {
		t.Fatalf("jars %+v, %v", jars, err)
	}
	e, err := st.GetLedgerEntry(ctx, "e1")
	if err != nil || e.Amount != 500 || e.IdempotencyKey != "key-1" || e.ReversedByID != "e2" {
		t.Fatalf("entry %+v, %v", e, err)
	}

	w, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var fk int
	if err := w.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
	rows, err := w.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violations after the rebuild")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The new kinds are accepted; unknown ones still aren't.
	if _, err := w.ExecContext(ctx, `INSERT INTO jars (id, kid_id, name, kind) VALUES ('j3', 'k', 'Invest', 'invest')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ExecContext(ctx, `INSERT INTO jars (id, kid_id, name, kind) VALUES ('j4', 'k', 'X', 'bogus')`); err == nil {
		t.Fatal("bogus jar kind accepted")
	}
	// Children still point at the rebuilt tables.
	if _, err := w.ExecContext(ctx, `INSERT INTO split_rules (kid_id, jar_id, basis_points) VALUES ('k', 'nope', 0)`); err == nil {
		t.Fatal("split rule for a missing jar accepted")
	}
}
