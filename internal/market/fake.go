package market

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Provider for tests and demos. Set prices with Set and
// open or close the market with SetOpen.
type Fake struct {
	mu     sync.Mutex
	open   bool
	quotes map[string]Quote
	secs   map[string]Security
	Calls  int // Quote calls, to check caching
	Err    error
}

// NewFake returns a closed market with no symbols.
func NewFake() *Fake {
	return &Fake{quotes: map[string]Quote{}, secs: map[string]Security{}}
}

// Add lists a symbol at a price in micro-dollars; open is today's open.
func (f *Fake) Add(sec Security, price, open int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secs[sec.Symbol] = sec
	f.quotes[sec.Symbol] = Quote{Symbol: sec.Symbol, Price: price, Open: open, PrevClose: open, At: time.Now().UTC()}
}

// Set moves a symbol's price.
func (f *Fake) Set(symbol string, price int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.quotes[symbol]
	q.Price, q.At = price, time.Now().UTC()
	f.quotes[symbol] = q
}

// SetOpen opens or closes the market.
func (f *Fake) SetOpen(open bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open = open
}

func (f *Fake) Quote(_ context.Context, symbol string) (Quote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return Quote{}, f.Err
	}
	q, ok := f.quotes[symbol]
	if !ok {
		return Quote{}, ErrUnknownSymbol
	}
	return q, nil
}

func (f *Fake) Lookup(_ context.Context, symbol string) (Security, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.secs[symbol]; ok {
		return s, nil
	}
	return Security{}, ErrUnknownSymbol
}

func (f *Fake) Search(_ context.Context, query string) ([]Security, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := strings.ToLower(query)
	var out []Security
	for _, s := range f.secs {
		if strings.Contains(strings.ToLower(s.Symbol+" "+s.Name), q) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *Fake) Status(context.Context) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return Status{}, f.Err
	}
	if f.open {
		return Status{Open: true, Session: "regular"}, nil
	}
	return Status{Session: "post-market"}, nil
}
