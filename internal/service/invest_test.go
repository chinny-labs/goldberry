package service

import (
	"testing"
	"time"
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
