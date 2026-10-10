package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func finnhubStub(t *testing.T) *Finnhub {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Finnhub-Token") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Has("token") {
			t.Error("API key sent in the URL")
		}
		switch r.URL.Path {
		case "/quote":
			switch r.URL.Query().Get("symbol") {
			case "AAPL":
				_, _ = w.Write([]byte(`{"c":227.52,"d":1.5,"dp":0.66,"h":228,"l":225,"o":226.01,"pc":226.02,"t":1760011200}`))
			case "SLOW":
				w.WriteHeader(http.StatusTooManyRequests)
			default:
				_, _ = w.Write([]byte(`{"c":0,"d":null,"dp":null,"h":0,"l":0,"o":0,"pc":0,"t":0}`))
			}
		case "/search":
			if r.URL.Query().Get("exchange") != "US" {
				t.Error("search not limited to US")
			}
			_, _ = w.Write([]byte(`{"count":4,"result":[
				{"description":"APPLE INC","displaySymbol":"AAPL","symbol":"AAPL","type":"Common Stock"},
				{"description":"VANGUARD TOTAL STOCK MKT ETF","displaySymbol":"VTI","symbol":"VTI","type":"ETP"},
				{"description":"APPLE INC CALL","displaySymbol":"AAPL 1","symbol":"AAPL 1","type":"Common Stock"},
				{"description":"SOME WARRANT","displaySymbol":"XW","symbol":"XW","type":"Warrant"}]}`))
		case "/stock/market-status":
			_, _ = w.Write([]byte(`{"exchange":"US","holiday":null,"isOpen":true,"session":"regular","t":1760011200,"timezone":"America/New_York"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	f := NewFinnhub("k")
	f.Base = srv.URL
	return f
}

func TestFinnhub(t *testing.T) {
	ctx := context.Background()
	f := finnhubStub(t)

	q, err := f.Quote(ctx, "AAPL")
	if err != nil || q.Price != 227_520_000 || q.Open != 226_010_000 || q.PrevClose != 226_020_000 || q.At.Unix() != 1760011200 {
		t.Fatalf("quote %+v, %v", q, err)
	}
	if _, err := f.Quote(ctx, "NOPE"); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("unknown symbol: %v", err)
	}
	if _, err := f.Quote(ctx, "SLOW"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}

	found, err := f.Search(ctx, "apple")
	if err != nil || len(found) != 2 || found[0] != (Security{"AAPL", "Apple Inc.", Stock}) || found[1].Kind != Fund {
		t.Fatalf("search %+v, %v", found, err)
	}
	if s, err := f.Lookup(ctx, "vti"); err != nil || s.Symbol != "VTI" {
		t.Fatalf("lookup %+v, %v", s, err)
	}
	if _, err := f.Lookup(ctx, "XW"); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("warrant looked up: %v", err)
	}

	st, err := f.Status(ctx)
	if err != nil || !st.Open || st.Session != "regular" {
		t.Fatalf("status %+v, %v", st, err)
	}

	f.Key = "wrong"
	if _, err := f.Status(ctx); !errors.Is(err, ErrBadKey) {
		t.Fatalf("bad key: %v", err)
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"APPLE INC":                    "Apple Inc.",
		"SPDR S&P 500 ETF TRUST":       "SPDR S&P 500 ETF Trust",
		"VANGUARD TOTAL STOCK MKT ETF": "Vanguard Total Stock Mkt ETF",
		"Already Nice Co":              "Already Nice Co.",
	} {
		if got := NiceName(in); got != want {
			t.Errorf("NiceName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, ok := range map[string]bool{"aapl": true, " brk.b ": true, "AAPL 1": false, "": false, "TOOLONGSYMBOL": false} {
		if _, got := NormalizeSymbol(in); got != ok {
			t.Errorf("NormalizeSymbol(%q) ok = %v", in, got)
		}
	}
}
