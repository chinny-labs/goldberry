package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chinny-labs/goldberry/internal/market"
	"github.com/chinny-labs/goldberry/internal/notify"
	"github.com/chinny-labs/goldberry/internal/store"
)

// Investing (plan §7.6, ADR 0014). Kids buy and sell real US stocks and funds
// at real prices with the money in their Invest jar. The app moves no real
// money: like every other balance, the jar is what the parents hold for the
// kid. Buying takes cash out of the jar; selling puts back up to what was
// paid, so a loss comes off by itself, and any profit waits as earnings a
// parent pays.
//
// Units: cash is minor units (cents; investing needs a USD household), prices
// are micro-dollars and shares are micro-shares (millionths of a share).

const (
	// MinOrder is the smallest buy or sell, in cents.
	MinOrder = 100
	// MicroShares is one whole share.
	MicroShares = 1_000_000

	settingInvesting = "investing_enabled"
	statusTTL        = time.Minute
	quoteTTLOpen     = time.Minute
	quoteTTLClosed   = 30 * time.Minute
	snapshotEvery    = 5 * time.Minute
)

// DefaultInvestSplit is the split for a kid with an Invest jar: half to
// spend, and a fifth each to save and to invest.
var DefaultInvestSplit = map[store.JarKind]int{store.JarSpend: 5000, store.JarSave: 2000, store.JarGive: 1000, store.JarInvest: 2000}

var (
	ErrInvestingOff        = errors.New("investing isn't turned on; a parent can turn it on in Settings")
	ErrNoInvestJar         = errors.New("you don't have an Invest jar yet; ask a parent")
	ErrMarketDown          = errors.New("couldn't get prices right now; try again in a minute")
	ErrUnknownSymbol       = errors.New("couldn't find that stock or fund")
	ErrAlreadyPaid         = errors.New("those earnings were already paid")
	ErrOrderNotQueued      = errors.New("that order already went through or was cancelled")
	ErrCannotReverseInvest = errors.New("investing entries can't be undone; sell, or cancel a waiting order, instead")
)

// investCache keeps the market's open/closed status between calls.
type investCache struct {
	mu       sync.Mutex
	status   market.Status
	statusAt time.Time
	snapAt   time.Time

	holdings    map[string]holdingsEntry
	holdingsRun map[string]chan struct{} // fetches under way, closed when done
}

// holdingsEntry is a fund's contents as last fetched, or the time a fetch failed.
type holdingsEntry struct {
	h     market.FundHoldings
	ok    bool
	fresh bool // fetched fine, so it lasts a day rather than a retry interval
	tried time.Time
}

const (
	holdingsTTL      = 24 * time.Hour   // funds report monthly; a day is plenty
	holdingsRetry    = 15 * time.Minute // after a failure, before asking again
	holdingsTopCount = 10
)

// --- unit math ---------------------------------------------------------------------

// mulDiv is a×b÷c, floored, without overflow (a share count times a price
// overflows int64).
func mulDiv(a, b, c int64) int64 {
	if c == 0 {
		return 0
	}
	r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return r.Quo(r, big.NewInt(c)).Int64()
}

// centScale converts between cents and micro-shares × micro-dollars: a cent
// is 10,000 micro-dollars and a share is 1,000,000 micro-shares.
const centScale = (market.Micro / 100) * MicroShares

// SharesFor is how many micro-shares amount cents buys at price.
func SharesFor(amount, price int64) int64 { return mulDiv(amount, centScale, price) }

// ValueOf is what shares are worth at price, in cents (floored).
func ValueOf(shares, price int64) int64 { return mulDiv(shares, price, centScale) }

// sharesWorth is the fewest micro-shares worth at least amount cents.
func sharesWorth(amount, price int64) int64 {
	s := SharesFor(amount, price)
	if ValueOf(s, price) < amount {
		s++
	}
	return s
}

// --- settings ------------------------------------------------------------------

// InvestStatus is whether investing can run and whether it's on.
type InvestStatus struct {
	Enabled    bool // a parent turned it on
	Configured bool // a market data key is set
	USD        bool // the household uses US dollars, the market's currency
}

// Ready reports whether kids can trade.
func (st InvestStatus) Ready() bool { return st.Enabled && st.Configured && st.USD }

// InvestingStatus reads the investing switch.
func (s *Service) InvestingStatus(ctx context.Context) (InvestStatus, error) {
	h, err := s.Store.GetHousehold(ctx)
	if err != nil {
		return InvestStatus{}, err
	}
	st := InvestStatus{Configured: s.Market != nil, USD: h.Currency == "USD"}
	v, err := s.Store.GetSetting(ctx, settingInvesting)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return st, err
	}
	st.Enabled = v == "1"
	return st, nil
}

// SetInvesting turns investing on or off. Turning it on gives every kid an
// Invest jar; kids still on the plain 70/20/10 split move to
// DefaultInvestSplit, and other splits are left for a parent to change. It
// reports how many kids' splits changed. Turning it off cancels waiting
// orders (refunding buys) and leaves holdings where they are.
func (s *Service) SetInvesting(ctx context.Context, actor store.User, on bool) (int, error) {
	if err := requireAdmin(actor); err != nil {
		return 0, err
	}
	st, err := s.InvestingStatus(ctx)
	if err != nil {
		return 0, err
	}
	if on && !st.Configured {
		return 0, invalid("investing", "Set GOLDBERRY_FINNHUB_API_KEY first: prices come from Finnhub.")
	}
	if on && !st.USD {
		return 0, invalid("investing", "Investing uses US stock prices, so it needs a household in US dollars.")
	}
	if !on {
		queued, err := s.Store.ListOrders(ctx, store.OrderFilter{Status: store.OrderQueued})
		if err != nil {
			return 0, err
		}
		for _, o := range queued {
			if err := s.cancelOrder(ctx, actor, o.ID); err != nil && !errors.Is(err, ErrOrderNotQueued) {
				return 0, err
			}
		}
	}
	changed := 0
	err = s.Store.Tx(ctx, "", func(tx store.Tx) error {
		val := "0"
		if on {
			val = "1"
			kids, err := tx.ListUsers(ctx, store.RoleKid)
			if err != nil {
				return err
			}
			for _, k := range kids {
				moved, err := s.ensureInvestJar(ctx, tx, k.ID)
				if err != nil {
					return err
				}
				if moved {
					changed++
				}
			}
		}
		if err := tx.PutSetting(ctx, settingInvesting, val, false); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor.ID, "investing.set", "", map[string]any{"on": on})
	})
	return changed, err
}

// ensureInvestJar adds an Invest jar if the kid has none, and moves a plain
// default split to DefaultInvestSplit. It reports whether the split changed.
func (s *Service) ensureInvestJar(ctx context.Context, tx store.Tx, kidID string) (bool, error) {
	jars, err := tx.ListJars(ctx, kidID)
	if err != nil {
		return false, err
	}
	byKind := map[store.JarKind]store.Jar{}
	order := 0
	for _, j := range jars {
		if _, dup := byKind[j.Kind]; !dup {
			byKind[j.Kind] = j
		}
		order = max(order, j.SortOrder+1)
	}
	if _, ok := byKind[store.JarInvest]; !ok {
		j := store.Jar{ID: store.NewID(), KidID: kidID, Name: "Invest", Kind: store.JarInvest, SortOrder: order}
		if err := tx.CreateJar(ctx, j); err != nil {
			return false, err
		}
		if err := tx.PutSplitRule(ctx, store.SplitRule{KidID: kidID, JarID: j.ID}); err != nil {
			return false, err
		}
		byKind[store.JarInvest] = j
		jars = append(jars, j)
	}
	rules, err := tx.ListSplitRules(ctx, kidID)
	if err != nil {
		return false, err
	}
	plain := map[string]int{}
	for _, d := range DefaultJars {
		if j, ok := byKind[d.Kind]; ok {
			plain[j.ID] = d.BPS
		}
	}
	if len(plain) != len(DefaultJars) {
		return false, nil
	}
	for _, r := range rules {
		if r.BasisPoints != plain[r.JarID] {
			return false, nil
		}
	}
	var next []store.SplitRule
	for _, j := range jars {
		next = append(next, store.SplitRule{KidID: kidID, JarID: j.ID, BasisPoints: DefaultInvestSplit[j.Kind]})
	}
	return true, tx.ReplaceSplitRules(ctx, kidID, next)
}

func (s *Service) investReady(ctx context.Context) error {
	st, err := s.InvestingStatus(ctx)
	if err != nil {
		return err
	}
	if !st.Ready() {
		return ErrInvestingOff
	}
	return nil
}

// --- prices --------------------------------------------------------------------

// MarketStatus is whether the US market is open, cached for a minute.
func (s *Service) MarketStatus(ctx context.Context) (market.Status, error) {
	if s.Market == nil {
		return market.Status{}, ErrInvestingOff
	}
	c := &s.invest
	c.mu.Lock()
	if !c.statusAt.IsZero() && s.now().Sub(c.statusAt) < statusTTL {
		st := c.status
		c.mu.Unlock()
		return st, nil
	}
	c.mu.Unlock()
	st, err := s.Market.Status(ctx)
	if err != nil {
		s.Log.Warn("market status", "err", err)
		return st, ErrMarketDown
	}
	c.mu.Lock()
	c.status, c.statusAt = st, s.now()
	c.mu.Unlock()
	return st, nil
}

// Quote is a symbol's price, from the cache while it's fresh (a minute while
// the market is open, half an hour while it's closed). When the provider
// fails, a cached price is returned with stale set.
func (s *Service) Quote(ctx context.Context, symbol string) (q store.Quote, stale bool, err error) {
	cached, cerr := s.Store.GetQuote(ctx, symbol)
	if cerr != nil && !errors.Is(cerr, store.ErrNotFound) {
		return q, false, cerr
	}
	have := cerr == nil
	if s.Market == nil {
		if have {
			return cached, true, nil
		}
		return q, false, ErrInvestingOff
	}
	ttl := quoteTTLClosed
	if st, err := s.MarketStatus(ctx); err == nil && st.Open {
		ttl = quoteTTLOpen
	}
	if have && s.now().Sub(cached.FetchedAt) < ttl {
		return cached, false, nil
	}
	mq, err := s.Market.Quote(ctx, symbol)
	if err != nil {
		if errors.Is(err, market.ErrUnknownSymbol) {
			return q, false, ErrUnknownSymbol
		}
		s.Log.Warn("market quote", "symbol", symbol, "err", err)
		if have {
			return cached, true, nil
		}
		return q, false, ErrMarketDown
	}
	q = store.Quote{Symbol: symbol, Price: mq.Price, Open: mq.Open, PrevClose: mq.PrevClose, QuotedAt: mq.At, FetchedAt: s.now()}
	if err := s.Store.Tx(ctx, "", func(tx store.Tx) error { return tx.PutQuote(ctx, q) }); err != nil {
		return q, false, err
	}
	return q, false, nil
}

// FundHoldings is what a fund owns: its biggest holdings, cached for a day.
// It reports false when there's no source, the source has nothing for the
// fund, or it can't be reached (an older copy is used if there is one), so a
// page can simply leave the section out.
//
// A report can take a while (a bond fund's runs to megabytes), so the fetch
// runs on its own, outliving the page that asked: the page waits as long as
// its context allows, and if that isn't long enough the next view finds it
// cached.
func (s *Service) FundHoldings(ctx context.Context, symbol string) (market.FundHoldings, bool) {
	if s.Holdings == nil {
		return market.FundHoldings{}, false
	}
	c := &s.invest
	c.mu.Lock()
	e, have := c.holdings[symbol]
	if have {
		wait := holdingsRetry
		if e.fresh {
			wait = holdingsTTL
		}
		if s.now().Sub(e.tried) < wait {
			c.mu.Unlock()
			return e.h, e.ok
		}
	}
	done, running := c.holdingsRun[symbol]
	if !running {
		done = make(chan struct{})
		if c.holdingsRun == nil {
			c.holdingsRun = map[string]chan struct{}{}
		}
		c.holdingsRun[symbol] = done
		go s.fetchHoldings(context.WithoutCancel(ctx), symbol, e, done)
	}
	c.mu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		return e.h, e.ok
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e = c.holdings[symbol]
	return e.h, e.ok
}

// fetchHoldings asks the source and records the answer; on failure it keeps
// the old copy (if any) and waits before trying again.
func (s *Service) fetchHoldings(ctx context.Context, symbol string, old holdingsEntry, done chan struct{}) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	h, err := s.Holdings.Holdings(ctx, symbol, holdingsTopCount)
	e := holdingsEntry{h: old.h, ok: old.ok, tried: s.now()}
	if err == nil {
		e = holdingsEntry{h: h, ok: true, fresh: true, tried: s.now()}
	} else if !errors.Is(err, market.ErrNoHoldings) {
		s.Log.Warn("fund holdings", "symbol", symbol, "err", err)
	}
	c := &s.invest
	c.mu.Lock()
	if c.holdings == nil {
		c.holdings = map[string]holdingsEntry{}
	}
	c.holdings[symbol] = e
	delete(c.holdingsRun, symbol)
	c.mu.Unlock()
	close(done)
}

// Security looks a symbol up, remembering its name.
func (s *Service) Security(ctx context.Context, symbol string) (store.Security, error) {
	sym, ok := market.NormalizeSymbol(symbol)
	if !ok {
		return store.Security{}, ErrUnknownSymbol
	}
	if sec, err := s.Store.GetSecurity(ctx, sym); err == nil {
		return sec, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return sec, err
	}
	if s.Market == nil {
		return store.Security{}, ErrInvestingOff
	}
	ms, err := s.Market.Lookup(ctx, sym)
	if errors.Is(err, market.ErrUnknownSymbol) {
		return store.Security{}, ErrUnknownSymbol
	}
	if err != nil {
		s.Log.Warn("market lookup", "symbol", sym, "err", err)
		return store.Security{}, ErrMarketDown
	}
	sec := store.Security{Symbol: ms.Symbol, Name: ms.Name, Kind: string(ms.Kind), UpdatedAt: s.now()}
	return sec, s.Store.Tx(ctx, "", func(tx store.Tx) error { return tx.PutSecurity(ctx, sec) })
}

// Search finds stocks and funds by name or symbol.
func (s *Service) Search(ctx context.Context, query string) ([]market.Security, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if s.Market == nil {
		return nil, ErrInvestingOff
	}
	found, err := s.Market.Search(ctx, query)
	if err != nil {
		s.Log.Warn("market search", "err", err)
		return nil, ErrMarketDown
	}
	if len(found) > 20 {
		found = found[:20]
	}
	return found, nil
}

// --- the portfolio ---------------------------------------------------------------

// Holding is what a kid owns of one symbol.
type Holding struct {
	Symbol    string
	Name      string
	Kind      string
	Shares    int64 // micro-shares owned
	Reserved  int64 // micro-shares in waiting sell orders
	Cost      int64 // cents paid for the shares still owned
	Price     int64 // micro-dollars
	PrevClose int64
	Stale     bool // the price couldn't be refreshed
	Value     int64
}

// Gain is what the holding has made (or lost) so far.
func (h Holding) Gain() int64 { return h.Value - h.Cost }

// DayChange is how much the holding moved since yesterday's close.
func (h Holding) DayChange() int64 {
	if h.PrevClose == 0 {
		return 0
	}
	return ValueOf(h.Shares, h.Price) - ValueOf(h.Shares, h.PrevClose)
}

// Free is the micro-shares that can still be sold.
func (h Holding) Free() int64 { return h.Shares - h.Reserved }

type position struct{ shares, reserved, cost int64 }

// positions adds up a kid's orders by symbol.
func positions(orders []store.Order) map[string]*position {
	out := map[string]*position{}
	for _, o := range orders {
		p := out[o.Symbol]
		if p == nil {
			p = &position{}
			out[o.Symbol] = p
		}
		switch {
		case o.Status == store.OrderFilled && o.Side == store.SideBuy:
			p.shares += o.Shares
			p.cost += o.Amount
		case o.Status == store.OrderFilled && o.Side == store.SideSell:
			p.shares -= o.Shares
			p.cost -= o.CostBasis
		case o.Status == store.OrderQueued && o.Side == store.SideSell:
			p.reserved += o.Shares
		}
	}
	return out
}

// Portfolio is a kid's investing page.
type Portfolio struct {
	Status   InvestStatus
	Market   market.Status
	MarketOK bool
	Jar      store.JarBalance // the Invest jar
	HasJar   bool
	Holdings []Holding
	Value    int64 // holdings at today's prices
	Cost     int64 // what was paid for them
	Queued   []store.Order
	Recent   []store.Order // filled and cancelled, newest first
	Owed     []store.Payout
	Paid     []store.Payout
}

// Gain is the whole portfolio's gain or loss so far.
func (p Portfolio) Gain() int64 { return p.Value - p.Cost }

// OwedTotal is earnings waiting for a parent.
func (p Portfolio) OwedTotal() int64 {
	var t int64
	for _, x := range p.Owed {
		t += x.Amount
	}
	return t
}

// Holding returns the kid's holding of one symbol.
func (p Portfolio) Holding(symbol string) (Holding, bool) {
	for _, h := range p.Holdings {
		if h.Symbol == symbol {
			return h, true
		}
	}
	return Holding{}, false
}

// investJar is the kid's Invest jar.
func investJar(ctx context.Context, r store.Reader, kidID string) (store.JarBalance, error) {
	jars, err := r.JarBalances(ctx, kidID)
	if err != nil {
		return store.JarBalance{}, err
	}
	for _, j := range jars {
		if j.Kind == store.JarInvest {
			return j, nil
		}
	}
	return store.JarBalance{}, ErrNoInvestJar
}

// Portfolio loads a kid's investments at current prices.
func (s *Service) Portfolio(ctx context.Context, kidID string) (Portfolio, error) {
	var p Portfolio
	if _, err := getKid(ctx, s.Store, kidID); err != nil {
		return p, err
	}
	var err error
	if p.Status, err = s.InvestingStatus(ctx); err != nil {
		return p, err
	}
	if s.Market != nil {
		if p.Market, err = s.MarketStatus(ctx); err == nil {
			p.MarketOK = true
		}
	}
	if p.Jar, err = investJar(ctx, s.Store, kidID); err == nil {
		p.HasJar = true
	} else if !errors.Is(err, ErrNoInvestJar) {
		return p, err
	}
	orders, err := s.Store.ListOrders(ctx, store.OrderFilter{KidID: kidID})
	if err != nil {
		return p, err
	}
	names := map[string][2]string{}
	for _, o := range orders {
		names[o.Symbol] = [2]string{o.Name, o.SecurityKind}
		switch {
		case o.Status == store.OrderQueued:
			p.Queued = append([]store.Order{o}, p.Queued...) // oldest first
		case len(p.Recent) < 20:
			p.Recent = append(p.Recent, o)
		}
	}
	for sym, pos := range positions(orders) {
		if pos.shares <= 0 && pos.reserved == 0 {
			continue
		}
		h := Holding{Symbol: sym, Name: names[sym][0], Kind: names[sym][1], Shares: pos.shares, Reserved: pos.reserved, Cost: pos.cost}
		q, stale, err := s.Quote(ctx, sym)
		if err == nil {
			h.Price, h.PrevClose, h.Stale = q.Price, q.PrevClose, stale
			h.Value = ValueOf(h.Shares, h.Price)
		} else {
			h.Stale, h.Value = true, h.Cost // no price at all: show what was paid
		}
		p.Holdings = append(p.Holdings, h)
		p.Value += h.Value
		p.Cost += h.Cost
	}
	sort.Slice(p.Holdings, func(i, j int) bool { return p.Holdings[i].Value > p.Holdings[j].Value })
	payouts, err := s.Store.ListPayouts(ctx, kidID, "")
	if err != nil {
		return p, err
	}
	for _, x := range payouts {
		if x.Status == "pending" {
			p.Owed = append(p.Owed, x)
		} else {
			p.Paid = append(p.Paid, x)
		}
	}
	return p, nil
}

// --- orders ----------------------------------------------------------------------

// OrderInput is a kid buying or selling. Amount is cents; for a sale, All
// sells every free share instead.
type OrderInput struct {
	Symbol         string
	Side           store.OrderSide
	Amount         int64
	All            bool
	IdempotencyKey string
}

// PlaceOrder buys or sells for the signed-in kid. While the market is open it
// fills at the current price; while it's closed the order waits and fills at
// the next opening price. A buy takes the cash out of the Invest jar straight
// away; a sale returns up to what was paid, and any profit becomes earnings
// for a parent to pay.
func (s *Service) PlaceOrder(ctx context.Context, kid store.User, in OrderInput) (store.Order, error) {
	if !kid.IsKid() || kid.Disabled() {
		return store.Order{}, ErrForbidden
	}
	if err := s.investReady(ctx); err != nil {
		return store.Order{}, err
	}
	if in.Side != store.SideBuy && in.Side != store.SideSell {
		return store.Order{}, invalid("side", "Buy or sell?")
	}
	if !in.All && in.Amount < MinOrder {
		return store.Order{}, invalid("amount", "The smallest order is $1.00.")
	}
	sec, err := s.Security(ctx, in.Symbol)
	if err != nil {
		return store.Order{}, err
	}
	st, err := s.MarketStatus(ctx)
	if err != nil {
		return store.Order{}, err
	}
	q, stale, err := s.Quote(ctx, sec.Symbol)
	if err != nil {
		return store.Order{}, err
	}
	if st.Open && stale {
		return store.Order{}, ErrMarketDown // never fill at an old price
	}
	_, cur, err := s.Household(ctx)
	if err != nil {
		return store.Order{}, err
	}

	var out store.Order
	var notifyAdmins *store.Payout
	err = s.Store.Tx(ctx, kid.ID, func(tx store.Tx) error {
		if in.IdempotencyKey != "" {
			if prev, err := tx.GetOrderByKey(ctx, in.IdempotencyKey); err == nil {
				if prev.KidID != kid.ID {
					return ErrForbidden
				}
				out = prev
				return nil
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		jar, err := investJar(ctx, tx, kid.ID)
		if err != nil {
			return err
		}
		now := s.now()
		o := store.Order{ID: store.NewID(), KidID: kid.ID, JarID: jar.ID, Symbol: sec.Symbol, Side: in.Side,
			Status: store.OrderQueued, PlacedBy: kid.ID, IdempotencyKey: in.IdempotencyKey, CreatedAt: now,
			Name: sec.Name, SecurityKind: sec.Kind}
		if in.Side == store.SideBuy {
			if err := s.buyLocks(ctx, tx, kid.ID, jar); err != nil {
				return err
			}
			if in.Amount > jar.Available() {
				return &ErrInsufficient{Available: jar.Available(), Jar: jar.Name, Currency: cur}
			}
			o.Amount = in.Amount
			comment := "Buying " + sec.Symbol + " when the market opens"
			if st.Open {
				if o.Shares = SharesFor(in.Amount, q.Price); o.Shares == 0 {
					return invalid("amount", "That's too little to buy any %s.", sec.Symbol)
				}
				o.Status, o.Price, o.FilledAt, comment = store.OrderFilled, q.Price, &now, "Bought "+sec.Symbol
			}
			e := store.LedgerEntry{ID: store.NewID(), KidID: kid.ID, JarID: jar.ID, Amount: -in.Amount,
				Kind: store.KindInvestBuy, Comment: comment, ActorID: kid.ID, IdempotencyKey: "order:" + o.ID + ":buy",
				EffectiveAt: now, CreatedAt: now}
			if _, err := tx.InsertLedgerEntry(ctx, e); err != nil {
				return err
			}
			o.LedgerEntryID = e.ID
		} else {
			orders, err := tx.ListOrders(ctx, store.OrderFilter{KidID: kid.ID})
			if err != nil {
				return err
			}
			pos := positions(orders)[sec.Symbol]
			if pos == nil || pos.shares-pos.reserved <= 0 {
				return invalid("amount", "You don't have any %s to sell.", sec.Symbol)
			}
			free := pos.shares - pos.reserved
			o.Shares = free
			if !in.All {
				o.Shares = min(free, sharesWorth(in.Amount, q.Price))
			}
			if st.Open {
				p, err := s.fillSell(ctx, tx, &o, pos, q.Price, now)
				if err != nil {
					return err
				}
				notifyAdmins = p
			}
		}
		if _, err := tx.InsertOrder(ctx, o); err != nil {
			return err
		}
		if notifyAdmins != nil {
			if err := tx.InsertPayout(ctx, *notifyAdmins); err != nil {
				return err
			}
			if err := s.notifyEarnings(ctx, tx, kid, *notifyAdmins, sec.Symbol); err != nil {
				return err
			}
		}
		out = o
		return nil
	})
	return out, err
}

// buyLocks refuses a buy when a whole-jar lock is on the Invest jar: money
// leaving the jar is what a lock is for.
func (s *Service) buyLocks(ctx context.Context, tx store.Tx, kidID string, jar store.JarBalance) error {
	locks, err := s.KidLocks(ctx, tx, kidID)
	if err != nil {
		return err
	}
	for _, l := range locks {
		if l.JarID != jar.ID || l.ToJarID != "" {
			continue
		}
		if l.Hard() {
			return &ErrJarLocked{Lock: l}
		}
		return invalid("", "You locked your %s jar. Remove the lock first if you really want to buy.", jar.Name)
	}
	return nil
}

// fillSell prices a sale of o.Shares at price: it posts the credit (up to
// what those shares cost) and returns the payout for any profit, which the
// caller inserts after the order.
func (s *Service) fillSell(ctx context.Context, tx store.Tx, o *store.Order, pos *position, price int64, now time.Time) (*store.Payout, error) {
	o.Shares = min(o.Shares, pos.shares)
	o.Price, o.Status, o.FilledAt = price, store.OrderFilled, &now
	o.Proceeds = ValueOf(o.Shares, price)
	o.CostBasis = pos.cost
	if o.Shares < pos.shares {
		o.CostBasis = mulDiv(pos.cost, o.Shares, pos.shares) // average cost
	}
	back := min(o.Proceeds, o.CostBasis)
	if back > 0 {
		comment := "Sold " + o.Symbol
		if o.Proceeds < o.CostBasis {
			comment += fmt.Sprintf(" (%s less than you paid)", usd(o.CostBasis-o.Proceeds))
		}
		e := store.LedgerEntry{ID: store.NewID(), KidID: o.KidID, JarID: o.JarID, Amount: back, Kind: store.KindInvestSell,
			Comment: comment, ActorID: o.PlacedBy, IdempotencyKey: "order:" + o.ID + ":sell", EffectiveAt: now, CreatedAt: now}
		if _, err := tx.InsertLedgerEntry(ctx, e); err != nil {
			return nil, err
		}
		o.LedgerEntryID = e.ID
	}
	if gain := o.Proceeds - o.CostBasis; gain > 0 {
		return &store.Payout{ID: store.NewID(), KidID: o.KidID, JarID: o.JarID, OrderID: o.ID, Amount: gain,
			Status: "pending", CreatedAt: now}, nil
	}
	return nil, nil
}

func usd(cents int64) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

func (s *Service) notifyEarnings(ctx context.Context, tx store.Tx, kid store.User, p store.Payout, symbol string) error {
	admins, err := notify.Admins(ctx, tx)
	if err != nil {
		return err
	}
	return notify.Send(ctx, tx, s.now(), admins, notify.EarningsOwed, "", notify.Payload{
		KidID: kid.ID, KidName: kid.DisplayName, Amount: p.Amount, Text: symbol,
	})
}

// CancelOrder cancels a waiting order. A buy's cash goes back to the jar as
// a reversal of the original debit. Kids cancel their own; admins any.
func (s *Service) CancelOrder(ctx context.Context, actor store.User, orderID string) error {
	return s.cancelOrder(ctx, actor, orderID)
}

func (s *Service) cancelOrder(ctx context.Context, actor store.User, orderID string) error {
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if !actor.IsAdmin() && o.KidID != actor.ID {
		return ErrNotFound // a sibling's order doesn't exist, as far as they know
	}
	return s.Store.Tx(ctx, o.KidID, func(tx store.Tx) error {
		now := s.now()
		ok, err := tx.CancelOrder(ctx, o.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrOrderNotQueued
		}
		if o.Side == store.SideBuy && o.LedgerEntryID != "" {
			_, err := tx.InsertLedgerEntry(ctx, store.LedgerEntry{ID: store.NewID(), KidID: o.KidID, JarID: o.JarID,
				Amount: o.Amount, Kind: store.KindReversal, Comment: "Cancelled: buy " + o.Symbol, ActorID: actor.ID,
				ReversesID: o.LedgerEntryID, IdempotencyKey: "order:" + o.ID + ":cancel", EffectiveAt: now, CreatedAt: now})
			return err
		}
		return nil
	})
}

// --- the background job --------------------------------------------------------------

// marketOpenAt is 9:30 in New York on t's New York date: when the regular
// session opens.
func marketOpenAt(t time.Time) time.Time {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		ny = time.UTC
	}
	l := t.In(ny)
	return time.Date(l.Year(), l.Month(), l.Day(), 9, 30, 0, 0, ny)
}

// FillQueued fills waiting orders once the market is open. An order placed
// before today's open fills at today's opening price, like a broker's
// market-on-open order. It reports how many filled.
func (s *Service) FillQueued(ctx context.Context) (int, error) {
	if s.Market == nil {
		return 0, nil
	}
	st, err := s.InvestingStatus(ctx)
	if err != nil || !st.Enabled {
		return 0, err
	}
	queued, err := s.Store.ListOrders(ctx, store.OrderFilter{Status: store.OrderQueued})
	if err != nil || len(queued) == 0 {
		return 0, err
	}
	ms, err := s.MarketStatus(ctx)
	if errors.Is(err, ErrMarketDown) {
		return 0, nil // already logged; try again next tick
	}
	if err != nil {
		return 0, err
	}
	if !ms.Open {
		return 0, nil
	}
	n := 0
	for _, o := range queued {
		q, stale, err := s.Quote(ctx, o.Symbol)
		if err != nil || stale {
			continue
		}
		price := q.Price
		if o.CreatedAt.Before(marketOpenAt(s.now())) && q.Open > 0 {
			price = q.Open
		}
		filled, err := s.fillQueued(ctx, o, price)
		if err != nil {
			return n, err
		}
		if filled {
			n++
		}
	}
	return n, nil
}

func (s *Service) fillQueued(ctx context.Context, o store.Order, price int64) (bool, error) {
	kid, err := getKid(ctx, s.Store, o.KidID)
	if err != nil {
		return false, err
	}
	filled := false
	err = s.Store.Tx(ctx, o.KidID, func(tx store.Tx) error {
		cur, err := tx.GetOrder(ctx, o.ID)
		if err != nil || cur.Status != store.OrderQueued {
			return err
		}
		now := s.now()
		var payout *store.Payout
		amount := cur.Amount
		if cur.Side == store.SideBuy {
			cur.Shares, cur.Price, cur.Status, cur.FilledAt = SharesFor(cur.Amount, price), price, store.OrderFilled, &now
			if cur.Shares == 0 { // the price rose past the whole order: give the cash back
				return s.cancelInTx(ctx, tx, cur, now)
			}
		} else {
			orders, err := tx.ListOrders(ctx, store.OrderFilter{KidID: cur.KidID})
			if err != nil {
				return err
			}
			pos := positions(orders)[cur.Symbol]
			if pos == nil || pos.shares <= 0 {
				_, err := tx.CancelOrder(ctx, cur.ID, now)
				return err
			}
			if payout, err = s.fillSell(ctx, tx, &cur, pos, price, now); err != nil {
				return err
			}
			amount = cur.Proceeds
		}
		ok, err := tx.FillOrder(ctx, cur)
		if err != nil || !ok {
			return err
		}
		filled = true
		if payout != nil {
			if err := tx.InsertPayout(ctx, *payout); err != nil {
				return err
			}
			if err := s.notifyEarnings(ctx, tx, kid, *payout, cur.Symbol); err != nil {
				return err
			}
		}
		return notify.Send(ctx, tx, now, []string{kid.ID}, notify.OrderFilled, "", notify.Payload{
			KidID: kid.ID, KidName: kid.DisplayName, Amount: amount, Text: cur.Symbol, Note: string(cur.Side),
		})
	})
	return filled, err
}

func (s *Service) cancelInTx(ctx context.Context, tx store.Tx, o store.Order, now time.Time) error {
	if _, err := tx.CancelOrder(ctx, o.ID, now); err != nil {
		return err
	}
	_, err := tx.InsertLedgerEntry(ctx, store.LedgerEntry{ID: store.NewID(), KidID: o.KidID, JarID: o.JarID,
		Amount: o.Amount, Kind: store.KindReversal, Comment: "Cancelled: buy " + o.Symbol, ReversesID: o.LedgerEntryID,
		IdempotencyKey: "order:" + o.ID + ":cancel", EffectiveAt: now, CreatedAt: now})
	return err
}

// Snapshot records each investing kid's portfolio value for today (in the
// household's calendar), at most every five minutes, for the graph.
func (s *Service) Snapshot(ctx context.Context) (int, error) {
	if s.Market == nil {
		return 0, nil
	}
	c := &s.invest
	c.mu.Lock()
	due := c.snapAt.IsZero() || s.now().Sub(c.snapAt) >= snapshotEvery
	if due {
		c.snapAt = s.now()
	}
	c.mu.Unlock()
	if !due {
		return 0, nil
	}
	st, err := s.InvestingStatus(ctx)
	if err != nil || !st.Enabled {
		return 0, err
	}
	h, err := s.Store.GetHousehold(ctx)
	if err != nil {
		return 0, err
	}
	day := s.now().In(h.Location()).Format(time.DateOnly)
	kids, err := s.Store.ListUsers(ctx, store.RoleKid)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, k := range kids {
		p, err := s.Portfolio(ctx, k.ID)
		if err != nil {
			return n, err
		}
		if len(p.Holdings) == 0 {
			continue
		}
		snap := store.Snapshot{KidID: k.ID, Day: day, Value: p.Value, Cost: p.Cost}
		if err := s.Store.Tx(ctx, "", func(tx store.Tx) error { return tx.PutSnapshot(ctx, snap) }); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// InvestHistory is a kid's portfolio value and cost over r, one point a day,
// as graph lines.
func (s *Service) InvestHistory(ctx context.Context, kidID string, r HistoryRange) (BalanceHistory, error) {
	out := BalanceHistory{Range: r, Show: "invest"}
	snaps, err := s.Store.ListSnapshots(ctx, kidID, r.FromDate, r.ToDate)
	if err != nil {
		return out, err
	}
	value := HistorySeries{Key: "value", Name: "Worth now", Kind: store.JarInvest}
	cost := HistorySeries{Key: "cost", Name: "What you paid", Kind: store.JarCustom}
	h, err := s.Store.GetHousehold(ctx)
	if err != nil {
		return out, err
	}
	for i, sn := range snaps {
		at, err := time.ParseInLocation(time.DateOnly, sn.Day, h.Location())
		if err != nil {
			continue
		}
		at = at.Add(16 * time.Hour) // a day's point sits at its close
		for _, x := range []struct {
			sr *HistorySeries
			v  int64
		}{{&value, sn.Value}, {&cost, sn.Cost}} {
			if i == 0 {
				x.sr.Start, x.sr.Low, x.sr.High = x.v, x.v, x.v
			}
			x.sr.Points = append(x.sr.Points, HistoryPoint{At: at, Balance: x.v, Delta: x.v - x.sr.End, Key: sn.Day})
			x.sr.End = x.v
			x.sr.Low, x.sr.High = min(x.sr.Low, x.v), max(x.sr.High, x.v)
		}
	}
	if len(snaps) > 0 {
		out.Series = []HistorySeries{value, cost}
	}
	return out, nil
}

// --- earnings ----------------------------------------------------------------------

// Earnings lists unpaid earnings for every kid (kidID "") or one.
func (s *Service) Earnings(ctx context.Context, kidID string) ([]store.Payout, error) {
	return s.Store.ListPayouts(ctx, kidID, "pending")
}

// PayEarnings records a parent paying a sale's profit: it's credited to the
// jar the sale came from.
func (s *Service) PayEarnings(ctx context.Context, actor store.User, payoutID string) (store.Payout, error) {
	if err := requireAdmin(actor); err != nil {
		return store.Payout{}, err
	}
	p, err := s.Store.GetPayout(ctx, payoutID)
	if err != nil {
		return p, err
	}
	err = s.Store.Tx(ctx, p.KidID, func(tx store.Tx) error {
		now := s.now()
		jar, err := tx.GetJar(ctx, p.JarID)
		if err != nil {
			return err
		}
		e := store.LedgerEntry{ID: store.NewID(), KidID: p.KidID, JarID: p.JarID, Amount: p.Amount, Kind: store.KindInvestGain,
			Comment: "Earnings from selling " + p.Symbol, ActorID: actor.ID, IdempotencyKey: "payout:" + p.ID,
			EffectiveAt: now, CreatedAt: now}
		// The entry goes in first (the payout points at it); a payout that
		// was already paid rolls both back.
		if _, err := tx.InsertLedgerEntry(ctx, e); err != nil {
			return err
		}
		p.PaidBy, p.PaidAt, p.LedgerEntryID, p.Status, p.PaidByName = actor.ID, &now, e.ID, "paid", actor.DisplayName
		ok, err := tx.PayPayout(ctx, p)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAlreadyPaid
		}
		return notify.Send(ctx, tx, now, []string{p.KidID}, notify.EarningsPaid, "", notify.Payload{
			KidID: p.KidID, KidName: p.KidName, ActorName: actor.DisplayName, Amount: p.Amount, Text: p.Symbol, Jar: jar.Name,
		})
	})
	return p, err
}
