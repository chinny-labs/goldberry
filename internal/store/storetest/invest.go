package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chinny/goldberry/internal/market"
	"github.com/chinny/goldberry/internal/notify"
	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

var investTests = []struct {
	name string
	fn   func(*testing.T, Opener)
}{
	{"InvestingSwitch", testInvestingSwitch},
	{"InvestBuySellEarnings", testInvestBuySell},
	{"InvestQueuedUntilOpen", testInvestQueued},
	{"InvestSnapshotsAndOff", testInvestSnapshots},
}

const dollar = market.Micro // a price of $1, in micro-dollars

// investFixture turns investing on with a fake market listing AAPL at $200
// and VTI at $300, and gives Ava $20 in her Invest jar.
func investFixture(t *testing.T, open Opener) (*Fixture, *market.Fake, store.JarBalance) {
	t.Helper()
	ctx := context.Background()
	f := NewFixture(t, open)
	fake := market.NewFake()
	fake.Add(market.Security{Symbol: "AAPL", Name: "Apple Inc.", Kind: market.Stock}, 200*dollar, 200*dollar)
	fake.Add(market.Security{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Kind: market.Fund}, 300*dollar, 300*dollar)
	f.Svc.Market = fake
	_, err := f.Svc.SetInvesting(ctx, f.Mom, true)
	must(t, err)
	j := f.jars(t, f.Ava)[store.JarInvest]
	_, err = f.Svc.PostEntry(ctx, f.Mom, service.EntryInput{KidID: f.Ava.ID, JarID: j.ID, Amount: 2000, Comment: "Seed money"})
	must(t, err)
	return f, fake, f.jars(t, f.Ava)[store.JarInvest]
}

func testInvestingSwitch(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	var ue *service.UserError
	if _, err := f.Svc.SetInvesting(ctx, f.Mom, true); !errors.As(err, &ue) {
		t.Fatalf("turned on without a price feed: %v", err)
	}
	f.Svc.Market = market.NewFake()
	if _, err := f.Svc.SetInvesting(ctx, f.Ava, true); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a kid turned investing on: %v", err)
	}
	// Leo's split isn't the plain default, so it's left for a parent.
	lj := f.jars(t, f.Leo)
	must(t, f.Svc.SetSplit(ctx, f.Mom, f.Leo.ID, map[string]int{lj[store.JarSpend].ID: 10000}))
	if _, err := f.Svc.PlaceOrder(ctx, f.Ava, service.OrderInput{Symbol: "AAPL", Side: store.SideBuy, Amount: 100}); !errors.Is(err, service.ErrInvestingOff) {
		t.Fatalf("ordered while off: %v", err)
	}

	changed, err := f.Svc.SetInvesting(ctx, f.Mom, true)
	must(t, err)
	if changed != 1 {
		t.Fatalf("%d splits changed, want 1 (Ava)", changed)
	}
	st, err := f.Svc.InvestingStatus(ctx)
	must(t, err)
	if !st.Ready() {
		t.Fatalf("status %+v", st)
	}
	// Ava: 50/20/10/20 across four jars, so deposits fill Invest by themselves.
	_, err = f.Svc.PostEntry(ctx, f.Mom, service.EntryInput{KidID: f.Ava.ID, Amount: 1000, UseSplit: true})
	must(t, err)
	aj := f.jars(t, f.Ava)
	if aj[store.JarSpend].Balance != 500 || aj[store.JarSave].Balance != 200 || aj[store.JarGive].Balance != 100 || aj[store.JarInvest].Balance != 200 {
		t.Fatalf("Ava's split deposit: %+v", aj)
	}
	if lj := f.jars(t, f.Leo); lj[store.JarInvest].ID == "" {
		t.Fatal("Leo got no Invest jar")
	}
	rules, err := f.Store.ListSplitRules(ctx, f.Leo.ID)
	must(t, err)
	for _, r := range rules {
		if (r.JarID == lj[store.JarSpend].ID) != (r.BasisPoints == 10000) {
			t.Fatalf("Leo's split changed: %+v", rules)
		}
	}
	// Turning it on again doesn't add a second jar.
	_, err = f.Svc.SetInvesting(ctx, f.Mom, true)
	must(t, err)
	all, err := f.Store.ListJars(ctx, f.Ava.ID)
	must(t, err)
	if len(all) != 4 {
		t.Fatalf("Ava has %d jars", len(all))
	}
	// A kid added while investing is on starts with four jars.
	kid, err := f.Svc.CreateKid(ctx, f.Mom, service.KidInput{Username: "mia", DisplayName: "Mia", PIN: "9753"})
	must(t, err)
	if mj := f.jars(t, kid); mj[store.JarInvest].ID == "" {
		t.Fatal("new kid has no Invest jar")
	}
}

func order(t *testing.T, f *Fixture, kid store.User, side store.OrderSide, symbol string, amount int64, all bool) (store.Order, error) {
	t.Helper()
	return f.Svc.PlaceOrder(context.Background(), kid, service.OrderInput{Symbol: symbol, Side: side, Amount: amount, All: all})
}

func testInvestBuySell(t *testing.T, open Opener) {
	ctx := context.Background()
	f, fake, jar := investFixture(t, open)
	fake.SetOpen(true)

	// $10 of AAPL at $200 is 0.05 of a share, filled straight away.
	buy, err := f.Svc.PlaceOrder(ctx, f.Ava, service.OrderInput{Symbol: "aapl", Side: store.SideBuy, Amount: 1000, IdempotencyKey: "b1"})
	must(t, err)
	if buy.Status != store.OrderFilled || buy.Shares != 50_000 || buy.Price != 200*dollar || buy.Symbol != "AAPL" {
		t.Fatalf("buy %+v", buy)
	}
	again, err := f.Svc.PlaceOrder(ctx, f.Ava, service.OrderInput{Symbol: "AAPL", Side: store.SideBuy, Amount: 1000, IdempotencyKey: "b1"})
	must(t, err)
	if again.ID != buy.ID {
		t.Fatal("double submit bought twice")
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1000 {
		t.Fatalf("Invest jar after buying: %d", j.Balance)
	}
	var ins *service.ErrInsufficient
	if _, err := order(t, f, f.Ava, store.SideBuy, "AAPL", 1001, false); !errors.As(err, &ins) || ins.Available != 1000 {
		t.Fatalf("overspent: %v", err)
	}
	var ue *service.UserError
	if _, err := order(t, f, f.Ava, store.SideBuy, "AAPL", 99, false); !errors.As(err, &ue) {
		t.Fatalf("under the minimum: %v", err)
	}
	if _, err := order(t, f, f.Ava, store.SideBuy, "NOPE", 500, false); !errors.Is(err, service.ErrUnknownSymbol) {
		t.Fatalf("unknown symbol: %v", err)
	}
	if _, err := order(t, f, f.Ava, store.SideSell, "VTI", 500, false); !errors.As(err, &ue) {
		t.Fatalf("sold what she doesn't own: %v", err)
	}
	if _, err := f.Svc.PlaceOrder(ctx, f.Mom, service.OrderInput{Symbol: "AAPL", Side: store.SideBuy, Amount: 500}); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("an admin placed an order: %v", err)
	}
	if _, err := f.Svc.Reverse(ctx, f.Mom, buy.LedgerEntryID); !errors.Is(err, service.ErrCannotReverseInvest) {
		t.Fatalf("reversed a buy: %v", err)
	}

	// AAPL rises to $240: the $10 is now worth $12.
	fake.Set("AAPL", 240*dollar)
	f.Clock.Advance(2 * time.Minute) // past the quote cache
	p, err := f.Svc.Portfolio(ctx, f.Ava.ID)
	must(t, err)
	if len(p.Holdings) != 1 || p.Value != 1200 || p.Cost != 1000 || p.Gain() != 200 {
		t.Fatalf("portfolio %+v", p)
	}

	// Sell $6: half the shares. $5 of what she paid comes back now; the $1
	// profit waits for a parent.
	sell, err := order(t, f, f.Ava, store.SideSell, "AAPL", 600, false)
	must(t, err)
	if sell.Status != store.OrderFilled || sell.Shares != 25_000 || sell.Proceeds != 600 || sell.CostBasis != 500 {
		t.Fatalf("sell %+v", sell)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1500 {
		t.Fatalf("Invest jar after selling: %d", j.Balance)
	}
	owed, err := f.Svc.Earnings(ctx, f.Ava.ID)
	must(t, err)
	if len(owed) != 1 || owed[0].Amount != 100 || owed[0].Symbol != "AAPL" || owed[0].JarID != jar.ID {
		t.Fatalf("earnings %+v", owed)
	}
	ns, err := f.Store.ListNotifications(ctx, f.Mom.ID, 5)
	must(t, err)
	if len(ns) == 0 || ns[0].Kind != notify.EarningsOwed {
		t.Fatalf("parents not told about earnings: %+v", ns)
	}

	// The rest sells at $150, a loss: only what it's worth comes back, and
	// nothing is owed.
	fake.Set("AAPL", 150*dollar)
	f.Clock.Advance(2 * time.Minute)
	rest, err := order(t, f, f.Ava, store.SideSell, "AAPL", 0, true)
	must(t, err)
	if rest.Shares != 25_000 || rest.Proceeds != 375 || rest.CostBasis != 500 {
		t.Fatalf("sell all %+v", rest)
	}
	p, err = f.Svc.Portfolio(ctx, f.Ava.ID)
	must(t, err)
	if len(p.Holdings) != 0 || p.Cost != 0 || len(p.Owed) != 1 {
		t.Fatalf("after selling everything: %+v", p)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1875 {
		t.Fatalf("Invest jar after the loss: %d", j.Balance)
	}

	// Mom pays the $1; paying twice is refused.
	if _, err := f.Svc.PayEarnings(ctx, f.Ava, owed[0].ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a kid paid herself: %v", err)
	}
	paid, err := f.Svc.PayEarnings(ctx, f.Mom, owed[0].ID)
	must(t, err)
	if paid.Status != "paid" || paid.LedgerEntryID == "" {
		t.Fatalf("payout %+v", paid)
	}
	if _, err := f.Svc.PayEarnings(ctx, f.Dad, owed[0].ID); !errors.Is(err, service.ErrAlreadyPaid) {
		t.Fatalf("paid twice: %v", err)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1975 {
		t.Fatalf("Invest jar after earnings: %d", j.Balance)
	}
	gain, err := f.Store.GetLedgerEntry(ctx, paid.LedgerEntryID)
	must(t, err)
	if gain.Kind != store.KindInvestGain || gain.ActorID != f.Mom.ID {
		t.Fatalf("earnings entry %+v", gain)
	}

	// A parent lock on the Invest jar stops buying.
	_, err = f.Svc.SetLock(ctx, f.Mom, service.LockInput{JarID: jar.ID, Reason: "Not this week"})
	must(t, err)
	var locked *service.ErrJarLocked
	if _, err := order(t, f, f.Ava, store.SideBuy, "VTI", 500, false); !errors.As(err, &locked) {
		t.Fatalf("bought through a parent lock: %v", err)
	}
}

func testInvestQueued(t *testing.T, open Opener) {
	ctx := context.Background()
	f, fake, jar := investFixture(t, open) // the market is closed

	// A buy while closed waits, but the cash leaves the jar now.
	b, err := order(t, f, f.Ava, store.SideBuy, "VTI", 900, false)
	must(t, err)
	if b.Status != store.OrderQueued || b.Shares != 0 {
		t.Fatalf("closed-market buy %+v", b)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1100 {
		t.Fatalf("jar while waiting: %d", j.Balance)
	}
	// Cancelling gives it back; a sibling can't, and cancelling twice can't.
	if err := f.Svc.CancelOrder(ctx, f.Leo, b.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a sibling cancelled: %v", err)
	}
	must(t, f.Svc.CancelOrder(ctx, f.Ava, b.ID))
	if err := f.Svc.CancelOrder(ctx, f.Ava, b.ID); !errors.Is(err, service.ErrOrderNotQueued) {
		t.Fatalf("cancelled twice: %v", err)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 2000 {
		t.Fatalf("jar after cancelling: %d", j.Balance)
	}

	// Buy $15 of VTI overnight; nothing fills while the market is shut.
	b, err = order(t, f, f.Ava, store.SideBuy, "VTI", 1500, false)
	must(t, err)
	n, err := f.Svc.FillQueued(ctx)
	must(t, err)
	if n != 0 {
		t.Fatal("filled while closed")
	}
	// Next morning VTI opens at $250 and trades at $260: the order fills at
	// the open, like a real broker's.
	f.Clock.Advance(24 * time.Hour)
	fake.Add(market.Security{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Kind: market.Fund}, 260*dollar, 250*dollar)
	fake.SetOpen(true)
	n, err = f.Svc.FillQueued(ctx)
	must(t, err)
	got, err := f.Store.GetOrder(ctx, b.ID)
	must(t, err)
	if n != 1 || got.Status != store.OrderFilled || got.Price != 250*dollar || got.Shares != 60_000 {
		t.Fatalf("filled %d: %+v", n, got)
	}
	ns, err := f.Store.ListNotifications(ctx, f.Ava.ID, 1)
	must(t, err)
	if len(ns) != 1 || ns[0].Kind != notify.OrderFilled {
		t.Fatalf("Ava not told: %+v", ns)
	}
	if n, _ := f.Svc.FillQueued(ctx); n != 0 {
		t.Fatal("filled twice")
	}

	// A sale placed after hours reserves its shares until it fills.
	fake.SetOpen(false)
	f.Clock.Advance(2 * time.Minute)
	s, err := order(t, f, f.Ava, store.SideSell, "VTI", 0, true)
	must(t, err)
	if s.Status != store.OrderQueued || s.Shares != 60_000 {
		t.Fatalf("queued sale %+v", s)
	}
	var ue *service.UserError
	if _, err := order(t, f, f.Ava, store.SideSell, "VTI", 100, false); !errors.As(err, &ue) {
		t.Fatalf("sold reserved shares: %v", err)
	}
	if err := f.Svc.ArchiveJar(ctx, f.Mom, jar.ID); !errors.As(err, &ue) {
		t.Fatalf("archived an Invest jar with holdings: %v", err)
	}
	f.Clock.Advance(24 * time.Hour)
	fake.Add(market.Security{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Kind: market.Fund}, 280*dollar, 275*dollar)
	fake.SetOpen(true)
	n, err = f.Svc.FillQueued(ctx)
	must(t, err)
	got, err = f.Store.GetOrder(ctx, s.ID)
	must(t, err)
	// 0.06 × $275 = $16.50: $15 back now, $1.50 owed.
	if n != 1 || got.Proceeds != 1650 || got.CostBasis != 1500 {
		t.Fatalf("queued sale filled %d: %+v", n, got)
	}
	owed, err := f.Svc.Earnings(ctx, "")
	must(t, err)
	if len(owed) != 1 || owed[0].Amount != 150 || owed[0].KidName != "Ava" {
		t.Fatalf("earnings %+v", owed)
	}
}

func testInvestSnapshots(t *testing.T, open Opener) {
	ctx := context.Background()
	f, fake, _ := investFixture(t, open)
	fake.SetOpen(true)
	_, err := order(t, f, f.Ava, store.SideBuy, "AAPL", 1000, false)
	must(t, err)
	n, err := f.Svc.Snapshot(ctx)
	must(t, err)
	if n != 1 {
		t.Fatalf("%d snapshots, want Ava's", n)
	}
	if n, _ := f.Svc.Snapshot(ctx); n != 0 {
		t.Fatal("snapshotted again within five minutes")
	}
	f.Clock.Advance(24 * time.Hour)
	fake.Set("AAPL", 220*dollar)
	_, err = f.Svc.Snapshot(ctx)
	must(t, err)
	rng, err := f.Svc.ResolveRange(ctx, service.RangeWeek, "", "")
	must(t, err)
	h, err := f.Svc.InvestHistory(ctx, f.Ava.ID, rng)
	must(t, err)
	if len(h.Series) != 2 || len(h.Series[0].Points) != 2 || h.Series[0].Start != 1000 || h.Series[0].End != 1100 || h.Series[1].End != 1000 {
		t.Fatalf("history %+v", h.Series)
	}

	// Turning investing off cancels waiting orders and refunds them.
	fake.SetOpen(false)
	f.Clock.Advance(2 * time.Minute)
	q, err := order(t, f, f.Ava, store.SideBuy, "VTI", 500, false)
	must(t, err)
	_, err = f.Svc.SetInvesting(ctx, f.Mom, false)
	must(t, err)
	got, err := f.Store.GetOrder(ctx, q.ID)
	must(t, err)
	if got.Status != store.OrderCancelled {
		t.Fatalf("waiting order after turning off: %+v", got)
	}
	if j := f.jars(t, f.Ava)[store.JarInvest]; j.Balance != 1000 {
		t.Fatalf("Invest jar after turning off: %d", j.Balance)
	}
	if _, err := order(t, f, f.Ava, store.SideSell, "AAPL", 0, true); !errors.Is(err, service.ErrInvestingOff) {
		t.Fatalf("sold while off: %v", err)
	}
}
