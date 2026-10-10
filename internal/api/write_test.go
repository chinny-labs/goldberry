package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type entryOut struct {
	ID             string
	KidID          string `json:"kid_id"`
	JarID          string `json:"jar_id"`
	JarName        string `json:"jar_name"`
	Amount         int64
	Kind           string
	Comment        string
	ActorName      string  `json:"actor_name"`
	IdempotencyKey *string `json:"idempotency_key"`
	ReversesID     *string `json:"reverses_id"`
}

type entriesResp struct {
	Entries  []entryOut
	Replayed bool
}

func (e *env) balance(t *testing.T, kid string) (total, held int64) {
	t.Helper()
	var k struct{ Total, Held int64 }
	e.want(200, e.call("read", "GET", "/kids/"+kid, nil, &k), "balance of "+kid)
	return k.Total, k.Held
}

func TestScopes(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	var er apiErr
	money := map[string]string{"/kids/ava/entries": `{"amount":1}`, "/entries:batch": `{"entries":[]}`, "/entries/x/reverse": `{}`,
		"/kids/ava/transfers": `{}`, "/requests/x/decision": `{}`}
	write := map[string]string{"/kids/ava/jars": `{"name":"x"}`, "/kids/ava/goals": `{}`, "/jars/x/archive": `{}`, "/goals/x/archive": `{}`}
	for path, body := range money {
		for _, tok := range []string{"read", "write"} {
			e.want(403, e.call(tok, "POST", path, json.RawMessage(body), &er), tok+" token on "+path)
			if er.Error.Code != "insufficient_scope" || !strings.Contains(er.Error.Message, "money") {
				t.Fatalf("%s: %+v", path, er)
			}
		}
	}
	for path, body := range write {
		for _, tok := range []string{"read", "money"} {
			e.want(403, e.call(tok, "POST", path, json.RawMessage(body), nil), tok+" token on "+path)
		}
	}
	e.want(403, e.call("money", "PUT", "/kids/ava/split", json.RawMessage(`{}`), nil), "money token on split")
	e.want(401, e.call("", "POST", "/kids/ava/entries", json.RawMessage(`{"amount":1}`), nil), "anonymous write")
}

func TestPostEntry(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	ctx := context.Background()
	var er apiErr
	key := map[string]string{"Idempotency-Key": "pay-1"}

	// A key is required: a retry must not be able to double-pay.
	e.want(422, e.call("money", "POST", "/kids/ava/entries", map[string]any{"amount": 500}, &er), "no key")
	if er.Error.Field != "idempotency_key" {
		t.Fatalf("%+v", er)
	}
	if tot, _ := e.balance(t, "ava"); tot != 0 {
		t.Fatalf("a refused post moved money: %d", tot)
	}

	var out entriesResp
	e.want(201, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 500, "comment": "dishes", "private_note": "twice"}, key, &out), "post")
	if len(out.Entries) != 1 || out.Entries[0].Amount != 500 || out.Replayed || out.Entries[0].ActorName != "Mom" ||
		out.Entries[0].Comment != "dishes" || *out.Entries[0].IdempotencyKey != "pay-1" || out.Entries[0].JarName != "Spend" {
		t.Fatalf("entry %+v", out)
	}
	first := out.Entries[0].ID

	// Retrying returns the same entry and posts nothing.
	e.want(200, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 500, "comment": "dishes"}, key, &out), "replay")
	if !out.Replayed || out.Entries[0].ID != first {
		t.Fatalf("replay %+v", out)
	}
	if tot, _ := e.balance(t, "ava"); tot != 500 {
		t.Fatalf("balance %d after a replay", tot)
	}
	// A key reused for a different payment is an error, not a silent replay.
	for name, body := range map[string]map[string]any{
		"amount":    {"amount": 501},
		"direction": {"amount": 500, "direction": "remove"},
		"jar":       {"amount": 500, "jar_id": e.f.Jar(t, e.f.Ava).ID + "x"},
	} {
		e.want(422, e.callH("money", "POST", "/kids/ava/entries", body, key, &er), "key reused with another "+name)
		if er.Error.Code != "invalid" || er.Error.Field != "idempotency_key" {
			t.Fatalf("%s: %+v", name, er)
		}
	}
	// Only the money and the direction count: a retry may fix the comment.
	e.want(200, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 500, "comment": "dishes!"}, key, &out), "retry with a new comment")
	if !out.Replayed {
		t.Fatal("a comment-only change should still replay")
	}
	// The key can also ride in the body, but must agree with the header.
	e.want(201, e.call("money", "POST", "/kids/ava/entries", map[string]any{"amount": 100, "idempotency_key": "body-key"}, &out), "key in body")
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 100, "idempotency_key": "other"}, key, &er), "key mismatch")

	// Split across jars.
	e.want(201, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 1000, "use_split": true}, map[string]string{"Idempotency-Key": "split-1"}, &out), "split")
	if len(out.Entries) != 3 {
		t.Fatalf("split gave %d entries", len(out.Entries))
	}

	// Removing more than is available is refused with the figure.
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 99999, "direction": "remove"}, map[string]string{"Idempotency-Key": "rm-1"}, &er), "overdraw")
	if er.Error.Code != "insufficient_funds" || er.Error.Field != "amount" || er.Error.Detail["jar"] != "Spend" || er.Error.Detail["available"].(float64) <= 0 {
		t.Fatalf("overdraw %+v", er)
	}
	e.want(201, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 100, "direction": "remove", "comment": "candy"}, map[string]string{"Idempotency-Key": "rm-2"}, &out), "remove")
	if out.Entries[0].Amount != -100 || out.Entries[0].Kind != "withdrawal" {
		t.Fatalf("remove %+v", out)
	}

	// Bad input is told what's wrong; unknown fields aren't ignored.
	hdr := func(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 0}, hdr("z1"), &er), "zero")
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 5, "direction": "sideways"}, hdr("z2"), &er), "direction")
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 5, "jar_id": "nope"}, hdr("z3"), &er), "jar")
	e.want(400, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 5, "amt": 5}, hdr("z4"), &er), "unknown field")
	if er.Error.Code != "bad_request" {
		t.Fatalf("%+v", er)
	}
	e.want(400, e.callH("money", "POST", "/kids/ava/entries", json.RawMessage(`{"amount": 1.5}`), hdr("z5"), &er), "fractional amount")
	e.want(400, e.callH("money", "POST", "/kids/ava/entries", json.RawMessage(`not json`), hdr("z6"), &er), "garbage")
	e.want(400, e.callH("money", "POST", "/kids/ava/entries", nil, hdr("z7"), &er), "empty body")
	e.want(404, e.callH("money", "POST", "/kids/nobody/entries", map[string]any{"amount": 5}, hdr("z8"), &er), "unknown kid")
	e.want(422, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 5}, hdr(strings.Repeat("k", 200)), &er), "long key")

	// Dry run checks and reports without posting.
	before, _ := e.balance(t, "ava")
	e.want(200, e.callH("money", "POST", "/kids/ava/entries?dry_run=true", map[string]any{"amount": 777}, hdr("dry-1"), &out), "dry run")
	if len(out.Entries) != 1 || out.Entries[0].Amount != 777 {
		t.Fatalf("dry run %+v", out)
	}
	if after, _ := e.balance(t, "ava"); after != before {
		t.Fatal("dry run posted")
	}

	// API postings are traceable to the token that made them.
	log, err := e.f.Store.ListAudit(ctx, e.f.Ava.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var traced bool
	for _, a := range log {
		if a.Action == "api.entry" && strings.Contains(string(a.Detail), `"token"`) {
			traced = true
		}
	}
	if !traced {
		t.Fatalf("no api.entry audit row with a token in %d rows", len(log))
	}

	// Client keys can't collide with Goldberry's own ("reverse:<id>").
	e.want(201, e.callH("money", "POST", "/kids/leo/entries", map[string]any{"amount": 50}, hdr("reverse:"+first), &out), "hostile key")
	e.want(200, e.call("money", "POST", "/entries/"+first+"/reverse", nil, &out), "reverse unaffected")
	if len(out.Entries) != 1 || out.Entries[0].Amount != -500 {
		t.Fatalf("reverse %+v", out)
	}
}

func TestBatch(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	var er apiErr
	var out struct {
		DryRun  bool `json:"dry_run"`
		Results []entriesResp
	}
	items := func(extra ...map[string]any) map[string]any {
		es := []map[string]any{
			{"kid": "ava", "amount": 1000, "comment": "chores", "idempotency_key": "w1-ava"},
			{"kid": e.f.Leo.ID, "amount": 400, "idempotency_key": "w1-leo"},
		}
		return map[string]any{"entries": append(es, extra...)}
	}

	e.want(200, e.call("money", "POST", "/entries:batch?dry_run=true", items(), &out), "dry run")
	if !out.DryRun || len(out.Results) != 2 || out.Results[0].Entries[0].Amount != 1000 {
		t.Fatalf("dry run %+v", out)
	}
	if a, _ := e.balance(t, "ava"); a != 0 {
		t.Fatal("dry run posted")
	}

	e.want(200, e.call("money", "POST", "/entries:batch", items(), &out), "batch")
	if out.DryRun || out.Results[1].Entries[0].KidID != e.f.Leo.ID || out.Results[0].Replayed {
		t.Fatalf("batch %+v", out)
	}
	if a, _ := e.balance(t, "ava"); a != 1000 {
		t.Fatalf("ava %d", a)
	}

	// Retry the lot: nothing posts twice.
	e.want(200, e.call("money", "POST", "/entries:batch", items(), &out), "retry")
	if !out.Results[0].Replayed || !out.Results[1].Replayed {
		t.Fatalf("retry %+v", out)
	}
	if a, _ := e.balance(t, "ava"); a != 1000 {
		t.Fatalf("ava %d after retry", a)
	}
	// Reusing one item's key for a different amount fails the batch at that item.
	changed := items()
	changed["entries"].([]map[string]any)[1]["amount"] = 401
	e.want(422, e.call("money", "POST", "/entries:batch", changed, &er), "batch key reused")
	if er.Error.Field != "idempotency_key" || er.Error.Detail["item"].(float64) != 1 {
		t.Fatalf("%+v", er)
	}

	// One bad item sinks the batch and says which.
	bad := items(map[string]any{"kid": "ava", "amount": 999999, "direction": "remove", "idempotency_key": "w2-big"})
	bad["entries"].([]map[string]any)[0]["idempotency_key"] = "w2-ava"
	bad["entries"].([]map[string]any)[1]["idempotency_key"] = "w2-leo"
	e.want(422, e.call("money", "POST", "/entries:batch", bad, &er), "overdraw")
	if er.Error.Code != "insufficient_funds" || er.Error.Detail["item"].(float64) != 2 || !strings.HasPrefix(er.Error.Message, "Item 3:") {
		t.Fatalf("overdraw %+v", er)
	}
	if a, _ := e.balance(t, "ava"); a != 1000 {
		t.Fatalf("a failed batch moved money: %d", a)
	}

	e.want(404, e.call("money", "POST", "/entries:batch", map[string]any{"entries": []map[string]any{
		{"kid": "ava", "amount": 1, "idempotency_key": "ok"}, {"kid": "ghost", "amount": 1, "idempotency_key": "g"}}}, &er), "unknown kid")
	if er.Error.Detail["item"].(float64) != 1 {
		t.Fatalf("%+v", er)
	}
	e.want(422, e.call("money", "POST", "/entries:batch", map[string]any{"entries": []map[string]any{{"kid": "ava", "amount": 1}}}, &er), "no key")
	if er.Error.Field != "idempotency_key" || er.Error.Detail["item"].(float64) != 0 {
		t.Fatalf("%+v", er)
	}
	e.want(422, e.call("money", "POST", "/entries:batch", map[string]any{"entries": []any{}}, &er), "empty")
	e.want(422, e.call("money", "POST", "/entries:batch", map[string]any{"entries": []map[string]any{
		{"kid": "ava", "amount": 1, "idempotency_key": "d"}, {"kid": "leo", "amount": 1, "idempotency_key": "d"}}}, &er), "duplicate keys")
}

func TestReverseTransferDecide(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	ctx := context.Background()
	var er apiErr
	var out entriesResp
	hdr := func(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }

	e.want(201, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 2000}, hdr("e1"), &out), "deposit")
	dep := out.Entries[0]
	e.want(200, e.call("money", "POST", "/entries/"+dep.ID+"/reverse", nil, &out), "reverse")
	if out.Entries[0].Amount != -2000 || *out.Entries[0].ReversesID != dep.ID {
		t.Fatalf("reversal %+v", out)
	}
	revID := out.Entries[0].ID
	e.want(200, e.call("money", "POST", "/entries/"+dep.ID+"/reverse", nil, &out), "reverse again")
	if out.Entries[0].ID != revID {
		t.Fatal("a second reverse posted another reversal")
	}
	e.want(409, e.call("money", "POST", "/entries/"+revID+"/reverse", nil, &er), "reverse a reversal")
	if er.Error.Code != "conflict" {
		t.Fatalf("%+v", er)
	}
	e.want(404, e.call("money", "POST", "/entries/nope/reverse", nil, &er), "reverse unknown")

	// Transfers between a kid's own jars.
	e.want(201, e.callH("money", "POST", "/kids/ava/entries", map[string]any{"amount": 1000}, hdr("e2"), &out), "deposit 2")
	jars := e.f.Jar(t, e.f.Ava)
	var save string
	var js struct{ Items []struct{ ID, Name string } }
	e.want(200, e.call("read", "GET", "/kids/ava/jars", nil, &js), "jars")
	for _, j := range js.Items {
		if j.Name == "Save" {
			save = j.ID
		}
	}
	tr := map[string]any{"from_jar_id": jars.ID, "to_jar_id": save, "amount": 300}
	e.want(201, e.callH("money", "POST", "/kids/ava/transfers", tr, hdr("t1"), &out), "transfer")
	if len(out.Entries) != 2 || out.Entries[0].Amount != -300 || out.Entries[1].Amount != 300 {
		t.Fatalf("transfer %+v", out)
	}
	e.want(200, e.callH("money", "POST", "/kids/ava/transfers", tr, hdr("t1"), &out), "transfer replay")
	if !out.Replayed || len(out.Entries) != 2 || out.Entries[0].Amount != -300 || out.Entries[1].Amount != 300 {
		t.Fatalf("transfer replay %+v", out)
	}
	e.want(422, e.callH("money", "POST", "/kids/ava/transfers", map[string]any{"from_jar_id": jars.ID, "to_jar_id": save, "amount": 301}, hdr("t1"), &er), "transfer key reused")
	if er.Error.Field != "idempotency_key" {
		t.Fatalf("%+v", er)
	}
	e.want(422, e.callH("money", "POST", "/kids/ava/transfers", map[string]any{"from_jar_id": save, "to_jar_id": save, "amount": 1}, hdr("t2"), &er), "same jar")
	e.want(422, e.callH("money", "POST", "/kids/ava/transfers", map[string]any{"from_jar_id": save, "to_jar_id": jars.ID, "amount": 99999}, hdr("t3"), &er), "overdraw")
	if er.Error.Code != "insufficient_funds" {
		t.Fatalf("%+v", er)
	}
	e.want(422, e.call("money", "POST", "/kids/ava/transfers", tr, &er), "no key")

	// Deciding a kid's request.
	req, err := e.f.Svc.CreateRequest(ctx, e.f.Ava, requestInput(jars.ID, 500, "r1"))
	if err != nil {
		t.Fatal(err)
	}
	var rq struct {
		Status         string
		Amount         int64
		ApprovedAmount int64  `json:"approved_amount"`
		DecidedByName  string `json:"decided_by_name"`
		DecisionNote   string `json:"decision_note"`
	}
	e.want(422, e.call("money", "POST", "/requests/"+req.ID+"/decision", map[string]any{"decision": "maybe"}, &er), "bad decision")
	e.want(422, e.call("money", "POST", "/requests/"+req.ID+"/decision", map[string]any{"decision": "approve", "approved_amount": 900}, &er), "approve too much")
	before, _ := e.balance(t, "ava")
	e.want(200, e.call("money", "POST", "/requests/"+req.ID+"/decision", map[string]any{"decision": "approve", "approved_amount": 400, "note": "just 4"}, &rq), "approve")
	if rq.Status != "approved" || rq.ApprovedAmount != 400 || rq.Amount != 500 || rq.DecidedByName != "Mom" || rq.DecisionNote != "just 4" {
		t.Fatalf("approved %+v", rq)
	}
	if after, _ := e.balance(t, "ava"); after != before-400 {
		t.Fatalf("balance %d → %d, want −400", before, after)
	}
	e.want(409, e.call("money", "POST", "/requests/"+req.ID+"/decision", map[string]any{"decision": "deny"}, &er), "decide twice")
	if er.Error.Code != "already_decided" {
		t.Fatalf("%+v", er)
	}
	e.want(404, e.call("money", "POST", "/requests/nope/decision", map[string]any{"decision": "deny"}, &er), "unknown request")
}

func TestJarsAndGoals(t *testing.T) {
	e := newEnv(t)
	e.tick = true
	var er apiErr
	type jarT struct {
		ID, Name, Kind string
		Archived       bool
		SplitBPS       int `json:"split_bps"`
	}
	var j jarT

	e.want(201, e.call("write", "POST", "/kids/ava/jars", map[string]any{"name": "Toys"}, &j), "add jar")
	if j.Name != "Toys" || j.Kind != "custom" || j.SplitBPS != 0 {
		t.Fatalf("jar %+v", j)
	}
	e.want(200, e.call("write", "PUT", "/jars/"+j.ID, map[string]any{"name": "Lego fund"}, &j), "rename")
	if j.Name != "Lego fund" {
		t.Fatalf("jar %+v", j)
	}
	e.want(422, e.call("write", "POST", "/kids/ava/jars", map[string]any{"name": " "}, &er), "empty name")
	e.want(404, e.call("write", "PUT", "/jars/nope", map[string]any{"name": "x"}, &er), "rename unknown")

	// The split must total 100%.
	var all struct{ Items []jarT }
	e.want(200, e.call("read", "GET", "/kids/ava/jars", nil, &all), "jars")
	shares := map[string]int{}
	for _, x := range all.Items {
		shares[x.ID] = 0
	}
	e.want(422, e.call("write", "PUT", "/kids/ava/split", map[string]any{"shares": shares}, &er), "split of 0%")
	for _, x := range all.Items {
		if x.Name == "Spend" {
			shares[x.ID] = 6000
		}
		if x.Name == "Lego fund" {
			shares[x.ID] = 4000
		}
	}
	e.want(200, e.call("write", "PUT", "/kids/ava/split", map[string]any{"shares": shares}, &all), "set split")
	for _, x := range all.Items {
		if x.Name == "Lego fund" && x.SplitBPS != 4000 {
			t.Fatalf("split not applied: %+v", all.Items)
		}
	}

	e.want(422, e.call("write", "POST", "/jars/"+j.ID+"/archive", nil, &er), "archive refused while it has a share")
	// (a jar with a share can't be archived; zero it first)
	shares[j.ID] = 0
	for _, x := range all.Items {
		if x.Name == "Spend" {
			shares[x.ID] = 10000
		}
	}
	e.want(200, e.call("write", "PUT", "/kids/ava/split", map[string]any{"shares": shares}, nil), "reset split")
	e.want(200, e.call("write", "POST", "/jars/"+j.ID+"/archive", nil, &j), "archive")
	if !j.Archived {
		t.Fatalf("jar %+v", j)
	}

	// Goals.
	type goalT struct {
		ID, Name, Emoji string
		Target, Funded  int64
		Percent         int
		Archived        *string `json:"archived_at"`
	}
	var g goalT
	e.want(201, e.call("write", "POST", "/kids/ava/goals", map[string]any{"name": "Bike", "emoji": "🚲", "target": 20000}, &g), "add goal")
	if g.Name != "Bike" || g.Target != 20000 || g.Emoji != "🚲" {
		t.Fatalf("goal %+v", g)
	}
	e.want(200, e.call("write", "PUT", "/goals/"+g.ID, map[string]any{"name": "Red bike", "target": 25000}, &g), "update goal")
	if g.Name != "Red bike" || g.Target != 25000 {
		t.Fatalf("goal %+v", g)
	}
	e.want(422, e.call("write", "POST", "/kids/ava/goals", map[string]any{"name": "Free", "target": 0}, &er), "zero target")
	e.want(200, e.call("write", "POST", "/goals/"+g.ID+"/archive", nil, &g), "archive goal")
	if g.Archived == nil || g.Funded != 0 {
		t.Fatalf("goal %+v", g)
	}
	var gl struct{ Items []goalT }
	e.want(200, e.call("read", "GET", "/kids/ava/goals", nil, &gl), "goals")
	if len(gl.Items) != 0 {
		t.Fatalf("archived goal still listed: %+v", gl)
	}
}
