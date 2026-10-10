// Command demo serves Goldberry with a made-up household, "The Riverbanks",
// and three months of history: allowance, chores, requests, goals, locks,
// interest and investing at made-up prices. It drives the real service layer
// on a moved clock, so every row is one the app could have written itself.
// The README screenshots and the GitHub Pages site are taken from it:
//
//	go run ./e2e/demo -listen :18081
//
// Sign in as mom / "correct horse battery", or as ava (PIN 2468) or leo (PIN 1357).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/chinny/goldberry/internal/config"
	"github.com/chinny/goldberry/internal/market"
	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
	"github.com/chinny/goldberry/internal/store/sqlite"
	"github.com/chinny/goldberry/internal/web"
)

const (
	days     = 91
	timezone = "America/New_York"
	password = "correct horse battery"
)

func main() {
	listen := flag.String("listen", ":18081", "address to serve on")
	dir := flag.String("dir", "", "directory for the SQLite file (default: a new temp dir)")
	flag.Parse()
	if err := run(*listen, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

func run(listen, dir string) error {
	ctx := context.Background()
	if dir == "" {
		d, err := os.MkdirTemp("", "goldberry-demo-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(d)
		dir = d
	}
	st, err := sqlite.Open(ctx, filepath.Join(dir, "goldberry.db"), nil)
	if err != nil {
		return err
	}
	defer st.Close()

	log := slog.New(slog.DiscardHandler)
	svc := service.New(st, log)
	prices := newPrices()
	svc.Market = prices.fake

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	if err := seed(ctx, svc, prices, loc); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	svc.Now = time.Now
	prices.fake.SetOpen(true)
	cfg := config.Config{InsecureCookies: true, Timezone: timezone, MCPEnabled: true}
	srv, err := web.New(ctx, svc, cfg, log, "")
	if err != nil {
		return err
	}
	fmt.Printf("demo ready on %s (mom / %q, ava PIN 2468, leo PIN 1357)\n", listen, password)
	hs := &http.Server{Addr: listen, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	return hs.ListenAndServe()
}

// prices is a made-up market: a seeded random walk per symbol, one step a day.
type prices struct {
	fake *market.Fake
	rng  *rand.Rand
	last map[string]int64
	secs []market.Security
}

func newPrices() *prices {
	// Seeded on purpose: the same price moves every run, so retaken screenshots match.
	p := &prices{fake: market.NewFake(), rng: rand.New(rand.NewPCG(7, 11)), last: map[string]int64{}} //nolint:gosec // not for secrets
	start := map[string]float64{
		"VTI": 268, "VOO": 512, "VXUS": 63, "QQQ": 470,
		"AAPL": 214, "DIS": 112, "NKE": 78, "MCD": 291, "NFLX": 640, "RBLX": 46,
	}
	p.secs = []market.Security{
		{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Kind: market.Fund},
		{Symbol: "VOO", Name: "Vanguard S&P 500 ETF", Kind: market.Fund},
		{Symbol: "VXUS", Name: "Vanguard Total International Stock ETF", Kind: market.Fund},
		{Symbol: "QQQ", Name: "Invesco QQQ Trust", Kind: market.Fund},
		{Symbol: "AAPL", Name: "Apple Inc.", Kind: market.Stock},
		{Symbol: "DIS", Name: "Walt Disney Co.", Kind: market.Stock},
		{Symbol: "NKE", Name: "Nike Inc.", Kind: market.Stock},
		{Symbol: "MCD", Name: "McDonald's Corp.", Kind: market.Stock},
		{Symbol: "NFLX", Name: "Netflix Inc.", Kind: market.Stock},
		{Symbol: "RBLX", Name: "Roblox Corp.", Kind: market.Stock},
	}
	for _, s := range p.secs {
		px := int64(start[s.Symbol] * market.Micro)
		p.last[s.Symbol] = px
		p.fake.Add(s, px, px)
	}
	p.fake.SetOpen(true)
	return p
}

// step moves every price by a day: a small upward drift plus noise, a bit
// wilder for single stocks than for funds.
func (p *prices) step() {
	for _, s := range p.secs {
		vol := 0.009
		if s.Kind == market.Stock {
			vol = 0.018
		}
		prev := p.last[s.Symbol]
		next := int64(float64(prev) * (1 + 0.0007 + vol*p.rng.NormFloat64()))
		p.last[s.Symbol] = next
		p.fake.Add(s, next, prev) // today's open is yesterday's close
	}
}

func dollars(d float64) int64 { return int64(d*100 + 0.5) }

func seed(ctx context.Context, svc *service.Service, px *prices, loc *time.Location) error {
	today := time.Now().In(loc)
	first := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days)
	var clock time.Time
	svc.Now = func() time.Time { return clock }
	at := func(day, hour, minute int) {
		clock = first.AddDate(0, 0, day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}

	at(0, 8, 0)
	mom, err := svc.Setup(ctx, service.SetupInput{HouseholdName: "The Riverbanks", Currency: "USD", Timezone: timezone,
		Username: "mom", DisplayName: "Mom", Password: password})
	if err != nil {
		return err
	}
	dad, err := svc.CreateAdmin(ctx, mom, service.AdminInput{Username: "dad", DisplayName: "Dad", Password: password})
	if err != nil {
		return err
	}
	if _, err := svc.SetInvesting(ctx, mom, true); err != nil {
		return err
	}
	ava, err := svc.CreateKid(ctx, mom, service.KidInput{Username: "ava", DisplayName: "Ava", PIN: "2468"})
	if err != nil {
		return err
	}
	leo, err := svc.CreateKid(ctx, mom, service.KidInput{Username: "leo", DisplayName: "Leo", PIN: "1357"})
	if err != nil {
		return err
	}
	avaJars, err := jarIDs(ctx, svc, ava.ID)
	if err != nil {
		return err
	}
	leoJars, err := jarIDs(ctx, svc, leo.ID)
	if err != nil {
		return err
	}
	if _, err := svc.CreateSchedule(ctx, mom, ava.ID, service.ScheduleInput{Amount: dollars(10), Cadence: "weekly", Weekday: 6}); err != nil {
		return err
	}
	if _, err := svc.CreateSchedule(ctx, mom, leo.ID, service.ScheduleInput{Amount: dollars(6), Cadence: "weekly", Weekday: 6}); err != nil {
		return err
	}
	if err := svc.SetInterest(ctx, mom, ava.ID, avaJars["Save"], 100, dollars(5), true); err != nil {
		return err
	}
	if err := svc.SetInterest(ctx, mom, leo.ID, leoJars["Save"], 100, dollars(5), true); err != nil {
		return err
	}
	if _, err := svc.SetLock(ctx, mom, service.LockInput{JarID: avaJars["Give"], Reason: "Give goes out with a parent"}); err != nil {
		return err
	}

	n := 0
	key := func() string { n++; return fmt.Sprintf("demo-%d", n) }
	deposit := func(actor, kid store.User, amount float64, comment string) error {
		_, err := svc.PostEntry(ctx, actor, service.EntryInput{KidID: kid.ID, UseSplit: true, Amount: dollars(amount),
			Comment: comment, IdempotencyKey: key()})
		return err
	}
	ask := func(kid store.User, jar string, amount float64, reason string) (store.WithdrawalRequest, error) {
		return svc.CreateRequest(ctx, kid, service.RequestInput{JarID: jar, Amount: dollars(amount), Reason: reason, IdempotencyKey: key()})
	}
	// decide has kid ask for amount and actor answer: approve (approved, 0
	// for all of it) or deny, with a note.
	decide := func(actor, kid store.User, jar string, amount float64, reason string, approve bool, approved float64, note string) error {
		r, err := ask(kid, jar, amount, reason)
		if err != nil {
			return err
		}
		_, err = svc.DecideRequest(ctx, actor, r.ID, service.Decision{Approve: approve, ApprovedAmount: dollars(approved), Note: note})
		return err
	}
	move := func(kid store.User, from, to string, amount float64) error {
		_, err := svc.Transfer(ctx, kid, service.TransferInput{KidID: kid.ID, FromJarID: from, ToJarID: to, Amount: dollars(amount), IdempotencyKey: key()})
		return err
	}
	// buy spends up to amount of what's in the Invest jar.
	buy := func(kid store.User, symbol string, amount float64) error {
		p, err := svc.Portfolio(ctx, kid.ID)
		if err != nil {
			return err
		}
		cents := min(dollars(amount), p.Jar.Available())
		if cents < service.MinOrder {
			return nil
		}
		_, err = svc.PlaceOrder(ctx, kid, service.OrderInput{Symbol: symbol, Side: store.SideBuy, Amount: cents, IdempotencyKey: key()})
		return err
	}
	sellAll := func(kid store.User, symbol string) error {
		_, err := svc.PlaceOrder(ctx, kid, service.OrderInput{Symbol: symbol, Side: store.SideSell, All: true, IdempotencyKey: key()})
		return err
	}

	// What happens on each day, at the hour it happens.
	events := map[int]func() error{
		1: func() error {
			at(1, 18, 5)
			if err := deposit(mom, ava, 60, "Birthday money from Grandma"); err != nil {
				return err
			}
			_, err := svc.CreateGoal(ctx, ava, ava.ID, service.GoalInput{Name: "Nintendo Switch", Emoji: "🎮", Target: dollars(300), Priority: 1})
			return err
		},
		2: func() error {
			at(2, 16, 40)
			_, err := svc.CreateGoal(ctx, leo, leo.ID, service.GoalInput{Name: "Soccer cleats", Emoji: "⚽", Target: dollars(45), Priority: 1})
			return err
		},
		4: func() error {
			at(4, 15, 30)
			if err := buy(ava, "VTI", 10); err != nil {
				return err
			}
			at(4, 19, 10)
			return deposit(dad, leo, 5, "Tooth fairy")
		},
		6: func() error {
			at(6, 11, 0)
			return deposit(dad, ava, 15, "Mowed the lawn")
		},
		9: func() error {
			at(9, 17, 20)
			if _, err := svc.SetLock(ctx, ava, service.LockInput{JarID: avaJars["Save"], Reason: "Saving for a Switch"}); err != nil {
				return err
			}
			return decide(mom, ava, avaJars["Spend"], 6, "Ice cream with Jess", true, 0, "")
		},
		13: func() error {
			at(13, 12, 15)
			return deposit(mom, leo, 8, "Helped with the groceries")
		},
		16: func() error {
			at(16, 16, 0)
			return decide(dad, leo, leoJars["Spend"], 9, "Pokémon cards", false, 0, "Let's wait for the weekend")
		},
		18: func() error {
			at(18, 15, 45)
			return buy(ava, "AAPL", 12)
		},
		20: func() error {
			at(20, 10, 30)
			return deposit(mom, ava, 20, "Babysat Leo")
		},
		23: func() error {
			at(23, 18, 0)
			return move(ava, avaJars["Spend"], avaJars["Save"], 25)
		},
		27: func() error {
			at(27, 9, 40)
			if err := deposit(mom, ava, 15, "Mowed the lawn"); err != nil {
				return err
			}
			at(27, 9, 41) // a slip of the finger, undone a minute later
			es, err := svc.PostEntry(ctx, mom, service.EntryInput{KidID: leo.ID, UseSplit: true, Amount: dollars(50),
				Comment: "Raked the leaves", PrivateNote: "meant $5", IdempotencyKey: key()})
			if err != nil {
				return err
			}
			at(27, 9, 43)
			if _, err := svc.Reverse(ctx, mom, es[0].ID); err != nil {
				return err
			}
			return deposit(mom, leo, 5, "Raked the leaves")
		},
		31: func() error {
			at(31, 17, 10)
			return decide(mom, ava, avaJars["Spend"], 14, "Book fair", true, 10, "$10 is plenty for two books")
		},
		34: func() error {
			at(34, 15, 35)
			return buy(leo, "DIS", 8)
		},
		38: func() error {
			at(38, 13, 0)
			return deposit(dad, ava, 40, "Report card bonus")
		},
		41: func() error {
			at(41, 16, 20)
			return buy(ava, "VTI", 15)
		},
		45: func() error {
			at(45, 18, 30)
			return deposit(dad, leo, 10, "Washed the car")
		},
		48: func() error {
			at(48, 11, 15)
			return decide(dad, leo, leoJars["Spend"], 12, "Robux", true, 0, "")
		},
		52: func() error {
			at(52, 15, 50)
			return buy(ava, "RBLX", 8)
		},
		55: func() error {
			at(55, 19, 0)
			return deposit(mom, ava, 25, "Dog-sitting the Parks' dog")
		},
		58: func() error {
			at(58, 17, 45)
			return move(ava, avaJars["Spend"], avaJars["Save"], 30)
		},
		62: func() error {
			at(62, 10, 0)
			if err := deposit(mom, leo, 15, "Uncle Ben's visit"); err != nil {
				return err
			}
			// Give is parent-locked: it goes out with a parent.
			_, err := svc.PostEntry(ctx, mom, service.EntryInput{KidID: ava.ID, JarID: avaJars["Give"], Amount: dollars(10), Remove: true,
				Comment: "Animal shelter donation, together", IdempotencyKey: key()})
			return err
		},
		66: func() error {
			at(66, 14, 30)
			return deposit(dad, ava, 15, "Mowed the lawn")
		},
		70: func() error {
			at(70, 15, 55)
			return sellAll(ava, "AAPL")
		},
		72: func() error {
			at(72, 9, 0)
			p, err := svc.Portfolio(ctx, ava.ID)
			if err != nil {
				return err
			}
			for _, o := range p.Owed {
				if _, err := svc.PayEarnings(ctx, mom, o.ID); err != nil {
					return err
				}
			}
			return nil
		},
		75: func() error {
			at(75, 16, 30)
			return buy(ava, "QQQ", 15)
		},
		79: func() error {
			at(79, 12, 0)
			if err := deposit(mom, leo, 6, "Folded the laundry"); err != nil {
				return err
			}
			at(79, 15, 20)
			return buy(leo, "NKE", 10)
		},
		83: func() error {
			at(83, 18, 15)
			if err := deposit(dad, ava, 20, "Raked the whole yard"); err != nil {
				return err
			}
			_, err := svc.CreateGoal(ctx, ava, ava.ID, service.GoalInput{Name: "LEGO set", Emoji: "🧱", Target: dollars(50), Priority: 2})
			return err
		},
		86: func() error {
			at(86, 15, 40)
			if err := buy(ava, "VTI", 10); err != nil {
				return err
			}
			_, _, err := svc.CreateAPIToken(ctx, mom, service.TokenInput{Label: "Home Assistant", Scopes: []string{service.ScopeRead}})
			return err
		},
		88: func() error {
			at(88, 15, 0)
			if err := sellAll(ava, "RBLX"); err != nil {
				return err
			}
			_, _, err := svc.CreateAPIToken(ctx, mom, service.TokenInput{Label: "Claude", Scopes: []string{service.ScopeRead, service.ScopeWrite, service.ScopeMoney}})
			return err
		},
	}

	for d := 0; d <= days; d++ {
		if d > 0 {
			px.step()
		}
		if ev, ok := events[d]; ok {
			if err := ev(); err != nil {
				return fmt.Errorf("day %d: %w", d, err)
			}
		}
		// The scheduler's jobs, once late in the day (after the close, so
		// the snapshot is the day's last price).
		at(d, 21, 0)
		if d == days {
			clock = time.Now()
		}
		if _, err := svc.PostAllowance(ctx); err != nil {
			return err
		}
		if _, err := svc.PostInterest(ctx); err != nil {
			return err
		}
		if _, err := svc.CheckGoals(ctx); err != nil {
			return err
		}
		if _, err := svc.FillQueued(ctx); err != nil {
			return err
		}
		if _, err := svc.Snapshot(ctx); err != nil {
			return err
		}
	}

	// Everything so far has been seen; what's left is today's news.
	for _, u := range []store.User{mom, dad, ava, leo} {
		if err := svc.MarkRead(ctx, u); err != nil {
			return err
		}
	}
	clock = time.Now().Add(-40 * time.Minute)
	if _, err := ask(ava, avaJars["Spend"], 8, "Movie with Jess"); err != nil {
		return err
	}
	clock = time.Now().Add(-12 * time.Minute)
	if _, err := ask(leo, leoJars["Spend"], 15, "New soccer ball"); err != nil {
		return err
	}
	return nil
}

// jarIDs maps a kid's jar names to IDs.
func jarIDs(ctx context.Context, svc *service.Service, kidID string) (map[string]string, error) {
	v, err := svc.KidOverview(ctx, kidID, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, j := range v.Jars {
		out[j.Name] = j.ID
	}
	return out, nil
}
