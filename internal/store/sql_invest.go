package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- securities and quotes ------------------------------------------------------

func (x queries) GetSecurity(ctx context.Context, symbol string) (Security, error) {
	var s Security
	err := x.row(ctx, `SELECT symbol, name, kind, updated_at FROM securities WHERE symbol = ?`, symbol).
		Scan(&s.Symbol, &s.Name, &s.Kind, rtime{&s.UpdatedAt})
	return s, notFound(err)
}

func (x queries) PutSecurity(ctx context.Context, s Security) error {
	_, err := x.exec(ctx, `INSERT INTO securities (symbol, name, kind, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (symbol) DO UPDATE SET name = excluded.name, kind = excluded.kind, updated_at = excluded.updated_at`,
		s.Symbol, s.Name, s.Kind, x.t(s.UpdatedAt))
	return err
}

func (x queries) GetQuote(ctx context.Context, symbol string) (Quote, error) {
	var q Quote
	err := x.row(ctx, `SELECT symbol, price, open, prev_close, quoted_at, fetched_at FROM quotes WHERE symbol = ?`, symbol).
		Scan(&q.Symbol, &q.Price, &q.Open, &q.PrevClose, rtime{&q.QuotedAt}, rtime{&q.FetchedAt})
	return q, notFound(err)
}

func (x queries) PutQuote(ctx context.Context, q Quote) error {
	_, err := x.exec(ctx, `INSERT INTO quotes (symbol, price, open, prev_close, quoted_at, fetched_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (symbol) DO UPDATE SET price = excluded.price, open = excluded.open, prev_close = excluded.prev_close,
		quoted_at = excluded.quoted_at, fetched_at = excluded.fetched_at`,
		q.Symbol, q.Price, q.Open, q.PrevClose, x.t(q.QuotedAt), x.t(q.FetchedAt))
	return err
}

// --- orders ---------------------------------------------------------------------

const orderSelect = `SELECT o.id, o.kid_id, o.jar_id, o.symbol, o.side, o.amount, o.shares, o.status, o.price,
	o.proceeds, o.cost_basis, o.ledger_entry_id, o.placed_by, o.idempotency_key, o.created_at, o.filled_at,
	o.cancelled_at, s.name, s.kind
	FROM orders o JOIN securities s ON s.symbol = o.symbol`

func scanOrder(sc interface{ Scan(...any) error }) (Order, error) {
	var o Order
	err := sc.Scan(&o.ID, &o.KidID, &o.JarID, &o.Symbol, &o.Side, &o.Amount, &o.Shares, &o.Status, nint{&o.Price},
		nint{&o.Proceeds}, nint{&o.CostBasis}, nstr{&o.LedgerEntryID}, nstr{&o.PlacedBy}, nstr{&o.IdempotencyKey},
		rtime{&o.CreatedAt}, ntime{&o.FilledAt}, ntime{&o.CancelledAt}, &o.Name, &o.SecurityKind)
	return o, err
}

func (x queries) GetOrder(ctx context.Context, id string) (Order, error) {
	o, err := scanOrder(x.row(ctx, orderSelect+` WHERE o.id = ?`, id))
	return o, notFound(err)
}

func (x queries) GetOrderByKey(ctx context.Context, key string) (Order, error) {
	o, err := scanOrder(x.row(ctx, orderSelect+` WHERE o.idempotency_key = ?`, key))
	return o, notFound(err)
}

func (x queries) ListOrders(ctx context.Context, f OrderFilter) ([]Order, error) {
	var where []string
	var args []any
	if f.KidID != "" {
		where, args = append(where, `o.kid_id = ?`), append(args, f.KidID)
	}
	if f.Status != "" {
		where, args = append(where, `o.status = ?`), append(args, f.Status)
	}
	q := orderSelect
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if f.Status == OrderQueued { // first in, first filled
		q += ` ORDER BY o.created_at, o.id`
	} else {
		q += ` ORDER BY o.created_at DESC, o.id DESC`
	}
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := x.rows(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func nzero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (x queries) InsertOrder(ctx context.Context, o Order) (bool, error) {
	res, err := x.exec(ctx, `INSERT INTO orders (id, kid_id, jar_id, symbol, side, amount, shares, status, price,
		proceeds, cost_basis, ledger_entry_id, placed_by, idempotency_key, created_at, filled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		o.ID, o.KidID, o.JarID, o.Symbol, o.Side, o.Amount, o.Shares, o.Status, nzero(o.Price),
		fillNum(o, o.Proceeds), fillNum(o, o.CostBasis), nul(o.LedgerEntryID), nul(o.PlacedBy), nul(o.IdempotencyKey),
		x.t(o.CreatedAt), x.tp(o.FilledAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// fillNum stores a sale's proceeds and basis (which may be 0) once filled,
// and NULL for anything else.
func fillNum(o Order, v int64) any {
	if o.Side == SideSell && o.Status == OrderFilled {
		return v
	}
	return nil
}

func (x queries) FillOrder(ctx context.Context, o Order) (bool, error) {
	res, err := x.exec(ctx, `UPDATE orders SET status = 'filled', shares = ?, price = ?, proceeds = ?, cost_basis = ?,
		ledger_entry_id = COALESCE(?, ledger_entry_id), filled_at = ? WHERE id = ? AND status = 'queued'`,
		o.Shares, o.Price, fillNum(Order{Side: o.Side, Status: OrderFilled}, o.Proceeds),
		fillNum(Order{Side: o.Side, Status: OrderFilled}, o.CostBasis), nul(o.LedgerEntryID), x.tp(o.FilledAt), o.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (x queries) CancelOrder(ctx context.Context, id string, at time.Time) (bool, error) {
	res, err := x.exec(ctx, `UPDATE orders SET status = 'cancelled', cancelled_at = ? WHERE id = ? AND status = 'queued'`,
		x.t(at), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// --- payouts --------------------------------------------------------------------

const payoutSelect = `SELECT p.id, p.kid_id, p.jar_id, p.order_id, p.amount, p.status, p.paid_by, p.paid_at,
	p.ledger_entry_id, p.created_at, k.display_name, o.symbol, b.display_name
	FROM payouts p
	JOIN users k ON k.id = p.kid_id
	JOIN orders o ON o.id = p.order_id
	LEFT JOIN users b ON b.id = p.paid_by`

func scanPayout(sc interface{ Scan(...any) error }) (Payout, error) {
	var p Payout
	err := sc.Scan(&p.ID, &p.KidID, &p.JarID, &p.OrderID, &p.Amount, &p.Status, nstr{&p.PaidBy}, ntime{&p.PaidAt},
		nstr{&p.LedgerEntryID}, rtime{&p.CreatedAt}, &p.KidName, &p.Symbol, nstr{&p.PaidByName})
	return p, err
}

func (x queries) GetPayout(ctx context.Context, id string) (Payout, error) {
	p, err := scanPayout(x.row(ctx, payoutSelect+` WHERE p.id = ?`, id))
	return p, notFound(err)
}

func (x queries) ListPayouts(ctx context.Context, kidID, status string) ([]Payout, error) {
	var where []string
	var args []any
	if kidID != "" {
		where, args = append(where, `p.kid_id = ?`), append(args, kidID)
	}
	if status != "" {
		where, args = append(where, `p.status = ?`), append(args, status)
	}
	q := payoutSelect
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	rows, err := x.rows(ctx, q+` ORDER BY p.created_at DESC, p.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (x queries) InsertPayout(ctx context.Context, p Payout) error {
	_, err := x.exec(ctx, `INSERT INTO payouts (id, kid_id, jar_id, order_id, amount, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.ID, p.KidID, p.JarID, p.OrderID, p.Amount, p.Status, x.t(p.CreatedAt))
	return err
}

func (x queries) PayPayout(ctx context.Context, p Payout) (bool, error) {
	res, err := x.exec(ctx, `UPDATE payouts SET status = 'paid', paid_by = ?, paid_at = ?, ledger_entry_id = ?
		WHERE id = ? AND status = 'pending'`, nul(p.PaidBy), x.tp(p.PaidAt), nul(p.LedgerEntryID), p.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// --- snapshots ------------------------------------------------------------------

func (x queries) PutSnapshot(ctx context.Context, s Snapshot) error {
	_, err := x.exec(ctx, `INSERT INTO portfolio_snapshots (kid_id, day, value, cost) VALUES (?, ?, ?, ?)
		ON CONFLICT (kid_id, day) DO UPDATE SET value = excluded.value, cost = excluded.cost`,
		s.KidID, s.Day, s.Value, s.Cost)
	return err
}

func (x queries) ListSnapshots(ctx context.Context, kidID, fromDay, toDay string) ([]Snapshot, error) {
	rows, err := x.rows(ctx, `SELECT kid_id, day, value, cost FROM portfolio_snapshots
		WHERE kid_id = ? AND day >= ? AND day <= ? ORDER BY day`, kidID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(&s.KidID, &s.Day, &s.Value, &s.Cost); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
