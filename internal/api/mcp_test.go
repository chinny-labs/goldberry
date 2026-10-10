package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chinny/goldberry/internal/api"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// session connects an MCP client using the named token.
func (e *env) session(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             e.srv.URL + api.MCPPath,
		HTTPClient:           &http.Client{Transport: bearerTransport{e.secret[token]}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect with %s token: %v", token, err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	return names
}

// call runs a tool; out receives the structured result. It returns the error text of a failed call.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		var msg string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg += tc.Text
			}
		}
		return msg
	}
	if out != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: bad structured output %s: %v", name, b, err)
		}
	}
	return ""
}

func TestMCPAuthAndToolVisibility(t *testing.T) {
	e := newEnv(t)
	e.tick = true

	// No token, no entry.
	resp, err := http.Post(e.srv.URL+api.MCPPath, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous MCP: %d", resp.StatusCode)
	}

	readTools := []string{"get_household", "get_kid", "get_ledger", "list_goals", "list_kids", "list_requests"}
	writeTools := []string{"add_jar", "archive_goal", "create_goal", "rename_jar", "set_split"}
	moneyTools := []string{"decide_request", "post_entries", "post_entry", "reverse_entry"}
	want := map[string][]string{
		"read":  readTools,
		"write": append(slices.Clone(readTools), writeTools...),
		"money": append(slices.Clone(readTools), moneyTools...),
		"all":   slices.Concat(readTools, writeTools, moneyTools),
	}
	for tok, names := range want {
		slices.Sort(names)
		if got := toolNames(t, e.session(t, tok)); !slices.Equal(got, names) {
			t.Fatalf("%s token sees %v, want %v", tok, got, names)
		}
	}
}

func TestMCPReadTools(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	e.f.Deposit(t, e.f.Ava, 2500)
	cs := e.session(t, "read")

	var kids struct {
		Items []struct {
			Username string
			Total    int64
		}
	}
	if msg := call(t, cs, "list_kids", nil, &kids); msg != "" || len(kids.Items) != 2 || kids.Items[0].Total != 2500 {
		t.Fatalf("list_kids: %q %+v", msg, kids)
	}
	var kid struct {
		Username string
		Jars     []struct{ Name string }
	}
	if msg := call(t, cs, "get_kid", map[string]any{"kid": "ava"}, &kid); msg != "" || kid.Username != "ava" || len(kid.Jars) != 3 {
		t.Fatalf("get_kid: %q %+v", msg, kid)
	}
	var ledger struct{ Items []struct{ Amount int64 } }
	if msg := call(t, cs, "get_ledger", map[string]any{"kid": "ava", "limit": 5}, &ledger); msg != "" || len(ledger.Items) != 1 {
		t.Fatalf("get_ledger: %q %+v", msg, ledger)
	}
	var hh struct{ Currency struct{ Code string } }
	if msg := call(t, cs, "get_household", nil, &hh); msg != "" || hh.Currency.Code != "USD" {
		t.Fatalf("get_household: %q %+v", msg, hh)
	}
	// Failures reach the model as tool errors with a stable code.
	if msg := call(t, cs, "get_kid", map[string]any{"kid": "ghost"}, nil); !strings.HasPrefix(msg, "not_found:") {
		t.Fatalf("unknown kid: %q", msg)
	}
	if msg := call(t, cs, "get_ledger", map[string]any{"kid": "ava", "from": "last week"}, nil); !strings.HasPrefix(msg, "invalid:") || !strings.Contains(msg, "field: from") {
		t.Fatalf("bad date: %q", msg)
	}
	// Tools the token can't see can't be called either.
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "post_entry", Arguments: map[string]any{}}); err == nil {
		t.Fatal("a read token called post_entry")
	}
}

func TestMCPMoneyTools(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	cs := e.session(t, "money")
	var out struct {
		DryRun  bool `json:"dry_run"`
		Results []entriesResp
	}

	args := map[string]any{"kid": "ava", "amount": 800, "comment": "chores", "idempotency_key": "mcp-1"}
	// The default is a dry run: nothing moves.
	if msg := call(t, cs, "post_entry", args, &out); msg != "" || !out.DryRun || out.Results[0].Entries[0].Amount != 800 {
		t.Fatalf("dry run: %q %+v", msg, out)
	}
	if tot, _ := e.balance(t, "ava"); tot != 0 {
		t.Fatalf("a dry run posted: %d", tot)
	}
	args["dry_run"] = false
	if msg := call(t, cs, "post_entry", args, &out); msg != "" || out.DryRun || out.Results[0].Replayed {
		t.Fatalf("post: %q %+v", msg, out)
	}
	if tot, _ := e.balance(t, "ava"); tot != 800 {
		t.Fatalf("balance %d", tot)
	}
	if msg := call(t, cs, "post_entry", args, &out); msg != "" || !out.Results[0].Replayed {
		t.Fatalf("replay: %q %+v", msg, out)
	}
	if tot, _ := e.balance(t, "ava"); tot != 800 {
		t.Fatalf("a replay posted again: %d", tot)
	}
	first := out.Results[0].Entries[0].ID

	// Errors come back as tool errors, naming the failing item.
	msg := call(t, cs, "post_entries", map[string]any{"dry_run": false, "entries": []map[string]any{
		{"kid": "leo", "amount": 100, "idempotency_key": "mcp-2"},
		{"kid": "ava", "amount": 99999, "direction": "remove", "idempotency_key": "mcp-3"},
	}}, nil)
	if !strings.HasPrefix(msg, "insufficient_funds: item 2:") {
		t.Fatalf("overdraw: %q", msg)
	}
	if tot, _ := e.balance(t, "leo"); tot != 0 {
		t.Fatal("a failed batch posted its first item")
	}
	if msg := call(t, cs, "post_entry", map[string]any{"kid": "ava", "amount": 5}, nil); !strings.Contains(msg, "idempotency") {
		t.Fatalf("missing key: %q", msg)
	}

	// Reversing defaults to showing the entry.
	var rev struct {
		DryRun  bool       `json:"dry_run"`
		Entry   *entryOut  `json:"entry"`
		Entries []entryOut `json:"entries"`
	}
	if msg := call(t, cs, "reverse_entry", map[string]any{"entry_id": first}, &rev); msg != "" || !rev.DryRun || rev.Entry == nil || rev.Entry.Amount != 800 {
		t.Fatalf("reverse dry run: %q %+v", msg, rev)
	}
	if tot, _ := e.balance(t, "ava"); tot != 800 {
		t.Fatal("reverse dry run moved money")
	}
	if msg := call(t, cs, "reverse_entry", map[string]any{"entry_id": first, "dry_run": false}, &rev); msg != "" || len(rev.Entries) != 1 || rev.Entries[0].Amount != -800 {
		t.Fatalf("reverse: %q %+v", msg, rev)
	}

	// Deciding a request.
	e.f.Deposit(t, e.f.Ava, 1000)
	req, err := e.f.Svc.CreateRequest(context.Background(), e.f.Ava, requestInput(e.f.Jar(t, e.f.Ava).ID, 400, "mcp-req"))
	if err != nil {
		t.Fatal(err)
	}
	var dec struct {
		DryRun  bool `json:"dry_run"`
		Request struct{ Status string }
	}
	if msg := call(t, cs, "decide_request", map[string]any{"request_id": req.ID, "decision": "approve"}, &dec); msg != "" || !dec.DryRun || dec.Request.Status != "pending" {
		t.Fatalf("decide dry run: %q %+v", msg, dec)
	}
	if msg := call(t, cs, "decide_request", map[string]any{"request_id": req.ID, "decision": "approve", "dry_run": false}, &dec); msg != "" || dec.Request.Status != "approved" {
		t.Fatalf("decide: %q %+v", msg, dec)
	}
	if msg := call(t, cs, "decide_request", map[string]any{"request_id": req.ID, "decision": "deny", "dry_run": false}, nil); !strings.HasPrefix(msg, "already_decided:") {
		t.Fatalf("decide twice: %q", msg)
	}
	if msg := call(t, cs, "decide_request", map[string]any{"request_id": req.ID, "decision": "maybe"}, nil); !strings.HasPrefix(msg, "invalid:") {
		t.Fatalf("bad decision: %q", msg)
	}

	// MCP postings are audited with the token, like API ones.
	log, _ := e.f.Store.ListAudit(context.Background(), e.f.Ava.ID, 50)
	var traced bool
	for _, a := range log {
		if a.Action == "api.entry" && strings.Contains(string(a.Detail), `"token"`) {
			traced = true
		}
	}
	if !traced {
		t.Fatal("no token in the audit trail for MCP postings")
	}
}

func TestMCPWriteTools(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	cs := e.session(t, "write")
	var j struct {
		ID, Name string
	}
	if msg := call(t, cs, "add_jar", map[string]any{"kid": "ava", "name": "Toys"}, &j); msg != "" || j.Name != "Toys" {
		t.Fatalf("add_jar: %q %+v", msg, j)
	}
	if msg := call(t, cs, "rename_jar", map[string]any{"jar_id": j.ID, "name": "Lego"}, &j); msg != "" || j.Name != "Lego" {
		t.Fatalf("rename_jar: %q %+v", msg, j)
	}
	var g struct {
		ID, Name string
		Target   int64
	}
	if msg := call(t, cs, "create_goal", map[string]any{"kid": "ava", "name": "Bike", "target": 20000}, &g); msg != "" || g.Target != 20000 {
		t.Fatalf("create_goal: %q %+v", msg, g)
	}
	if msg := call(t, cs, "set_split", map[string]any{"kid": "ava", "shares": map[string]int{j.ID: 5000}}, nil); !strings.HasPrefix(msg, "invalid:") {
		t.Fatalf("bad split: %q", msg)
	}
	if msg := call(t, cs, "archive_goal", map[string]any{"goal_id": g.ID}, &g); msg != "" {
		t.Fatalf("archive_goal: %q", msg)
	}
	// A revoked token stops working at once.
	toks, _ := e.f.Svc.APITokens(context.Background(), e.f.Mom, e.f.Mom.ID)
	for _, tk := range toks {
		if tk.Label == "write" {
			_ = e.f.Svc.RevokeAPIToken(context.Background(), e.f.Mom, tk.ID)
		}
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "add_jar", Arguments: map[string]any{"kid": "ava", "name": "x"}}); err == nil {
		t.Fatal("a revoked token still calls tools")
	}
}
