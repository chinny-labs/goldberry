package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/chinny/goldberry/internal/store"
)

// MaxBatch caps the entries in one PostEntries call.
const MaxBatch = 100

// BatchError says which item of a batch failed. Nothing was posted.
type BatchError struct {
	Index int // zero-based
	Err   error
}

func (e *BatchError) Error() string { return fmt.Sprintf("item %d: %s", e.Index+1, e.Err) }
func (e *BatchError) Unwrap() error { return e.Err }

// BatchResult is what one item posted.
type BatchResult struct {
	Entries  []store.LedgerEntry
	Replayed bool // the item's idempotency key had been used; nothing new was posted
}

// errDryRun rolls a dry run back.
var errDryRun = errors.New("dry run")

// PostEntries posts several entries atomically: all of them or none. Every
// item needs its own idempotency key, so retrying a whole batch after a
// timeout posts nothing twice; items that already posted come back replayed.
// Items apply in order, so a later removal sees an earlier deposit. With
// dryRun the batch is checked in full and then rolled back, and the results
// are what would be posted.
func (s *Service) PostEntries(ctx context.Context, actor store.User, ins []EntryInput, dryRun bool) ([]BatchResult, error) {
	if len(ins) == 0 {
		return nil, invalid("entries", "Send at least one entry.")
	}
	if len(ins) > MaxBatch {
		return nil, invalid("entries", "Send at most %d entries at a time.", MaxBatch)
	}
	plans := make([]entryPlan, len(ins))
	seen := map[string]int{}
	var kids []string
	for i, in := range ins {
		fail := func(err error) error { return &BatchError{Index: i, Err: err} }
		if in.IdempotencyKey == "" {
			return nil, fail(invalid("idempotency_key", "Every entry needs an idempotency key."))
		}
		if j, dup := seen[in.IdempotencyKey]; dup {
			return nil, fail(invalid("idempotency_key", "The key is already used by item %d.", j+1))
		}
		seen[in.IdempotencyKey] = i
		p, err := s.planEntry(ctx, actor, in)
		if err != nil {
			return nil, fail(err)
		}
		plans[i] = p
		if !slices.Contains(kids, p.kid.ID) {
			kids = append(kids, p.kid.ID)
		}
	}
	h, cur, err := s.Household(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BatchResult, len(plans))
	err = s.Store.TxLocked(ctx, kids, func(tx store.Tx) error {
		for i, p := range plans {
			es, replay, err := s.applyEntry(ctx, tx, h, cur, actor, p)
			if err != nil {
				return &BatchError{Index: i, Err: err}
			}
			out[i] = BatchResult{Entries: es, Replayed: replay}
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
