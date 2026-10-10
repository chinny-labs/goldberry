package service

import (
	"context"

	"github.com/chinny/goldberry/internal/store"
)

// KidSummary is a kid with their money totals, for lists.
type KidSummary struct {
	Kid   store.User
	Total int64 // sum of jar balances
	Held  int64 // sum of pending holds
}

// Available is what the kid can still ask for.
func (k KidSummary) Available() int64 { return k.Total - k.Held }

// KidSummaries lists kids (disabled ones only when asked) with their totals.
func (s *Service) KidSummaries(ctx context.Context, includeDisabled bool) ([]KidSummary, error) {
	kids, err := s.Store.ListUsers(ctx, store.RoleKid)
	if err != nil {
		return nil, err
	}
	var out []KidSummary
	for _, k := range kids {
		if k.Disabled() && !includeDisabled {
			continue
		}
		sum := KidSummary{Kid: k}
		jars, err := s.Store.JarBalances(ctx, k.ID)
		if err != nil {
			return nil, err
		}
		for _, j := range jars {
			sum.Total += j.Balance
			sum.Held += j.Held
		}
		out = append(out, sum)
	}
	return out, nil
}

// ResolveKid finds a kid by ID or username, so scripts and agents can say
// "ava". Anything that isn't a kid is ErrNotFound.
func (s *Service) ResolveKid(ctx context.Context, idOrUsername string) (store.User, error) {
	if u, err := s.Store.GetUser(ctx, idOrUsername); err == nil && u.IsKid() {
		return u, nil
	}
	u, err := s.Store.GetUserByUsername(ctx, idOrUsername)
	if err != nil || !u.IsKid() {
		return store.User{}, ErrNotFound
	}
	return u, nil
}

// KidLedger lists a kid's entries, newest first, narrowed by f (whose KidID
// is overwritten). A jar that isn't the kid's is invalid.
func (s *Service) KidLedger(ctx context.Context, kidID string, f store.LedgerFilter) ([]store.LedgerEntry, error) {
	if _, err := getKid(ctx, s.Store, kidID); err != nil {
		return nil, err
	}
	f.KidID = kidID
	if f.JarID != "" {
		if _, err := jarFor(ctx, s.Store, kidID, f.JarID); err != nil {
			return nil, err
		}
	}
	return s.Store.ListLedger(ctx, f)
}

// KidGoals lists a kid's active goals with progress.
func (s *Service) KidGoals(ctx context.Context, kidID string) ([]GoalView, error) {
	if _, err := getKid(ctx, s.Store, kidID); err != nil {
		return nil, err
	}
	return s.Goals(ctx, s.Store, kidID)
}

// ListRequests lists withdrawal requests, newest first. An empty KidID
// matches every kid.
func (s *Service) ListRequests(ctx context.Context, f store.RequestFilter) ([]store.WithdrawalRequest, error) {
	return s.Store.ListRequests(ctx, f)
}

// JarByID returns one jar with its balance, split share and locks.
func (s *Service) JarByID(ctx context.Context, jarID string) (JarView, error) {
	j, err := s.Store.GetJar(ctx, jarID)
	if err != nil {
		return JarView{}, err
	}
	v, err := s.KidOverview(ctx, j.KidID, 0)
	if err != nil {
		return JarView{}, err
	}
	if jv, ok := v.Jar(jarID); ok {
		return jv, nil
	}
	if j.ArchivedAt != nil { // archived jars drop out of the balances
		return JarView{JarBalance: store.JarBalance{Jar: j}}, nil
	}
	return JarView{}, ErrNotFound
}

// GoalByID returns one goal with its progress. An archived goal shows no
// funding: it no longer draws on its jar.
func (s *Service) GoalByID(ctx context.Context, goalID string) (GoalView, error) {
	g, err := s.Store.GetGoal(ctx, goalID)
	if err != nil {
		return GoalView{}, err
	}
	if g.ArchivedAt != nil {
		gv := GoalView{Goal: g}
		if j, err := s.Store.GetJar(ctx, g.JarID); err == nil {
			gv.JarName = j.Name
		}
		return gv, nil
	}
	views, err := s.Goals(ctx, s.Store, g.KidID)
	if err != nil {
		return GoalView{}, err
	}
	for _, v := range views {
		if v.ID == goalID {
			return v, nil
		}
	}
	return GoalView{}, ErrNotFound
}

// EntryByID returns one ledger entry.
func (s *Service) EntryByID(ctx context.Context, id string) (store.LedgerEntry, error) {
	return s.Store.GetLedgerEntry(ctx, id)
}

// RequestByID returns one withdrawal request.
func (s *Service) RequestByID(ctx context.Context, id string) (store.WithdrawalRequest, error) {
	return s.Store.GetRequest(ctx, id)
}
