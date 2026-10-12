package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chinny-labs/goldberry/internal/api"
	"github.com/chinny-labs/goldberry/internal/service"
	"github.com/chinny-labs/goldberry/internal/store"
	"github.com/chinny-labs/goldberry/internal/store/sqlite"
	"github.com/chinny-labs/goldberry/internal/store/storetest"
)

// env is a household behind a running API.
type env struct {
	t      *testing.T
	f      *storetest.Fixture
	api    *api.API
	srv    *httptest.Server
	secret map[string]string // by scope set, e.g. "read", "money"
	// tick moves the clock on after every call, so a long scenario stays
	// under the per-token rate limit.
	tick bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := storetest.NewFixture(t, func(t *testing.T) store.Store {
		st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "gb.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	})
	e := &env{t: t, f: f, secret: map[string]string{}}
	e.api = api.New(f.Svc, api.Options{})
	mux := http.NewServeMux()
	mux.Handle(api.Prefix+"/", e.api)
	mux.Handle(api.MCPPath, e.api.MCPHandler())
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	e.mint("read")
	e.mint("write", "write")
	e.mint("money", "money")
	e.mint("all", "write", "money")
	return e
}

func (e *env) mint(name string, scopes ...string) {
	e.t.Helper()
	s, _, err := e.f.Svc.CreateAPIToken(context.Background(), e.f.Mom, service.TokenInput{Label: name, Scopes: scopes})
	if err != nil {
		e.t.Fatal(err)
	}
	e.secret[name] = s
}

// call sends a request as the named token ("" = none) and decodes the JSON reply into out.
func (e *env) call(token, method, path string, body any, out any) int {
	e.t.Helper()
	return e.callH(token, method, path, body, nil, out)
}

func (e *env) callH(token, method, path string, body any, hdr map[string]string, out any) int {
	e.t.Helper()
	if e.tick {
		defer e.f.Clock.Advance(200 * time.Millisecond)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+api.Prefix+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+e.secret[token])
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: bad JSON %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

type apiErr struct {
	Error struct {
		Code, Message, Field string
		Detail               map[string]any
	}
}

func (e *env) want(status int, got int, what string) {
	e.t.Helper()
	if got != status {
		e.t.Fatalf("%s: status %d, want %d", what, got, status)
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t)

	var er apiErr
	e.want(401, e.call("", "GET", "/kids", nil, &er), "no token")
	if er.Error.Code != "unauthorized" {
		t.Fatalf("error %+v", er)
	}
	e.secret["junk"] = "gb_not-a-real-token"
	e.want(401, e.call("junk", "GET", "/kids", nil, nil), "unknown token")
	e.secret["noprefix"] = "hello"
	e.want(401, e.call("noprefix", "GET", "/kids", nil, nil), "no prefix")
	e.want(404, e.call("read", "GET", "/nope", nil, &er), "unknown route")
	if er.Error.Code != "not_found" {
		t.Fatalf("404 body %+v", er)
	}

	// The spec is public.
	var spec map[string]any
	e.want(200, e.call("", "GET", "/openapi.json", nil, &spec), "spec")

	var me struct {
		User   struct{ Username string }
		Label  string
		Scopes []string
	}
	e.want(200, e.call("all", "GET", "/me", nil, &me), "me")
	if me.User.Username != "mom" || me.Label != "all" || strings.Join(me.Scopes, ",") != "read,write,money" {
		t.Fatalf("me %+v", me)
	}

	// Revoking, or disabling the owner, cuts a token off at once.
	toks, _ := e.f.Svc.APITokens(context.Background(), e.f.Mom, e.f.Mom.ID)
	for _, tk := range toks {
		if tk.Label == "read" {
			if err := e.f.Svc.RevokeAPIToken(context.Background(), e.f.Mom, tk.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.want(401, e.call("read", "GET", "/kids", nil, nil), "revoked token")
}

func TestFailedTokenThrottle(t *testing.T) {
	e := newEnv(t)
	e.secret["junk"] = "gb_nope"
	for i := 0; i < 10; i++ {
		e.want(401, e.call("junk", "GET", "/kids", nil, nil), "bad token")
	}
	// The address is now blocked, even for a good token, until the window passes.
	var er apiErr
	e.want(429, e.call("read", "GET", "/kids", nil, &er), "blocked")
	if er.Error.Code != "rate_limited" {
		t.Fatalf("429 body %+v", er)
	}
	e.f.Clock.Advance(2 * time.Minute)
	e.want(200, e.call("read", "GET", "/kids", nil, nil), "after the window")
}

func TestTokenRateLimit(t *testing.T) {
	e := newEnv(t)
	limited := 0
	for i := 0; i < 80; i++ {
		if e.call("read", "GET", "/me", nil, nil) == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("80 back-to-back requests were never limited")
	}
	// Another token has its own bucket.
	e.want(200, e.call("all", "GET", "/me", nil, nil), "other token")
	e.f.Clock.Advance(10 * time.Second)
	e.want(200, e.call("read", "GET", "/me", nil, nil), "refilled")
}

func TestReads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.f

	f.Deposit(t, f.Ava, 2500)
	f.Clock.Advance(time.Hour)
	f.Deposit(t, f.Ava, 500)
	f.Deposit(t, f.Leo, 100)
	if _, err := f.Svc.CreateGoal(ctx, f.Mom, f.Ava.ID, service.GoalInput{Name: "Lego", Target: 4000}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.CreateRequest(ctx, f.Ava, service.RequestInput{Amount: 300, Reason: "snack", IdempotencyKey: "r1"}); err != nil {
		t.Fatal(err)
	}

	var hh struct {
		Name     string
		Currency struct {
			Code     string
			Exponent int
		}
		Timezone string
	}
	e.want(200, e.call("read", "GET", "/household", nil, &hh), "household")
	if hh.Name != "The Chins" || hh.Currency.Code != "USD" || hh.Currency.Exponent != 2 {
		t.Fatalf("household %+v", hh)
	}

	var kids struct {
		Items []struct {
			ID, Username           string
			Total, Held, Available int64
			Disabled               bool
		}
	}
	e.want(200, e.call("read", "GET", "/kids", nil, &kids), "kids")
	if len(kids.Items) != 2 || kids.Items[0].Username != "ava" {
		t.Fatalf("kids %+v", kids.Items)
	}
	if k := kids.Items[0]; k.Total != 3000 || k.Held != 300 || k.Available != 2700 {
		t.Fatalf("ava totals %+v", k)
	}

	// A kid is addressable by ID or username, in any case.
	for _, ref := range []string{f.Ava.ID, "ava", "AVA"} {
		var kid struct {
			Username        string
			Total           int64
			PendingRequests int `json:"pending_requests"`
			Jars            []struct {
				Name                     string
				Balance, Held, Available int64
				SplitBPS                 int `json:"split_bps"`
				Locks                    []any
			}
		}
		e.want(200, e.call("read", "GET", "/kids/"+ref, nil, &kid), "kid "+ref)
		if kid.Username != "ava" || kid.Total != 3000 || kid.PendingRequests != 1 || len(kid.Jars) != 3 || kid.Jars[0].SplitBPS != 7000 {
			t.Fatalf("kid %s: %+v", ref, kid)
		}
		if kid.Jars[0].Locks == nil {
			t.Fatal("locks should be [] not null")
		}
	}
	var er apiErr
	e.want(404, e.call("read", "GET", "/kids/nobody", nil, &er), "unknown kid")
	e.want(404, e.call("read", "GET", "/kids/mom", nil, nil), "an admin is not a kid")

	var jars struct{ Items []struct{ Name string } }
	e.want(200, e.call("read", "GET", "/kids/ava/jars", nil, &jars), "jars")
	if len(jars.Items) != 3 {
		t.Fatalf("jars %+v", jars)
	}

	var ledger struct {
		Items []struct {
			Amount        int64
			Kind, Comment string
			PrivateNote   string `json:"private_note"`
			ActorName     string `json:"actor_name"`
			EffectiveAt   string `json:"effective_at"`
		}
	}
	e.want(200, e.call("read", "GET", "/kids/ava/ledger", nil, &ledger), "ledger")
	if len(ledger.Items) != 2 || ledger.Items[0].Amount != 500 || ledger.Items[0].ActorName != "Mom" {
		t.Fatalf("ledger %+v", ledger.Items)
	}
	e.want(200, e.call("read", "GET", "/kids/ava/ledger?limit=1", nil, &ledger), "limit")
	if len(ledger.Items) != 1 {
		t.Fatalf("limit ignored: %d items", len(ledger.Items))
	}
	e.want(200, e.call("read", "GET", "/kids/ava/ledger?from=2026-03-09", nil, &ledger), "from")
	if len(ledger.Items) != 0 {
		t.Fatalf("from after the deposits still returned %d", len(ledger.Items))
	}
	for _, bad := range []string{"limit=0", "limit=abc", "from=yesterday", "to=nope", "jar_id=not-a-jar"} {
		e.want(422, e.call("read", "GET", "/kids/ava/ledger?"+bad, nil, &er), bad)
	}
	if er.Error.Code != "invalid" || er.Error.Field != "jar" {
		t.Fatalf("jar error %+v", er)
	}

	var goals struct {
		Items []struct {
			Name                      string
			Target, Funded, Remaining int64
			Percent                   int
			Reached                   bool
		}
	}
	e.want(200, e.call("read", "GET", "/kids/ava/goals", nil, &goals), "goals")
	if len(goals.Items) != 1 || goals.Items[0].Name != "Lego" || goals.Items[0].Target != 4000 {
		t.Fatalf("goals %+v", goals)
	}

	var reqs struct {
		Items []struct {
			Amount  int64
			Status  string
			KidName string `json:"kid_name"`
		}
	}
	e.want(200, e.call("read", "GET", "/requests?status=pending", nil, &reqs), "requests")
	if len(reqs.Items) != 1 || reqs.Items[0].Amount != 300 || reqs.Items[0].KidName != "Ava" {
		t.Fatalf("requests %+v", reqs)
	}
	e.want(200, e.call("read", "GET", "/kids/leo/requests", nil, &reqs), "leo's requests")
	if len(reqs.Items) != 0 {
		t.Fatalf("leo has requests: %+v", reqs)
	}
	e.want(422, e.call("read", "GET", "/requests?status=bogus", nil, &er), "bad status")

	// Disabled kids are hidden unless asked for.
	if err := f.Svc.SetDisabled(ctx, f.Mom, f.Leo.ID, true); err != nil {
		t.Fatal(err)
	}
	e.want(200, e.call("read", "GET", "/kids", nil, &kids), "kids after disable")
	if len(kids.Items) != 1 {
		t.Fatalf("disabled kid listed: %+v", kids.Items)
	}
	e.want(200, e.call("read", "GET", "/kids?include_disabled=true", nil, &kids), "include disabled")
	if len(kids.Items) != 2 {
		t.Fatalf("include_disabled gave %d", len(kids.Items))
	}
}

func requestInput(jarID string, amount int64, key string) service.RequestInput {
	return service.RequestInput{JarID: jarID, Amount: amount, Reason: "treat", IdempotencyKey: key}
}
