package api

import (
	"context"
	_ "embed"
	"net/http"
	"strconv"
	"time"

	"github.com/chinny-labs/goldberry/internal/service"
	"github.com/chinny-labs/goldberry/internal/store"
)

//go:embed openapi.json
var openapiJSON []byte

func (a *API) routeAll() {
	a.handle("GET", "/openapi.json", "", func(w http.ResponseWriter, _ *http.Request, _ *principal) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(openapiJSON)
	})
	a.handle("GET", "/me", service.ScopeRead, a.me)
	a.handle("GET", "/household", service.ScopeRead, a.household)
	a.handle("GET", "/kids", service.ScopeRead, a.kids)
	a.handle("GET", "/kids/{kid}", service.ScopeRead, a.kid)
	a.handle("GET", "/kids/{kid}/jars", service.ScopeRead, a.jars)
	a.handle("GET", "/kids/{kid}/ledger", service.ScopeRead, a.ledger)
	a.handle("GET", "/kids/{kid}/goals", service.ScopeRead, a.goals)
	a.handle("GET", "/kids/{kid}/requests", service.ScopeRead, a.kidRequests)
	a.handle("GET", "/requests", service.ScopeRead, a.allRequests)
	a.routeWrites()
}

func (a *API) me(w http.ResponseWriter, _ *http.Request, p *principal) {
	writeJSON(w, http.StatusOK, meDTO{User: user(p.User), TokenID: p.Token.ID, Label: p.Token.Label,
		Scopes: p.Token.Scopes, LastUsed: tsp(p.Token.LastUsedAt)})
}

func (a *API) household(w http.ResponseWriter, r *http.Request, _ *principal) {
	h, cur, err := a.svc.Household(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, household(h, cur))
}

// The read operations below return DTOs and are shared by the HTTP handlers
// and the MCP tools.

func (a *API) listKids(ctx context.Context, includeDisabled bool) (list[kidDTO], error) {
	ks, err := a.svc.KidSummaries(ctx, includeDisabled)
	out := list[kidDTO]{Items: make([]kidDTO, 0, len(ks))}
	for _, k := range ks {
		out.Items = append(out.Items, kidSummary(k))
	}
	return out, err
}

func (a *API) kidView(ctx context.Context, ref string) (service.KidView, error) {
	k, err := a.svc.ResolveKid(ctx, ref)
	if err != nil {
		return service.KidView{}, err
	}
	return a.svc.KidOverview(ctx, k.ID, 0)
}

func (a *API) getKid(ctx context.Context, ref string) (kidDetailDTO, error) {
	v, err := a.kidView(ctx, ref)
	if err != nil {
		return kidDetailDTO{}, err
	}
	d := kidDetailDTO{kidDTO: kidDTO{userDTO: user(v.Kid), Total: v.Total, Held: v.Held, Available: v.Available()},
		Jars: make([]jarDTO, 0, len(v.Jars)), PendingRequests: len(v.Pending)}
	for _, j := range v.Jars {
		d.Jars = append(d.Jars, jar(j))
	}
	return d, nil
}

func (a *API) getJars(ctx context.Context, ref string) (list[jarDTO], error) {
	v, err := a.kidView(ctx, ref)
	if err != nil {
		return list[jarDTO]{}, err
	}
	out := list[jarDTO]{Items: make([]jarDTO, 0, len(v.Jars))}
	for _, j := range v.Jars {
		out.Items = append(out.Items, jar(j))
	}
	return out, nil
}

// ledgerQuery is the filter for a kid's ledger, in wire form.
type ledgerQuery struct {
	Limit int
	JarID string
	From  string
	To    string
}

func (a *API) getLedger(ctx context.Context, ref string, q ledgerQuery) (list[entryDTO], error) {
	k, err := a.svc.ResolveKid(ctx, ref)
	if err != nil {
		return list[entryDTO]{}, err
	}
	if q.Limit == 0 {
		q.Limit = defaultLimit
	}
	if q.Limit < 0 || q.Limit > maxLimit {
		return list[entryDTO]{}, &service.UserError{Field: "limit", Msg: "limit must be a whole number from 1 to " + strconv.Itoa(maxLimit) + "."}
	}
	from, err := parseTime("from", q.From)
	if err != nil {
		return list[entryDTO]{}, err
	}
	to, err := parseTime("to", q.To)
	if err != nil {
		return list[entryDTO]{}, err
	}
	es, err := a.svc.KidLedger(ctx, k.ID, store.LedgerFilter{JarID: q.JarID, From: from, To: to, Limit: q.Limit})
	return list[entryDTO]{Items: entries(es)}, err
}

func (a *API) getGoals(ctx context.Context, ref string) (list[goalDTO], error) {
	k, err := a.svc.ResolveKid(ctx, ref)
	if err != nil {
		return list[goalDTO]{}, err
	}
	gs, err := a.svc.KidGoals(ctx, k.ID)
	out := list[goalDTO]{Items: make([]goalDTO, 0, len(gs))}
	for _, g := range gs {
		out.Items = append(out.Items, goal(g))
	}
	return out, err
}

// getRequests lists requests; an empty ref means every kid's.
func (a *API) getRequests(ctx context.Context, ref, status string, limit int) (list[requestDTO], error) {
	kidID := ""
	if ref != "" {
		k, err := a.svc.ResolveKid(ctx, ref)
		if err != nil {
			return list[requestDTO]{}, err
		}
		kidID = k.ID
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 0 || limit > maxLimit {
		return list[requestDTO]{}, &service.UserError{Field: "limit", Msg: "limit must be a whole number from 1 to " + strconv.Itoa(maxLimit) + "."}
	}
	st := store.RequestStatus(status)
	switch st {
	case "", store.StatusPending, store.StatusApproved, store.StatusDenied, store.StatusCancelled, store.StatusExpired:
	default:
		return list[requestDTO]{}, &service.UserError{Field: "status", Msg: "status must be pending, approved, denied, cancelled or expired."}
	}
	rs, err := a.svc.ListRequests(ctx, store.RequestFilter{KidID: kidID, Status: st, Limit: limit})
	return list[requestDTO]{Items: requests(rs)}, err
}

// parseTime reads an RFC3339 time or a YYYY-MM-DD date (midnight UTC); "" is unset.
func parseTime(name, raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.DateOnly, raw); err == nil {
		return t, nil
	}
	return time.Time{}, &service.UserError{Field: name, Msg: name + " must be an RFC3339 time or a YYYY-MM-DD date."}
}

// reply writes v, or the failure.
func reply[T any](a *API, w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) kids(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.listKids(r.Context(), r.URL.Query().Get("include_disabled") == "true")
	reply(a, w, r, v, err)
}

func (a *API) kid(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.getKid(r.Context(), r.PathValue("kid"))
	reply(a, w, r, v, err)
}

func (a *API) jars(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.getJars(r.Context(), r.PathValue("kid"))
	reply(a, w, r, v, err)
}

// limitParam reads ?limit=: 0 when absent, -1 when malformed.
func limitParam(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
		return n
	}
	return -1
}

func (a *API) ledger(w http.ResponseWriter, r *http.Request, _ *principal) {
	q := r.URL.Query()
	v, err := a.getLedger(r.Context(), r.PathValue("kid"), ledgerQuery{Limit: limitParam(r), JarID: q.Get("jar_id"), From: q.Get("from"), To: q.Get("to")})
	reply(a, w, r, v, err)
}

func (a *API) goals(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.getGoals(r.Context(), r.PathValue("kid"))
	reply(a, w, r, v, err)
}

func (a *API) kidRequests(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.getRequests(r.Context(), r.PathValue("kid"), r.URL.Query().Get("status"), limitParam(r))
	reply(a, w, r, v, err)
}

func (a *API) allRequests(w http.ResponseWriter, r *http.Request, _ *principal) {
	v, err := a.getRequests(r.Context(), "", r.URL.Query().Get("status"), limitParam(r))
	reply(a, w, r, v, err)
}

// kidID resolves the {kid} path value (an ID or a username).
func (a *API) kidID(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	k, err := a.svc.ResolveKid(r.Context(), r.PathValue("kid"))
	if err != nil {
		a.fail(w, r, err)
		return k, false
	}
	return k, true
}

const (
	defaultLimit = 50
	maxLimit     = 500
)
