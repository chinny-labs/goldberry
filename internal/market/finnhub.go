package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FinnhubURL is Finnhub's REST API.
const FinnhubURL = "https://finnhub.io/api/v1"

// Finnhub reads US quotes from finnhub.io. The free tier allows 60 calls a
// minute, which a household's handful of symbols never comes near; the
// service caches quotes so page views don't each cost a call.
type Finnhub struct {
	Key  string
	Base string // FinnhubURL unless testing
	HTTP *http.Client
}

// NewFinnhub returns a client for the given API key.
func NewFinnhub(key string) *Finnhub {
	return &Finnhub{Key: key, Base: FinnhubURL, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (f *Finnhub) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.Base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	// The key goes in a header, not the URL, so it stays out of proxy logs.
	req.Header.Set("X-Finnhub-Token", f.Key)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("market: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrBadKey
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("market: %s returned %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("market: %s: %w", path, err)
	}
	return nil
}

func micro(f float64) int64 { return int64(math.Round(f * Micro)) }

func (f *Finnhub) Quote(ctx context.Context, symbol string) (Quote, error) {
	var r struct {
		C, O, PC float64
		T        int64
	}
	if err := f.get(ctx, "/quote", url.Values{"symbol": {symbol}}, &r); err != nil {
		return Quote{}, err
	}
	if r.C <= 0 || r.T == 0 { // Finnhub answers an unknown symbol with zeros
		return Quote{}, ErrUnknownSymbol
	}
	return Quote{Symbol: symbol, Price: micro(r.C), Open: micro(r.O), PrevClose: micro(r.PC), At: time.Unix(r.T, 0).UTC()}, nil
}

// finnhubKind maps Finnhub's security types to ours; others aren't tradable here.
func finnhubKind(t string) (Kind, bool) {
	switch t {
	case "Common Stock", "ADR", "REIT":
		return Stock, true
	case "ETP":
		return Fund, true
	}
	return "", false
}

func (f *Finnhub) Search(ctx context.Context, query string) ([]Security, error) {
	var r struct {
		Result []struct {
			Description string
			Symbol      string
			Type        string
		}
	}
	if err := f.get(ctx, "/search", url.Values{"q": {query}, "exchange": {"US"}}, &r); err != nil {
		return nil, err
	}
	var out []Security
	for _, x := range r.Result {
		kind, ok := finnhubKind(x.Type)
		sym, valid := NormalizeSymbol(x.Symbol)
		if !ok || !valid {
			continue
		}
		out = append(out, Security{Symbol: sym, Name: NiceName(x.Description), Kind: kind})
	}
	return out, nil
}

func (f *Finnhub) Lookup(ctx context.Context, symbol string) (Security, error) {
	found, err := f.Search(ctx, symbol)
	if err != nil {
		return Security{}, err
	}
	for _, s := range found {
		if strings.EqualFold(s.Symbol, symbol) {
			return s, nil
		}
	}
	return Security{}, ErrUnknownSymbol
}

func (f *Finnhub) Status(ctx context.Context) (Status, error) {
	var r struct {
		IsOpen  bool
		Session *string
		Holiday *string
	}
	if err := f.get(ctx, "/stock/market-status", url.Values{"exchange": {"US"}}, &r); err != nil {
		return Status{}, err
	}
	st := Status{Open: r.IsOpen}
	if r.Session != nil {
		st.Session = *r.Session
	}
	if r.Holiday != nil {
		st.Holiday = *r.Holiday
	}
	return st, nil
}
