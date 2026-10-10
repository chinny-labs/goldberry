package service

import (
	"context"
	"slices"
	"strings"

	"github.com/chinny/goldberry/internal/auth"
	"github.com/chinny/goldberry/internal/store"
)

// API token scopes (ADR 0015). Scopes are separate grants: money does not
// imply write. Every token can read.
const (
	ScopeRead  = "read"
	ScopeWrite = "write" // jars, goals, schedules; no money movement
	ScopeMoney = "money" // post, remove and reverse ledger entries
)

// TokenPrefix marks a Goldberry API token, so leaked ones are easy to spot
// and secret scanners can match them.
const TokenPrefix = "gb_"

// MaxTokensPerUser caps live tokens per admin.
const MaxTokensPerUser = 20

// TokenInput is a new token's label and scopes.
type TokenInput struct {
	Label  string
	Scopes []string
}

// HasScope reports whether t grants scope.
func HasScope(t store.APIToken, scope string) bool { return slices.Contains(t.Scopes, scope) }

func cleanScopes(in []string) ([]string, error) {
	got := map[string]bool{ScopeRead: true}
	for _, sc := range in {
		sc = strings.TrimSpace(sc)
		switch sc {
		case ScopeRead, ScopeWrite, ScopeMoney:
			got[sc] = true
		case "":
		default:
			return nil, invalid("scopes", "Unknown scope “%s”.", sc)
		}
	}
	var out []string
	for _, sc := range []string{ScopeRead, ScopeWrite, ScopeMoney} {
		if got[sc] {
			out = append(out, sc)
		}
	}
	return out, nil
}

// CreateAPIToken mints a token owned by actor and returns its secret, which
// is shown once and never stored.
func (s *Service) CreateAPIToken(ctx context.Context, actor store.User, in TokenInput) (string, store.APIToken, error) {
	if err := requireAdmin(actor); err != nil {
		return "", store.APIToken{}, err
	}
	label, err := cleanText("label", in.Label, true)
	if err != nil {
		return "", store.APIToken{}, err
	}
	scopes, err := cleanScopes(in.Scopes)
	if err != nil {
		return "", store.APIToken{}, err
	}
	secret := TokenPrefix + auth.NewToken()
	t := store.APIToken{
		ID: store.NewID(), TokenHash: auth.HashToken(secret), UserID: actor.ID,
		Label: label, Scopes: scopes, CreatedAt: s.now(),
	}
	err = s.Store.Tx(ctx, "", func(tx store.Tx) error {
		live, err := tx.ListAPITokens(ctx, actor.ID)
		if err != nil {
			return err
		}
		if len(live) >= MaxTokensPerUser {
			return invalid("", "You already have %d tokens. Revoke one first.", MaxTokensPerUser)
		}
		if err := tx.CreateAPIToken(ctx, t); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor.ID, "token.create", actor.ID, map[string]any{"token": t.ID, "label": label, "scopes": scopes})
	})
	if err != nil {
		return "", store.APIToken{}, err
	}
	return secret, t, nil
}

// APITokens lists a user's live tokens. Admins see anyone's; a user sees
// only their own.
func (s *Service) APITokens(ctx context.Context, actor store.User, userID string) ([]store.APIToken, error) {
	if actor.ID != userID {
		if err := requireAdmin(actor); err != nil {
			return nil, err
		}
	}
	return s.Store.ListAPITokens(ctx, userID)
}

// RevokeAPIToken revokes one token. Its owner or any admin may.
func (s *Service) RevokeAPIToken(ctx context.Context, actor store.User, tokenID string) error {
	return s.Store.Tx(ctx, "", func(tx store.Tx) error {
		t, err := tx.GetAPIToken(ctx, tokenID)
		if err != nil {
			return err
		}
		if t.UserID != actor.ID {
			if err := requireAdmin(actor); err != nil {
				return err
			}
		}
		if t.RevokedAt != nil {
			return nil
		}
		if err := tx.RevokeAPIToken(ctx, t.ID, s.now()); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor.ID, "token.revoke", t.UserID, map[string]any{"token": t.ID, "label": t.Label})
	})
}

// AuthenticateToken resolves a bearer token to its owner. A token works only
// while it is unrevoked and its owner is an active admin. Unknown, revoked
// and owner-disabled tokens all look the same: ErrNotFound.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (store.User, store.APIToken, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return store.User{}, store.APIToken{}, ErrNotFound
	}
	t, err := s.Store.GetAPITokenByHash(ctx, auth.HashToken(secret))
	if err != nil {
		return store.User{}, store.APIToken{}, err
	}
	if t.RevokedAt != nil {
		return store.User{}, store.APIToken{}, ErrNotFound
	}
	u, err := s.Store.GetUser(ctx, t.UserID)
	if err != nil || !u.IsAdmin() || u.Disabled() {
		return store.User{}, store.APIToken{}, ErrNotFound
	}
	now := s.now()
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= touchEvery {
		t.LastUsedAt = &now
		if err := s.Store.Tx(ctx, "", func(tx store.Tx) error { return tx.TouchAPIToken(ctx, t.ID, now) }); err != nil {
			s.Log.Warn("touch api token", "err", err)
		}
	}
	return u, t, nil
}
