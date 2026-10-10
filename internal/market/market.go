// Package market is where real stock and fund prices come from (plan §7.6).
// Provider is the seam; Finnhub is the one implementation, and Fake stands in
// for it in tests. Prices are int64 micro-dollars, so floats stop at the edge
// of this package.
package market

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Micro is the price scale: a price of 1_000_000 is one US dollar.
const Micro = 1_000_000

// Kind is what a symbol is, in kid terms.
type Kind string

const (
	Stock Kind = "stock" // one company
	Fund  Kind = "fund"  // an ETF: a basket of many companies
)

// Security is a tradable symbol.
type Security struct {
	Symbol string
	Name   string
	Kind   Kind
}

// Quote is a symbol's latest price.
type Quote struct {
	Symbol    string
	Price     int64 // micro-dollars
	Open      int64 // today's opening price (0 before the open)
	PrevClose int64
	At        time.Time
}

// Status is whether the US market is open right now.
type Status struct {
	Open    bool
	Session string // "pre-market", "regular", "post-market" or ""
	Holiday string
}

var (
	ErrUnknownSymbol = errors.New("market: unknown symbol")
	ErrRateLimited   = errors.New("market: too many requests; try again in a minute")
	ErrBadKey        = errors.New("market: the API key was refused")
)

// Provider is a source of prices.
type Provider interface {
	Quote(ctx context.Context, symbol string) (Quote, error)
	// Lookup finds one exact symbol.
	Lookup(ctx context.Context, symbol string) (Security, error)
	// Search finds stocks and funds by symbol or name.
	Search(ctx context.Context, query string) ([]Security, error)
	Status(ctx context.Context) (Status, error)
}

// NormalizeSymbol upper-cases and trims a symbol, and reports whether it
// looks like a US ticker ("AAPL", "BRK.B").
func NormalizeSymbol(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || len(s) > 10 {
		return s, false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '-' {
			return s, false
		}
	}
	return s, true
}

// NiceName tidies an all-caps listing name: "APPLE INC" → "Apple Inc.",
// "SPDR S&P 500 ETF TRUST" → "SPDR S&P 500 ETF Trust".
func NiceName(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		up := strings.ToUpper(w)
		if keep, ok := acronyms[up]; ok {
			words[i] = keep
			continue
		}
		if strings.ContainsAny(w, "0123456789") || w != up {
			continue
		}
		words[i] = up[:1] + strings.ToLower(up[1:])
	}
	return strings.Join(words, " ")
}

var acronyms = map[string]string{
	"ETF": "ETF", "S&P": "S&P", "SPDR": "SPDR", "US": "US", "USA": "USA", "INC": "Inc.", "CORP": "Corp.",
	"CO": "Co.", "LTD": "Ltd.", "PLC": "plc", "NV": "N.V.", "SA": "S.A.", "AG": "AG", "ADR": "ADR",
	"QQQ": "QQQ", "REIT": "REIT", "MSCI": "MSCI", "ESG": "ESG", "II": "II", "III": "III", "&": "&",
}
