package market

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Holding is one thing a fund owns.
type Holding struct {
	Name   string
	Weight int64 // basis points of the fund: 650 is 6.50%
}

// FundHoldings is what a fund owns, biggest first.
type FundHoldings struct {
	Top   []Holding
	Count int       // how many holdings the fund has in all
	AsOf  time.Time // the date the fund reported them
}

// HoldingsProvider is a source of fund contents. It is separate from Provider
// because the price source (Finnhub) only sells holdings on a paid plan.
type HoldingsProvider interface {
	// Holdings returns the top n holdings of a fund, or ErrNoHoldings.
	Holdings(ctx context.Context, symbol string, n int) (FundHoldings, error)
}

// ErrNoHoldings means the source has nothing for that fund.
var ErrNoHoldings = errors.New("market: no holdings for that fund")

// SEC reads fund holdings from the monthly N-PORT reports every US fund files
// with the SEC. They're public and free, but each report is a few hundred KB
// to several MB of XML and funds file them with a lag (the report is for a
// month end, filed up to two months later), so callers cache the result.
type SEC struct {
	Base      string // "https://www.sec.gov" unless testing
	UserAgent string // SEC rejects requests with no contact in the User-Agent
	HTTP      *http.Client

	mu     sync.Mutex
	series map[string]seriesRef // by ticker
	loaded time.Time
}

type seriesRef struct {
	CIK    int64
	Series string
}

// SECURL is where the SEC serves EDGAR.
const SECURL = "https://www.sec.gov"

// NewSEC returns a client. contact is an email address; it goes in the
// User-Agent, as SEC's fair access policy asks (it answers 403 without one).
func NewSEC(contact string) *SEC {
	return &SEC{Base: SECURL, UserAgent: "goldberry " + contact, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

const maxReport = 32 << 20 // the biggest bond index funds file ~10 MB

func (s *SEC) get(ctx context.Context, path string, q url.Values) (io.ReadCloser, error) {
	u := s.Base + path
	if q != nil {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("market: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNoHoldings
	case resp.StatusCode != http.StatusOK:
		resp.Body.Close()
		return nil, fmt.Errorf("market: sec %s returned %s", path, resp.Status)
	}
	return resp.Body, nil
}

// lookup maps a ticker to its fund series, from SEC's ticker list (refreshed daily).
func (s *SEC) lookup(ctx context.Context, symbol string) (seriesRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.series == nil || time.Since(s.loaded) > 24*time.Hour {
		body, err := s.get(ctx, "/files/company_tickers_mf.json", nil)
		if err != nil {
			return seriesRef{}, err
		}
		defer body.Close()
		var r struct{ Data [][]any }
		if err := json.NewDecoder(io.LimitReader(body, maxReport)).Decode(&r); err != nil {
			return seriesRef{}, fmt.Errorf("market: sec tickers: %w", err)
		}
		m := make(map[string]seriesRef, len(r.Data))
		for _, row := range r.Data { // cik, seriesId, classId, symbol
			cik, ok1 := row[0].(float64)
			ser, ok2 := row[1].(string)
			sym, ok3 := row[3].(string)
			if len(row) == 4 && ok1 && ok2 && ok3 {
				m[strings.ToUpper(sym)] = seriesRef{int64(cik), ser}
			}
		}
		s.series, s.loaded = m, time.Now()
	}
	ref, ok := s.series[symbol]
	if !ok {
		return seriesRef{}, ErrNoHoldings
	}
	return ref, nil
}

// latestReport finds the accession number of a series' newest N-PORT.
func (s *SEC) latestReport(ctx context.Context, ref seriesRef) (string, error) {
	body, err := s.get(ctx, "/cgi-bin/browse-edgar", url.Values{
		"action": {"getcompany"}, "CIK": {ref.Series}, "type": {"NPORT-P"}, "count": {"1"}, "output": {"atom"}})
	if err != nil {
		return "", err
	}
	defer body.Close()
	var feed struct {
		Entries []struct {
			Accession string `xml:"content>accession-number"`
		} `xml:"entry"`
	}
	dec := xml.NewDecoder(io.LimitReader(body, 1<<20))
	dec.CharsetReader = latin1 // EDGAR's filing feeds declare ISO-8859-1
	if err := dec.Decode(&feed); err != nil {
		return "", fmt.Errorf("market: sec filings: %w", err)
	}
	if len(feed.Entries) == 0 || feed.Entries[0].Accession == "" {
		return "", ErrNoHoldings
	}
	return feed.Entries[0].Accession, nil
}

func (s *SEC) Holdings(ctx context.Context, symbol string, n int) (FundHoldings, error) {
	ref, err := s.lookup(ctx, symbol)
	if err != nil {
		return FundHoldings{}, err
	}
	acc, err := s.latestReport(ctx, ref)
	if err != nil {
		return FundHoldings{}, err
	}
	body, err := s.get(ctx, fmt.Sprintf("/Archives/edgar/data/%d/%s/primary_doc.xml", ref.CIK, strings.ReplaceAll(acc, "-", "")), nil)
	if err != nil {
		return FundHoldings{}, err
	}
	defer body.Close()
	return parseNPort(io.LimitReader(body, maxReport), ref.Series, n)
}

// parseNPort reads an N-PORT report, checking it belongs to the series (a
// trust's filings cover many funds) and keeping the n biggest long positions.
func parseNPort(r io.Reader, series string, n int) (FundHoldings, error) {
	var doc struct {
		Series string `xml:"formData>genInfo>seriesId"`
		AsOf   string `xml:"formData>genInfo>repPdDate"`
		Items  []struct {
			Name   string  `xml:"name"`
			Title  string  `xml:"title"`
			Pct    float64 `xml:"pctVal"`
			Payoff string  `xml:"payoffProfile"`
		} `xml:"formData>invstOrSecs>invstOrSec"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return FundHoldings{}, fmt.Errorf("market: sec report: %w", err)
	}
	if doc.Series != series || len(doc.Items) == 0 {
		return FundHoldings{}, ErrNoHoldings
	}
	// A fund can own many lots of one issuer (a hundred Treasury notes); show it once.
	var all []Holding
	at := map[string]int{}
	for _, it := range doc.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" || name == "N/A" {
			name = strings.TrimSpace(it.Title)
		}
		if name == "" || it.Pct <= 0 || it.Payoff == "Short" {
			continue
		}
		bp := int64(math.Round(it.Pct * 100))
		if i, ok := at[name]; ok {
			all[i].Weight += bp
			continue
		}
		at[name] = len(all)
		all = append(all, Holding{Name: NiceName(name), Weight: bp})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Weight > all[j].Weight })
	out := FundHoldings{Count: len(all)}
	if len(all) > n {
		all = all[:n]
	}
	out.Top = all
	out.AsOf, _ = time.Parse("2006-01-02", doc.AsOf)
	if len(out.Top) == 0 {
		return FundHoldings{}, ErrNoHoldings
	}
	return out, nil
}

// latin1 reads ISO-8859-1 (and plain ASCII) as UTF-8: every byte is the code
// point of the same number.
func latin1(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "us-ascii":
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(string(bytesToRunes(b))), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}

func bytesToRunes(b []byte) []rune {
	out := make([]rune, len(b))
	for i, c := range b {
		out[i] = rune(c)
	}
	return out
}
