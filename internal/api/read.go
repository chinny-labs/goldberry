package api

import (
	_ "embed"
	"net/http"
	"strconv"
	"time"

	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

//go:embed openapi.json
var openapiJSON []byte

const (
	defaultLimit = 50
	maxLimit     = 500
)

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

func (a *API) kids(w http.ResponseWriter, r *http.Request, _ *principal) {
	all := r.URL.Query().Get("include_disabled") == "true"
	ks, err := a.svc.KidSummaries(r.Context(), all)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := list[kidDTO]{Items: make([]kidDTO, 0, len(ks))}
	for _, k := range ks {
		out.Items = append(out.Items, kidSummary(k))
	}
	writeJSON(w, http.StatusOK, out)
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

func (a *API) kid(w http.ResponseWriter, r *http.Request, _ *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	v, err := a.svc.KidOverview(r.Context(), k.ID, 0)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d := kidDetailDTO{kidDTO: kidDTO{userDTO: user(v.Kid), Total: v.Total, Held: v.Held, Available: v.Available()},
		Jars: make([]jarDTO, 0, len(v.Jars)), PendingRequests: len(v.Pending)}
	for _, j := range v.Jars {
		d.Jars = append(d.Jars, jar(j))
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) jars(w http.ResponseWriter, r *http.Request, _ *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	v, err := a.svc.KidOverview(r.Context(), k.ID, 0)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := list[jarDTO]{Items: make([]jarDTO, 0, len(v.Jars))}
	for _, j := range v.Jars {
		out.Items = append(out.Items, jar(j))
	}
	writeJSON(w, http.StatusOK, out)
}

// limitParam reads ?limit=, defaulting and capping it.
func limitParam(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, &service.UserError{Field: "limit", Msg: "limit must be a whole number from 1 to " + strconv.Itoa(maxLimit) + "."}
	}
	return min(n, maxLimit), nil
}

// timeParam reads an RFC3339 time or a YYYY-MM-DD date (midnight UTC).
func timeParam(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
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

func (a *API) ledger(w http.ResponseWriter, r *http.Request, _ *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	limit, err := limitParam(r)
	var from, to time.Time
	if err == nil {
		from, err = timeParam(r, "from")
	}
	if err == nil {
		to, err = timeParam(r, "to")
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	es, err := a.svc.KidLedger(r.Context(), k.ID, store.LedgerFilter{JarID: r.URL.Query().Get("jar_id"), From: from, To: to, Limit: limit})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list[entryDTO]{Items: entries(es)})
}

func (a *API) goals(w http.ResponseWriter, r *http.Request, _ *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	gs, err := a.svc.KidGoals(r.Context(), k.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := list[goalDTO]{Items: make([]goalDTO, 0, len(gs))}
	for _, g := range gs {
		out.Items = append(out.Items, goal(g))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) requestList(w http.ResponseWriter, r *http.Request, kidID string) {
	limit, err := limitParam(r)
	status := store.RequestStatus(r.URL.Query().Get("status"))
	switch status {
	case "", store.StatusPending, store.StatusApproved, store.StatusDenied, store.StatusCancelled, store.StatusExpired:
	default:
		err = &service.UserError{Field: "status", Msg: "status must be pending, approved, denied, cancelled or expired."}
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rs, err := a.svc.ListRequests(r.Context(), store.RequestFilter{KidID: kidID, Status: status, Limit: limit})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list[requestDTO]{Items: requests(rs)})
}

func (a *API) kidRequests(w http.ResponseWriter, r *http.Request, _ *principal) {
	if k, ok := a.kidID(w, r); ok {
		a.requestList(w, r, k.ID)
	}
}

func (a *API) allRequests(w http.ResponseWriter, r *http.Request, _ *principal) {
	a.requestList(w, r, "")
}
