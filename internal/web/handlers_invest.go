package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chinny/goldberry/internal/market"
	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

// investData is the investing page: a kid's own, or a parent's read-only view.
type investData struct {
	Kid      store.User
	P        service.Portfolio
	Chart    *chart
	Ranges   []graphLink
	Trick    string
	Funds    []pick
	Stocks   []pick
	Base     string
	ReadOnly bool               // a parent looking: no buy or sell
	Inside   map[string]*inside // what each owned fund holds, by symbol
}

func (s *Server) kidInvest(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	s.renderInvest(w, r, *c.User, "/invest", "/", false)
}

func (s *Server) adminInvest(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	kv, ok := s.loadKid(w, r)
	if !ok {
		return
	}
	s.renderInvest(w, r, kv.Kid, "/admin/kids/"+kv.Kid.ID+"/invest", "/admin/kids/"+kv.Kid.ID, true)
}

func (s *Server) renderInvest(w http.ResponseWriter, r *http.Request, kid store.User, base, back string, readOnly bool) {
	ctx, c := r.Context(), rc(r)
	p, err := s.svc.Portfolio(ctx, kid.ID)
	if err != nil {
		s.fail(w, r, back, err)
		return
	}
	d := investData{Kid: kid, P: p, Trick: trickOfTheDay(time.Now().In(c.House.Location())), Funds: starterFunds,
		Stocks: starterStocks, Base: base, ReadOnly: readOnly, Inside: map[string]*inside{}}
	for _, h := range p.Holdings {
		if in := s.fundInside(ctx, h.Kind, h.Symbol); in != nil {
			d.Inside[h.Symbol] = in
		}
	}
	preset := r.URL.Query().Get("range")
	rng, err := s.svc.ResolveRange(ctx, preset, "", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	h, err := s.svc.InvestHistory(ctx, kid.ID, rng)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(h.Series) > 0 && len(h.Series[0].Points) > 1 {
		ch := buildChart(h, c.Cur, c.House.Location(), false)
		for i := range ch.Lines {
			ch.Lines[i].Dots, ch.Lines[i].Area = nil, "" // a point a day: the line says it
		}
		ch.Lines[0].Class = "worth" // solid, against the dashed "what you paid"
		d.Chart = &ch
		for _, pr := range service.RangePresets {
			d.Ranges = append(d.Ranges, graphLink{Label: pr.Label, On: pr.Key == rng.Preset,
				Href: base + "?" + url.Values{"range": {pr.Key}}.Encode() + "#growth"})
		}
	}
	title := "Invest"
	if readOnly {
		title = kid.DisplayName + "’s investments"
	}
	v := s.newView(w, r, title, d)
	v.Nav, v.Back = "invest", back
	if readOnly {
		v.Nav = "kid:" + kid.ID
	}
	s.render(w, r, http.StatusOK, "invest", v)
}

// --- one stock or fund --------------------------------------------------------------

type stockData struct {
	Sec      store.Security
	Quote    store.Quote
	HasQuote bool
	Stale    bool
	Holding  service.Holding
	Owns     bool
	P        service.Portfolio
	Key      string
	Inside   *inside // what a fund holds; nil for stocks or when unknown
}

// inside is a fund's biggest holdings, ready to show.
type inside struct {
	Rows  []insideRow
	More  int    // holdings not listed
	AsOf  string // "June 30, 2026", or ""
	Total int
}

type insideRow struct{ Name, Pct string }

// fundInside looks up what a fund holds. It returns nil when the symbol is a
// stock, or nothing is known, so templates can skip the section.
func (s *Server) fundInside(ctx context.Context, kind, symbol string) *inside {
	if kind != "fund" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	h, ok := s.svc.FundHoldings(ctx, symbol)
	if !ok {
		return nil
	}
	in := &inside{Total: h.Count, More: h.Count - len(h.Top)}
	for _, x := range h.Top {
		in.Rows = append(in.Rows, insideRow{Name: x.Name, Pct: fmt.Sprintf("%d.%02d%%", x.Weight/100, x.Weight%100)})
	}
	if !h.AsOf.IsZero() {
		in.AsOf = h.AsOf.Format("January 2, 2006")
	}
	return in
}

// Change is today's move per share, in micro-dollars.
func (d stockData) Change() int64 {
	if d.Quote.PrevClose == 0 {
		return 0
	}
	return d.Quote.Price - d.Quote.PrevClose
}

// ChangeCents is today's move per share, rounded to the cent.
func (d stockData) ChangeCents() int64 {
	c := d.Change()
	if c < 0 {
		return -((-c + 5_000) / 10_000)
	}
	return (c + 5_000) / 10_000
}

// ChangePct is today's move as a percentage: "+0.53%", "−1.20%".
func (d stockData) ChangePct() string {
	if d.Quote.PrevClose == 0 {
		return "0%"
	}
	bp := d.Change() * 10_000 / d.Quote.PrevClose
	sign := "+"
	if bp < 0 {
		sign, bp = "−", -bp
	}
	return fmt.Sprintf("%s%d.%02d%%", sign, bp/100, bp%100)
}

func (s *Server) kidStock(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	s.renderStock(w, r, c, r.PathValue("symbol"), http.StatusOK, "")
}

func (s *Server) renderStock(w http.ResponseWriter, r *http.Request, c *reqCtx, symbol string, status int, errMsg string) {
	ctx := r.Context()
	sec, err := s.svc.Security(ctx, symbol)
	if errors.Is(err, service.ErrUnknownSymbol) {
		s.renderStatus(w, r, http.StatusNotFound, "Couldn’t find that", "There’s no US stock or fund with that symbol. Try searching by name.")
		return
	}
	if err != nil {
		s.fail(w, r, "/invest", err)
		return
	}
	p, err := s.svc.Portfolio(ctx, c.User.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := stockData{Sec: sec, P: p, Key: store.NewID(), Inside: s.fundInside(ctx, sec.Kind, sec.Symbol)}
	if q, stale, err := s.svc.Quote(ctx, sec.Symbol); err == nil {
		d.Quote, d.HasQuote, d.Stale = q, true, stale
	}
	d.Holding, d.Owns = p.Holding(sec.Symbol)
	v := s.newView(w, r, sec.Name, d)
	v.Eyebrow, v.Nav, v.Back, v.Err = sec.Symbol, "invest", "/invest", errMsg
	if r.Method == http.MethodPost {
		v.Form = r.PostForm
	}
	s.render(w, r, status, "stock", v)
}

func (s *Server) kidOrderSubmit(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	f := r.PostForm
	in := service.OrderInput{Symbol: f.Get("symbol"), Side: store.OrderSide(f.Get("side")), All: f.Get("all") == "1",
		IdempotencyKey: f.Get("key")}
	if !in.All {
		amount, err := c.Cur.Parse(f.Get("amount"))
		if err != nil {
			s.renderStock(w, r, c, in.Symbol, http.StatusUnprocessableEntity, "How much? "+err.Error()+".")
			return
		}
		in.Amount = amount
	}
	o, err := s.svc.PlaceOrder(r.Context(), *c.User, in)
	if err != nil {
		if msg := userMessage(err); msg != "" {
			s.renderStock(w, r, c, in.Symbol, http.StatusUnprocessableEntity, msg)
			return
		}
		s.fail(w, r, "/invest", err)
		return
	}
	v := &view{reqCtx: c}
	var msg string
	switch {
	case o.Status == store.OrderQueued && o.Side == store.SideBuy:
		msg = "The market’s closed, so your order to buy " + c.Cur.Format(o.Amount) + " of " + o.Symbol + " is waiting. It goes through when the market opens."
	case o.Status == store.OrderQueued:
		msg = "The market’s closed, so your order to sell " + o.Symbol + " is waiting. It goes through when the market opens."
	case o.Side == store.SideBuy:
		msg = "You bought " + v.Shares(o.Shares) + " " + v.ShareWord(o.Shares) + " of " + o.Symbol + " for " + c.Cur.Format(o.Amount) + "!"
	default:
		msg = "You sold " + v.Shares(o.Shares) + " " + v.ShareWord(o.Shares) + " of " + o.Symbol + " for " + c.Cur.Format(o.Proceeds) + "."
		if gain := o.Proceeds - o.CostBasis; gain > 0 {
			msg += " You made " + c.Cur.Format(gain) + "! A grown-up will pay it into your jar."
		} else if gain < 0 {
			msg += " That’s " + c.Cur.Format(-gain) + " less than you paid."
		}
	}
	s.flash(w, "ok", msg)
	http.Redirect(w, r, "/invest", http.StatusSeeOther)
}

func (s *Server) kidOrderCancel(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	s.cancelOrder(w, r, c, "/invest")
}

func (s *Server) adminOrderCancel(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	s.cancelOrder(w, r, c, "/admin")
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request, c *reqCtx, def string) {
	if err := s.svc.CancelOrder(r.Context(), *c.User, r.PathValue("id")); err != nil {
		s.fail(w, r, def, err)
		return
	}
	s.back(w, r, def, "ok", "Order cancelled. The money is back in the Invest jar.")
}

// --- search and learning ------------------------------------------------------------

type findData struct {
	Query   string
	Results []market.Security
	Funds   []pick
	Stocks  []pick
}

func (s *Server) kidFind(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	d := findData{Query: q, Funds: starterFunds, Stocks: starterStocks}
	errMsg := ""
	if q != "" {
		// A symbol typed exactly goes straight to its page.
		if sym, ok := market.NormalizeSymbol(q); ok && !strings.Contains(q, " ") {
			if _, err := s.svc.Security(r.Context(), sym); err == nil {
				http.Redirect(w, r, "/invest/stocks/"+url.PathEscape(sym), http.StatusSeeOther)
				return
			}
		}
		res, err := s.svc.Search(r.Context(), q)
		if err != nil {
			if errMsg = userMessage(err); errMsg == "" {
				s.serverError(w, r, err)
				return
			}
		}
		d.Results = res
	}
	v := s.newView(w, r, "Find a stock or fund", d)
	v.Nav, v.Back, v.Err = "invest", "/invest", errMsg
	s.render(w, r, http.StatusOK, "invest_find", v)
}

type learnData struct {
	Tips   []tip
	Tricks []string
}

func (s *Server) investLearn(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	v := s.newView(w, r, "Investing words", learnData{Tips: tipList, Tricks: tricks})
	v.Nav, v.Back = "invest", "/invest"
	if c.User.IsAdmin() {
		v.Back, v.Nav = "/admin/settings", "settings"
	}
	s.render(w, r, http.StatusOK, "invest_learn", v)
}

// --- parents -------------------------------------------------------------------------

func (s *Server) adminInvestingSubmit(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	on := r.PostFormValue("on") == "1"
	changed, err := s.svc.SetInvesting(r.Context(), *c.User, on)
	if err != nil {
		s.fail(w, r, "/admin/settings", err)
		return
	}
	msg := "Investing is off. Holdings stay where they are, and waiting orders were cancelled."
	if on {
		msg = "Investing is on. Every kid has an Invest jar."
		if changed > 0 {
			msg += " Kids on the standard split now get 50% Spend, 20% Save, 10% Give and 20% Invest; change it on each kid’s Jars page."
		}
	}
	s.back(w, r, "/admin/settings", "ok", msg)
}

func (s *Server) adminPayEarnings(w http.ResponseWriter, r *http.Request, c *reqCtx) {
	p, err := s.svc.PayEarnings(r.Context(), *c.User, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, "/admin", err)
		return
	}
	s.back(w, r, "/admin/kids/"+p.KidID, "ok", "Paid "+c.Cur.Format(p.Amount)+" of "+p.KidName+"’s earnings.")
}
