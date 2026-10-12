package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chinny-labs/goldberry/internal/service"
)

const (
	maxBody      = 1 << 20
	maxKeyLen    = 128
	keyNamespace = "api:" // keeps client keys clear of the ones Goldberry makes ("reverse:…", "request:…")
)

func (a *API) routeWrites() {
	a.handle("POST", "/kids/{kid}/jars", service.ScopeWrite, a.jarAdd)
	a.handle("PUT", "/jars/{id}", service.ScopeWrite, a.jarRename)
	a.handle("POST", "/jars/{id}/archive", service.ScopeWrite, a.jarArchive)
	a.handle("PUT", "/kids/{kid}/split", service.ScopeWrite, a.splitSet)
	a.handle("POST", "/kids/{kid}/goals", service.ScopeWrite, a.goalAdd)
	a.handle("PUT", "/goals/{id}", service.ScopeWrite, a.goalUpdate)
	a.handle("POST", "/goals/{id}/archive", service.ScopeWrite, a.goalArchive)

	a.handle("POST", "/kids/{kid}/entries", service.ScopeMoney, a.entryPost)
	a.handle("POST", "/entries:batch", service.ScopeMoney, a.entryBatch)
	a.handle("POST", "/entries/{id}/reverse", service.ScopeMoney, a.entryReverse)
	a.handle("POST", "/kids/{kid}/transfers", service.ScopeMoney, a.transferPost)
	a.handle("POST", "/requests/{id}/decision", service.ScopeMoney, a.requestDecide)
}

// decode reads a JSON body strictly: unknown fields and trailing data are errors.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after the JSON body")
	}
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		writeError(w, http.StatusBadRequest, "bad_request", "Send a JSON body.", "")
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "The body is over 1 MB.", "")
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "Bad JSON: "+err.Error()+".", "")
	}
	return false
}

// cleanKey checks a client's idempotency key and namespaces it.
func cleanKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return "", &service.UserError{Field: "idempotency_key", Msg: "Send an idempotency key (the Idempotency-Key header), so a retry can't post twice."}
	case len(key) > maxKeyLen:
		return "", &service.UserError{Field: "idempotency_key", Msg: "Keep the idempotency key under 128 characters."}
	}
	for _, c := range key {
		if c < ' ' || c > '~' {
			return "", &service.UserError{Field: "idempotency_key", Msg: "Use printable ASCII in the idempotency key."}
		}
	}
	return keyNamespace + key, nil
}

// headerKey reads the Idempotency-Key header, or the body's field.
func headerKey(r *http.Request, body string) (string, error) {
	h := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if h != "" && body != "" && h != strings.TrimSpace(body) {
		return "", &service.UserError{Field: "idempotency_key", Msg: "The header and the body field disagree."}
	}
	if h == "" {
		h = body
	}
	return cleanKey(h)
}

// --- jars and goals (write) ---------------------------------------------------

type nameIn struct {
	Name string `json:"name"`
}

func (a *API) jarOut(w http.ResponseWriter, r *http.Request, status int, id string) {
	j, err := a.svc.JarByID(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, status, jar(j))
}

func (a *API) jarAdd(w http.ResponseWriter, r *http.Request, p *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	var in nameIn
	if !decode(w, r, &in) {
		return
	}
	j, err := a.svc.AddJar(r.Context(), p.User, k.ID, in.Name)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.jarOut(w, r, http.StatusCreated, j.ID)
}

func (a *API) jarRename(w http.ResponseWriter, r *http.Request, p *principal) {
	var in nameIn
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := a.svc.RenameJar(r.Context(), p.User, id, in.Name); err != nil {
		a.fail(w, r, err)
		return
	}
	a.jarOut(w, r, http.StatusOK, id)
}

func (a *API) jarArchive(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if err := a.svc.ArchiveJar(r.Context(), p.User, id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.jarOut(w, r, http.StatusOK, id)
}

type splitIn struct {
	Shares map[string]int `json:"shares"` // jar ID → basis points; must total 10000
}

func (a *API) splitSet(w http.ResponseWriter, r *http.Request, p *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	var in splitIn
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.SetSplit(r.Context(), p.User, k.ID, in.Shares); err != nil {
		a.fail(w, r, err)
		return
	}
	a.jars(w, r, p)
}

type goalIn struct {
	Name     string `json:"name"`
	Emoji    string `json:"emoji"`
	Target   int64  `json:"target"`
	JarID    string `json:"jar_id"`
	Priority int    `json:"priority"`
}

func (in goalIn) input() service.GoalInput {
	return service.GoalInput{Name: in.Name, Emoji: in.Emoji, Target: in.Target, JarID: in.JarID, Priority: in.Priority}
}

func (a *API) goalOut(w http.ResponseWriter, r *http.Request, status int, id string) {
	g, err := a.svc.GoalByID(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, status, goal(g))
}

func (a *API) goalAdd(w http.ResponseWriter, r *http.Request, p *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	var in goalIn
	if !decode(w, r, &in) {
		return
	}
	g, err := a.svc.CreateGoal(r.Context(), p.User, k.ID, in.input())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.goalOut(w, r, http.StatusCreated, g.ID)
}

// goalUpdate replaces a goal's editable fields (PUT, not PATCH): send them all.
func (a *API) goalUpdate(w http.ResponseWriter, r *http.Request, p *principal) {
	var in goalIn
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := a.svc.UpdateGoal(r.Context(), p.User, id, in.input()); err != nil {
		a.fail(w, r, err)
		return
	}
	a.goalOut(w, r, http.StatusOK, id)
}

func (a *API) goalArchive(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if err := a.svc.ArchiveGoal(r.Context(), p.User, id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.goalOut(w, r, http.StatusOK, id)
}

// --- money (money scope) ------------------------------------------------------

type entryIn struct {
	Kid            string `json:"kid"` // batch items only: an ID or username
	Direction      string `json:"direction"`
	Amount         int64  `json:"amount"`
	JarID          string `json:"jar_id"`
	UseSplit       bool   `json:"use_split"`
	Comment        string `json:"comment"`
	PrivateNote    string `json:"private_note"`
	IdempotencyKey string `json:"idempotency_key"`
	EffectiveAt    string `json:"effective_at"` // RFC3339 or YYYY-MM-DD; "" = now
}

// input turns the wire form into a service input for kidID. loc reads a bare
// effective_at date (the household's time zone).
func (in entryIn) input(kidID, key string, loc *time.Location) (service.EntryInput, error) {
	var remove bool
	switch in.Direction {
	case "", "add":
	case "remove":
		remove = true
	default:
		return service.EntryInput{}, &service.UserError{Field: "direction", Msg: "direction must be add or remove."}
	}
	at, err := parseEffective(in.EffectiveAt, loc)
	if err != nil {
		return service.EntryInput{}, err
	}
	return service.EntryInput{KidID: kidID, JarID: in.JarID, UseSplit: in.UseSplit, Amount: in.Amount, Remove: remove,
		Comment: in.Comment, PrivateNote: in.PrivateNote, IdempotencyKey: key, EffectiveAt: at}, nil
}

// parseEffective reads an RFC3339 time, or a YYYY-MM-DD date at midnight in
// loc; "" is unset (now).
func parseEffective(raw string, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, raw, loc); err == nil {
		return t, nil
	}
	return time.Time{}, &service.UserError{Field: "effective_at", Msg: "effective_at must be an RFC3339 time or a YYYY-MM-DD date."}
}

// householdLoc is the household's time zone, for reading bare dates.
func (a *API) householdLoc(ctx context.Context) (*time.Location, error) {
	h, _, err := a.svc.Household(ctx)
	if err != nil {
		return nil, err
	}
	return h.Location(), nil
}

type entriesOut struct {
	Entries  []entryDTO `json:"entries"`
	Replayed bool       `json:"replayed"`
}

func (a *API) entryPost(w http.ResponseWriter, r *http.Request, p *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	var in entryIn
	if !decode(w, r, &in) {
		return
	}
	key, err := headerKey(r, in.IdempotencyKey)
	var ei service.EntryInput
	if err == nil {
		var loc *time.Location
		if loc, err = a.householdLoc(r.Context()); err == nil {
			ei, err = in.input(k.ID, key, loc)
		}
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	res, err := a.svc.PostEntries(r.Context(), p.User, []service.EntryInput{ei}, dry)
	if err != nil {
		a.fail(w, r, unwrapBatch(err))
		return
	}
	status := http.StatusCreated
	if res[0].Replayed || dry {
		status = http.StatusOK
	}
	writeJSON(w, status, entriesOut{Entries: entries(res[0].Entries), Replayed: res[0].Replayed})
}

func unwrapBatch(err error) error {
	var be *service.BatchError
	if errors.As(err, &be) {
		return be.Err
	}
	return err
}

type batchIn struct {
	Entries []entryIn `json:"entries"`
}

type batchOut struct {
	DryRun  bool         `json:"dry_run"`
	Results []entriesOut `json:"results"`
}

func (a *API) entryBatch(w http.ResponseWriter, r *http.Request, p *principal) {
	var in batchIn
	if !decode(w, r, &in) {
		return
	}
	loc, err := a.householdLoc(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	items := make([]service.EntryInput, 0, len(in.Entries))
	for i, e := range in.Entries {
		fail := func(err error) { a.fail(w, r, &service.BatchError{Index: i, Err: err}) }
		k, err := a.svc.ResolveKid(r.Context(), e.Kid)
		if err != nil {
			fail(err)
			return
		}
		key, err := cleanKey(e.IdempotencyKey)
		var ei service.EntryInput
		if err == nil {
			ei, err = e.input(k.ID, key, loc)
		}
		if err != nil {
			fail(err)
			return
		}
		items = append(items, ei)
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	res, err := a.svc.PostEntries(r.Context(), p.User, items, dry)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := batchOut{DryRun: dry, Results: make([]entriesOut, 0, len(res))}
	for _, x := range res {
		out.Results = append(out.Results, entriesOut{Entries: entries(x.Entries), Replayed: x.Replayed})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) entryReverse(w http.ResponseWriter, r *http.Request, p *principal) {
	es, err := a.svc.Reverse(r.Context(), p.User, r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, entriesOut{Entries: entries(es)})
}

type transferIn struct {
	FromJarID      string `json:"from_jar_id"`
	ToJarID        string `json:"to_jar_id"`
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (a *API) transferPost(w http.ResponseWriter, r *http.Request, p *principal) {
	k, ok := a.kidID(w, r)
	if !ok {
		return
	}
	var in transferIn
	if !decode(w, r, &in) {
		return
	}
	key, err := headerKey(r, in.IdempotencyKey)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	es, replay, err := a.svc.TransferReplay(r.Context(), p.User, service.TransferInput{
		KidID: k.ID, FromJarID: in.FromJarID, ToJarID: in.ToJarID, Amount: in.Amount, IdempotencyKey: key})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	writeJSON(w, status, entriesOut{Entries: entries(es), Replayed: replay})
}

type decisionIn struct {
	Decision       string `json:"decision"` // approve | deny
	ApprovedAmount int64  `json:"approved_amount"`
	Note           string `json:"note"`
}

func (a *API) requestDecide(w http.ResponseWriter, r *http.Request, p *principal) {
	var in decisionIn
	if !decode(w, r, &in) {
		return
	}
	if in.Decision != "approve" && in.Decision != "deny" {
		a.fail(w, r, &service.UserError{Field: "decision", Msg: "decision must be approve or deny."})
		return
	}
	req, err := a.svc.DecideRequest(r.Context(), p.User, r.PathValue("id"), service.Decision{
		Approve: in.Decision == "approve", ApprovedAmount: in.ApprovedAmount, Note: in.Note})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, request(req))
}
