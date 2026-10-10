// Package api is Goldberry's JSON API (ADR 0015). Like the web handlers it
// calls the service layer and never the store, so every money rule lives in
// one place. Callers authenticate with bearer tokens (service.AuthenticateToken)
// and act as the admin who minted the token.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

// Prefix is where the API is mounted.
const Prefix = "/api/v1"

// Options configure the API. Zero values are usable.
type Options struct {
	// ClientIP names the caller's address for failed-token throttling
	// (the web server's trusted-proxy aware version). Default: RemoteAddr.
	ClientIP func(*http.Request) string
	Log      *slog.Logger
}

// API is the /api/v1 handler.
type API struct {
	svc      *service.Service
	log      *slog.Logger
	clientIP func(*http.Request) string
	lim      *limiter
	mux      *http.ServeMux
	routes   []Route
}

// Route is one registered operation, for the spec drift test.
type Route struct{ Method, Path, Scope string }

// New builds the API handler, to be mounted at Prefix + "/".
func New(svc *service.Service, opt Options) *API {
	a := &API{svc: svc, log: opt.Log, clientIP: opt.ClientIP, mux: http.NewServeMux()}
	if a.log == nil {
		a.log = slog.New(slog.DiscardHandler)
	}
	if a.clientIP == nil {
		a.clientIP = func(r *http.Request) string { return r.RemoteAddr }
	}
	a.lim = newLimiter(func() time.Time { return svc.Now() })
	a.routeAll()
	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No such endpoint.", "")
	})
	return a
}

// Routes lists the operations the API serves.
func (a *API) Routes() []Route { return a.routes }

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a.mux.ServeHTTP(w, r)
}

// scope "" means no token is needed.
func (a *API) handle(method, path, scope string, h func(http.ResponseWriter, *http.Request, *principal)) {
	a.routes = append(a.routes, Route{method, path, scope})
	a.mux.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
		if scope == "" {
			h(w, r, nil)
			return
		}
		p, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		if !service.HasScope(p.Token, scope) {
			writeError(w, http.StatusForbidden, "insufficient_scope", "This token needs the “"+scope+"” scope.", "")
			return
		}
		if ok, wait := a.lim.allow(p.Token.ID); !ok {
			tooMany(w, wait)
			return
		}
		h(w, r, p)
	})
}

// principal is who a request acts as.
type principal struct {
	User  store.User
	Token store.APIToken
}

func (a *API) authenticate(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	ip := a.clientIP(r)
	if blocked, wait := a.lim.blocked(ip); blocked {
		tooMany(w, wait)
		return nil, false
	}
	secret, ok := bearer(r)
	if !ok {
		unauthorized(w, "Send your token as “Authorization: Bearer gb_…”.")
		return nil, false
	}
	u, t, err := a.svc.AuthenticateToken(r.Context(), secret)
	if errors.Is(err, service.ErrNotFound) {
		a.lim.fail(ip)
		unauthorized(w, "That token isn't valid. It may have been revoked.")
		return nil, false
	}
	if err != nil {
		a.serverError(w, r, err)
		return nil, false
	}
	return &principal{User: u, Token: t}, true
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	return strings.TrimSpace(h[7:]), true
}

// --- responses ----------------------------------------------------------------

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
		Detail  any    `json:"detail,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg, field string) {
	writeErrorDetail(w, status, code, msg, field, nil)
}

func writeErrorDetail(w http.ResponseWriter, status int, code, msg, field string, detail any) {
	var b errorBody
	b.Error.Code, b.Error.Message, b.Error.Field, b.Error.Detail = code, msg, field, detail
	writeJSON(w, status, b)
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="goldberry"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", msg, "")
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait/time.Second) + 1
	w.Header().Set("Retry-After", itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again in "+itoa(secs)+" seconds.", "")
}

func (a *API) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("api request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. It's been logged.", "")
}

// fail maps a service error to a response. Expected errors carry their
// message; anything else is a 500 with no detail.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ue  *service.UserError
		ins *service.ErrInsufficient
		dec *service.ErrAlreadyDecided
	)
	switch {
	case errors.As(err, &ue):
		writeError(w, http.StatusUnprocessableEntity, "invalid", ue.Msg, ue.Field)
	case errors.As(err, &ins):
		writeErrorDetail(w, http.StatusUnprocessableEntity, "insufficient_funds", ins.Error(), "amount",
			map[string]any{"available": ins.Available, "jar": ins.Jar})
	case errors.As(err, &dec):
		writeError(w, http.StatusConflict, "already_decided", dec.Error(), "")
	case errors.Is(err, service.ErrCannotReverse), errors.Is(err, service.ErrCannotReverseMove):
		writeError(w, http.StatusConflict, "conflict", err.Error(), "")
	case errors.Is(err, service.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "You can't do that.", "")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Not found.", "")
	case errors.Is(err, context.Canceled):
		// The client went away; nothing to say.
	default:
		a.serverError(w, r, err)
	}
}
