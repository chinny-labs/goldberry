package web

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/chinny/goldberry/internal/market"
	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

func TestInvesting(t *testing.T) {
	ts, svc := newServerWithService(t)
	ctx := context.Background()
	mom, err := svc.Setup(ctx, service.SetupInput{HouseholdName: "Demo", Currency: "USD", Timezone: "UTC",
		Username: "mom", DisplayName: "Mom", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	ava, err := svc.CreateKid(ctx, mom, service.KidInput{Username: "ava", DisplayName: "Ava", PIN: "2468"})
	if err != nil {
		t.Fatal(err)
	}

	parent := newBrowser(t, ts)
	parent.get("/login", 200)
	parent.post("/login", url.Values{"username": {"mom"}, "secret": {"correct horse battery"}}, 200)
	kid := newBrowser(t, ts)
	kid.get("/login", 200)
	kid.post("/login", url.Values{"username": {"ava"}, "secret": {"2468"}}, 200)

	// No price feed: settings say how to get one, and kids see no Invest tab.
	parent.get("/admin/settings", 200)
	parent.mustSee("GOLDBERRY_FINNHUB_API_KEY")
	kid.get("/", 200)
	kid.mustNotSee(`href="/invest"`)
	kid.get("/invest/stocks/AAPL", 200) // back to /invest, which explains
	kid.mustSee("turned on; a parent can turn it on in Settings", "Ask a grown-up to turn investing on")

	fake := market.NewFake()
	fake.Add(market.Security{Symbol: "AAPL", Name: "Apple Inc.", Kind: market.Stock}, 200*market.Micro, 198*market.Micro)
	fake.Add(market.Security{Symbol: "VTI", Name: "Vanguard Total Stock Market ETF", Kind: market.Fund}, 300*market.Micro, 300*market.Micro)
	fake.SetOpen(true)
	svc.Market = fake

	parent.get("/admin/settings", 200)
	parent.post("/admin/settings/investing", url.Values{"on": {"1"}}, 200)
	parent.mustSee("Investing is on", "50% Spend")
	// $40 split 50/20/10/20 puts $8 in Invest.
	parent.get("/admin/kids/"+ava.ID+"/funds", 200)
	parent.post("/admin/kids/"+ava.ID+"/entries", url.Values{"key": {"d1"}, "direction": {"add"}, "amount": {"40"}, "jar": {"split"}}, 200)

	kid.get("/", 200)
	kid.mustSee(`href="/invest"`, "Invest it", "$8.00")
	kid.get("/invest", 200)
	kid.mustSee("Ready to invest", "$8.00", "The market is open", "Tip of the day", "Good places to start", `class="tip"`, "What is the Invest jar?")
	kid.get("/invest/find?q=apple", 200)
	kid.mustSee("Apple Inc.", `href="/invest/stocks/AAPL"`)
	kid.get("/invest/find?q=vti", 200) // an exact symbol goes straight to its page
	kid.mustSee("Vanguard Total Stock Market ETF", "Fund", "$300.00")
	kid.get("/invest/stocks/NOPE", 404)
	kid.get("/invest/learn", 200)
	kid.mustSee("Investing words", `id="diversify"`, "Tricks good investors use")

	// Buy $5 of AAPL at $200: 0.025 of a share.
	kid.get("/invest/stocks/AAPL", 200)
	kid.mustSee("Apple Inc.", "$200.00", "up $2.00", "1.01%) today", "Buy AAPL")
	key := find(t, `name="key" value="([^"]+)"`, kid.last)
	kid.post("/invest/orders", url.Values{"key": {key}, "symbol": {"AAPL"}, "side": {"buy"}, "amount": {"5"}}, 200)
	kid.mustSee("You bought 0.025 shares of AAPL for $5.00!", "My investments", "$3.00")
	kid.get("/invest/stocks/AAPL", 200)
	kid.post("/invest/orders", url.Values{"key": {"k2"}, "symbol": {"AAPL"}, "side": {"buy"}, "amount": {"50"}}, 422)
	kid.mustSee("Only $3.00 is available in Invest.")
	kid.post("/invest/orders", url.Values{"key": {"k3"}, "symbol": {"AAPL"}, "side": {"buy"}, "amount": {"lots"}}, 422)

	// AAPL jumps to $280; she sells it all for $7: $5 back now, $2 owed.
	fake.Set("AAPL", 280*market.Micro)
	svc.Now = func() time.Time { return time.Now().Add(5 * time.Minute) } // past the quote cache
	kid.get("/invest/stocks/AAPL", 200)
	kid.mustSee("You own 0.025 shares", "worth $7.00", "Sell all")
	kid.post("/invest/orders", url.Values{"key": {"s1"}, "symbol": {"AAPL"}, "side": {"sell"}, "all": {"1"}}, 200)
	kid.mustSee("You sold 0.025 shares of AAPL for $7.00.", "You made $2.00!", "A grown-up will pay it into your jar.")

	// Kids can't pay themselves; Mom sees it on the dashboard, the kid page and the bell.
	payouts, err := svc.Earnings(ctx, ava.ID)
	if err != nil || len(payouts) != 1 {
		t.Fatalf("earnings %v, %v", payouts, err)
	}
	kid.post("/admin/payouts/"+payouts[0].ID+"/pay", url.Values{}, 403)
	parent.get("/admin", 200)
	parent.mustSee("Investing earnings to pay", "Pay $2.00")
	parent.get("/notifications", 200)
	parent.mustSee("Ava earned $2.00 investing")
	parent.get("/admin/kids/"+ava.ID, 200)
	parent.mustSee("Investing", "You owe Ava $2.00")
	parent.post("/admin/payouts/"+payouts[0].ID+"/pay", url.Values{"next": {"/admin/kids/" + ava.ID}}, 200)
	parent.mustSee("Paid $2.00 of Ava’s earnings.", "Earnings from selling AAPL")
	parent.post("/admin/payouts/"+payouts[0].ID+"/pay", url.Values{}, 200)
	parent.mustSee("Those earnings were already paid.")
	kid.get("/invest", 200)
	kid.mustSee("$10.00", "Earnings paid by Mom")

	// After hours a buy waits; the kid can cancel it.
	fake.SetOpen(false)
	svc.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	fake.SetHoldings("VTI", market.FundHoldings{Count: 3500, AsOf: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Top: []market.Holding{{Name: "Apple Inc.", Weight: 650}, {Name: "Microsoft Corp.", Weight: 525}}})
	svc.Holdings = fake
	kid.get("/invest/stocks/VTI", 200)
	kid.mustSee("The market is closed", "What’s inside", "Apple Inc.", "6.50%", "5.25%", "and 3498 more", "June 30, 2026")
	kid.get("/invest/stocks/AAPL", 200)
	kid.mustNotSee("What’s inside") // only funds are baskets
	kid.get("/invest/stocks/VTI", 200)
	kid.post("/invest/orders", url.Values{"key": {"q1"}, "symbol": {"VTI"}, "side": {"buy"}, "amount": {"4"}}, 200)
	kid.mustSee("is waiting", "Waiting for the market", "Buy $4.00 of VTI")
	orders, err := svc.Store.ListOrders(ctx, store.OrderFilter{KidID: ava.ID, Status: store.OrderQueued})
	if err != nil || len(orders) != 1 {
		t.Fatalf("queued %v, %v", orders, err)
	}
	parent.get("/admin/kids/"+ava.ID+"/invest", 200)
	parent.mustSee("Ava’s investments", "Buy $4.00 of VTI", "/admin/orders/"+orders[0].ID+"/cancel")
	parent.mustNotSee("Find something to buy") // a parent's view is read-only
	parent.get("/admin/kids/"+ava.ID, 200)
	parent.mustSee("1 order waiting for the market.")
	kid.post("/invest/orders/"+orders[0].ID+"/cancel", url.Values{}, 200)
	kid.mustSee("Order cancelled.")

	// Turning investing off hides the tab.
	parent.get("/admin/settings", 200)
	parent.post("/admin/settings/investing", url.Values{"on": {"0"}}, 200)
	parent.mustSee("Investing is off")
	kid.get("/", 200)
	kid.mustNotSee(`href="/invest"`)
}
