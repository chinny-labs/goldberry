package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/chinny/goldberry/internal/market"
)

func TestShareMath(t *testing.T) {
	const dollar = 1_000_000
	for _, tc := range []struct {
		amount, price, shares, value int64
	}{
		{1000, 200 * dollar, 50_000, 1000},                       // $10 at $200: 0.05 share
		{100, 3 * dollar, 333_333, 99},                           // $1 at $3: floors to 0.333333, worth 99¢
		{100_000_000, 10 * dollar, 100_000_000_000, 100_000_000}, // $1M of a $10 stock: no overflow
		{100, 50_000 * dollar, 20, 100},                          // a pricey share
	} {
		if got := SharesFor(tc.amount, tc.price); got != tc.shares {
			t.Errorf("SharesFor(%d, %d) = %d, want %d", tc.amount, tc.price, got, tc.shares)
		}
		if got := ValueOf(tc.shares, tc.price); got != tc.value {
			t.Errorf("ValueOf(%d, %d) = %d, want %d", tc.shares, tc.price, got, tc.value)
		}
	}
	// sharesWorth rounds up so a "sell $1" really raises $1.
	if s := sharesWorth(100, 3*dollar); ValueOf(s, 3*dollar) < 100 || ValueOf(s-1, 3*dollar) >= 100 {
		t.Errorf("sharesWorth(100, $3) = %d", s)
	}
}

func TestMarketOpenAt(t *testing.T) {
	// 13:00 UTC on a summer day is 09:00 in New York (EDT): today's open is 13:30 UTC.
	got := marketOpenAt(time.Date(2026, 7, 6, 13, 0, 0, 0, time.UTC)).UTC()
	if want := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("summer open %s, want %s", got, want)
	}
	// In winter (EST) it's 14:30 UTC.
	got = marketOpenAt(time.Date(2026, 1, 6, 20, 0, 0, 0, time.UTC)).UTC()
	if want := time.Date(2026, 1, 6, 14, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("winter open %s, want %s", got, want)
	}
}

// countingHoldings counts calls and can be made to fail.
type countingHoldings struct {
	calls int
	err   error
}

func (c *countingHoldings) Holdings(context.Context, string, int) (market.FundHoldings, error) {
	c.calls++
	if c.err != nil {
		return market.FundHoldings{}, c.err
	}
	return market.FundHoldings{Count: 2, Top: []market.Holding{{Name: "Apple Inc.", Weight: 650}}}, nil
}

func TestFundHoldingsCache(t *testing.T) {
	src := &countingHoldings{}
	now := time.Now()
	s := &Service{Holdings: src, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}

	if h, ok := s.FundHoldings(context.Background(), "VTI"); !ok || h.Top[0].Name != "Apple Inc." {
		t.Fatalf("first = %+v, %v", h, ok)
	}
	s.FundHoldings(context.Background(), "VTI")
	if src.calls != 1 {
		t.Errorf("a second look should come from the cache: %d calls", src.calls)
	}

	// A day later the source is down: the old copy is still shown, and not hammered.
	now = now.Add(25 * time.Hour)
	src.err = errors.New("down")
	if _, ok := s.FundHoldings(context.Background(), "VTI"); !ok || src.calls != 2 {
		t.Errorf("stale copy should be kept: ok=%v calls=%d", ok, src.calls)
	}
	s.FundHoldings(context.Background(), "VTI")
	if src.calls != 2 {
		t.Errorf("a failed fetch should back off: %d calls", src.calls)
	}

	// A fund with nothing known stays quiet.
	src.err = market.ErrNoHoldings
	if _, ok := s.FundHoldings(context.Background(), "ZZZ"); ok {
		t.Error("unknown fund reported holdings")
	}
	s.Holdings = nil
	if _, ok := s.FundHoldings(context.Background(), "VTI"); ok {
		t.Error("no source should report nothing")
	}
}
