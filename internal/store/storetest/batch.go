package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/chinny/goldberry/internal/service"
	"github.com/chinny/goldberry/internal/store"
)

var batchTests = []struct {
	name string
	fn   func(*testing.T, Opener)
}{
	{"BatchAllOrNothing", testBatchAtomic},
	{"BatchDryRun", testBatchDryRun},
	{"BatchReplay", testBatchReplay},
	{"BatchValidation", testBatchValidation},
	{"BatchConcurrency", testBatchConcurrency},
}

func ledgerCount(t *testing.T, f *Fixture, kid store.User) int {
	t.Helper()
	es, err := f.Store.ListLedger(context.Background(), store.LedgerFilter{KidID: kid.ID})
	must(t, err)
	return len(es)
}

func testBatchAtomic(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	jar := f.Jar(t, f.Ava)

	// Items apply in order, across kids: a removal sees the deposit before it.
	res, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{
		{KidID: f.Ava.ID, Amount: 1000, Comment: "chores", IdempotencyKey: "b1-a"},
		{KidID: f.Leo.ID, Amount: 500, IdempotencyKey: "b1-b"},
		{KidID: f.Ava.ID, Amount: 400, Remove: true, JarID: jar.ID, IdempotencyKey: "b1-c"},
	}, false)
	must(t, err)
	if len(res) != 3 || res[2].Entries[0].Amount != -400 || res[0].Replayed {
		t.Fatalf("results %+v", res)
	}
	f.wantJar(t, f.Ava, 600, 0)
	f.wantJar(t, f.Leo, 500, 0)

	// One bad item (overdrawn) rejects the whole batch and names the item.
	before := ledgerCount(t, f, f.Ava) + ledgerCount(t, f, f.Leo)
	_, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{
		{KidID: f.Ava.ID, Amount: 100, IdempotencyKey: "b2-a"},
		{KidID: f.Leo.ID, Amount: 9999, Remove: true, JarID: f.Jar(t, f.Leo).ID, IdempotencyKey: "b2-b"},
	}, false)
	var be *service.BatchError
	var ins *service.ErrInsufficient
	if !errors.As(err, &be) || be.Index != 1 || !errors.As(err, &ins) || ins.Available != 500 {
		t.Fatalf("overdraw: %v", err)
	}
	if after := ledgerCount(t, f, f.Ava) + ledgerCount(t, f, f.Leo); after != before {
		t.Fatalf("a failed batch left %d new entries", after-before)
	}
	f.wantJar(t, f.Ava, 600, 0)
	// The first item's key was rolled back with the rest, so it is free to use.
	_, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 100, IdempotencyKey: "b2-a"}}, false)
	must(t, err)
	f.wantJar(t, f.Ava, 700, 0)

	// A split deposit inside a batch posts its parts.
	res, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 1000, UseSplit: true, IdempotencyKey: "b3"}}, false)
	must(t, err)
	if len(res[0].Entries) != 3 {
		t.Fatalf("split parts: %d", len(res[0].Entries))
	}
}

func testBatchDryRun(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	items := []service.EntryInput{
		{KidID: f.Ava.ID, Amount: 700, IdempotencyKey: "d1"},
		{KidID: f.Leo.ID, Amount: 300, IdempotencyKey: "d2"},
	}
	res, err := f.Svc.PostEntries(ctx, f.Mom, items, true)
	must(t, err)
	if len(res) != 2 || res[0].Entries[0].Amount != 700 {
		t.Fatalf("dry run results %+v", res)
	}
	f.wantJar(t, f.Ava, 0, 0)
	f.wantJar(t, f.Leo, 0, 0)
	// The dry run used no keys: the real run posts, and is not a replay.
	res, err = f.Svc.PostEntries(ctx, f.Mom, items, false)
	must(t, err)
	if res[0].Replayed || res[1].Replayed {
		t.Fatal("first real run reported as replay")
	}
	f.wantJar(t, f.Ava, 700, 0)

	// A dry run still fails on a bad item.
	_, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 1, Remove: true, JarID: f.Jar(t, f.Ava).ID, IdempotencyKey: "d3"},
		{KidID: f.Ava.ID, Amount: 99999, Remove: true, JarID: f.Jar(t, f.Ava).ID, IdempotencyKey: "d4"}}, true)
	var be *service.BatchError
	if !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("dry run overdraw: %v", err)
	}
}

func testBatchReplay(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	items := []service.EntryInput{
		{KidID: f.Ava.ID, Amount: 700, IdempotencyKey: "r1"},
		{KidID: f.Ava.ID, Amount: 300, UseSplit: true, IdempotencyKey: "r2"},
	}
	first, err := f.Svc.PostEntries(ctx, f.Mom, items, false)
	must(t, err)
	// Retrying after a timeout posts nothing and returns the same entries.
	again, err := f.Svc.PostEntries(ctx, f.Mom, items, false)
	must(t, err)
	if !again[0].Replayed || !again[1].Replayed || again[0].Entries[0].ID != first[0].Entries[0].ID || len(again[1].Entries) != len(first[1].Entries) {
		t.Fatalf("replay %+v vs %+v", again, first)
	}
	f.wantJar(t, f.Ava, 700+210, 0)

	// Reusing a key for a different payment is refused, so a mix-up can't hide.
	var ue *service.UserError
	_, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 701, IdempotencyKey: "r1"}}, false)
	if !errors.As(err, &ue) || ue.Field != "idempotency_key" {
		t.Fatalf("key reused for a different amount: %v", err)
	}
	_, err = f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 700, Remove: true, IdempotencyKey: "r1"}}, false)
	if !errors.As(err, &ue) {
		t.Fatalf("key reused for the other direction: %v", err)
	}
	f.wantJar(t, f.Ava, 700+210, 0)

	// A partly-new batch replays what posted and posts the rest.
	mixed, err := f.Svc.PostEntries(ctx, f.Mom, append(items[:1:1], service.EntryInput{KidID: f.Leo.ID, Amount: 50, IdempotencyKey: "r3"}), false)
	must(t, err)
	if !mixed[0].Replayed || mixed[1].Replayed {
		t.Fatalf("mixed %+v", mixed)
	}
	f.wantJar(t, f.Leo, 50, 0)

	// The single-entry path reports replays the same way.
	_, replay, err := f.Svc.PostEntryReplay(ctx, f.Mom, service.EntryInput{KidID: f.Ava.ID, Amount: 700, IdempotencyKey: "r1"})
	must(t, err)
	if !replay {
		t.Fatal("PostEntryReplay missed a replay")
	}
}

func testBatchValidation(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	bad := func(name string, ins []service.EntryInput, actor store.User, wantIdx int, wantField string) {
		t.Helper()
		_, err := f.Svc.PostEntries(ctx, actor, ins, false)
		var be *service.BatchError
		var ue *service.UserError
		switch {
		case wantIdx < 0:
			if !errors.As(err, &ue) || ue.Field != wantField {
				t.Fatalf("%s: %v", name, err)
			}
		case !errors.As(err, &be) || be.Index != wantIdx || !errors.As(err, &ue) || ue.Field != wantField:
			t.Fatalf("%s: %v", name, err)
		}
	}
	bad("empty", nil, f.Mom, -1, "entries")
	var many []service.EntryInput
	for i := 0; i <= service.MaxBatch; i++ {
		many = append(many, service.EntryInput{KidID: f.Ava.ID, Amount: 1, IdempotencyKey: string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	bad("too many", many, f.Mom, -1, "entries")
	bad("no key", []service.EntryInput{{KidID: f.Ava.ID, Amount: 1}}, f.Mom, 0, "idempotency_key")
	bad("duplicate key", []service.EntryInput{{KidID: f.Ava.ID, Amount: 1, IdempotencyKey: "x"}, {KidID: f.Leo.ID, Amount: 1, IdempotencyKey: "x"}}, f.Mom, 1, "idempotency_key")
	bad("zero amount", []service.EntryInput{{KidID: f.Ava.ID, Amount: 0, IdempotencyKey: "z"}}, f.Mom, 0, "amount")
	if _, err := f.Svc.PostEntries(ctx, f.Ava, []service.EntryInput{{KidID: f.Ava.ID, Amount: 1, IdempotencyKey: "k"}}, false); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a kid posted a batch: %v", err)
	}
	if _, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Mom.ID, Amount: 1, IdempotencyKey: "m"}}, false); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("posting to an admin: %v", err)
	}
	// A key can't be borrowed to read another kid's entry.
	_, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Ava.ID, Amount: 5, IdempotencyKey: "own"}}, false)
	must(t, err)
	if _, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{{KidID: f.Leo.ID, Amount: 5, IdempotencyKey: "own"}}, false); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a key reused for another kid: %v", err)
	}
}

// testBatchConcurrency hammers TxLocked: batches over the same two kids in
// opposite orders must neither deadlock nor lose an update, and concurrent
// removals must never overdraw.
func testBatchConcurrency(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)

	const batches = 40
	var wg sync.WaitGroup
	errs := make(chan error, batches)
	for i := 0; i < batches; i++ {
		kids := []string{f.Ava.ID, f.Leo.ID}
		if i%2 == 1 {
			kids[0], kids[1] = kids[1], kids[0]
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{
				{KidID: kids[0], Amount: 100, IdempotencyKey: fmt.Sprintf("c%d-a", i)},
				{KidID: kids[1], Amount: 100, IdempotencyKey: fmt.Sprintf("c%d-b", i)},
			}, false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	f.wantJar(t, f.Ava, batches*100, 0)
	f.wantJar(t, f.Leo, batches*100, 0)

	// 60 racing removals of 100 against 4000: exactly 40 may succeed.
	jar := f.Jar(t, f.Ava)
	var ok, refused int
	var mu sync.Mutex
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.Svc.PostEntries(ctx, f.Mom, []service.EntryInput{
				{KidID: f.Ava.ID, JarID: jar.ID, Amount: 100, Remove: true, IdempotencyKey: fmt.Sprintf("rm%d", i)}}, false)
			var ins *service.ErrInsufficient
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.As(err, &ins):
				refused++
			default:
				t.Errorf("removal %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if ok != 40 || refused != 20 {
		t.Fatalf("%d removals succeeded and %d were refused, want 40 and 20", ok, refused)
	}
	f.wantJar(t, f.Ava, 0, 0)
}
