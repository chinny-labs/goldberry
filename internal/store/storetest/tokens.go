package storetest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chinny-labs/goldberry/internal/service"
)

var tokenTests = []struct {
	name string
	fn   func(*testing.T, Opener)
}{
	{"APITokenLifecycle", testTokenLifecycle},
	{"APITokenOwnership", testTokenOwnership},
	{"APITokenDisabledOwner", testTokenDisabledOwner},
}

func testTokenLifecycle(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)

	secret, tok, err := f.Svc.CreateAPIToken(ctx, f.Mom, service.TokenInput{Label: " Home Assistant ", Scopes: []string{"money", "write", "money"}})
	must(t, err)
	if !strings.HasPrefix(secret, service.TokenPrefix) {
		t.Fatalf("secret %q lacks prefix", secret)
	}
	if tok.Label != "Home Assistant" || !slices.Equal(tok.Scopes, []string{"read", "write", "money"}) {
		t.Fatalf("token %+v: want trimmed label, read always granted, scopes deduped in order", tok)
	}

	// Only the hash is stored.
	if got, err := f.Store.GetAPIToken(ctx, tok.ID); err != nil || got.TokenHash == secret || got.TokenHash == "" {
		t.Fatalf("stored token %+v, err %v", got, err)
	}

	u, got, err := f.Svc.AuthenticateToken(ctx, secret)
	must(t, err)
	if u.ID != f.Mom.ID || got.ID != tok.ID || !service.HasScope(got, service.ScopeMoney) {
		t.Fatalf("authenticated as %s with %+v", u.Username, got)
	}
	if got.LastUsedAt == nil {
		t.Fatal("first use not recorded")
	}

	for _, bad := range []string{"", "gb_nope", secret + "x", strings.TrimPrefix(secret, service.TokenPrefix)} {
		if _, _, err := f.Svc.AuthenticateToken(ctx, bad); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("token %q: %v, want not found", bad, err)
		}
	}

	// last_used moves forward, but at most once a minute.
	first := *got.LastUsedAt
	f.Clock.Advance(10 * time.Second)
	_, _, err = f.Svc.AuthenticateToken(ctx, secret)
	must(t, err)
	if again, _ := f.Store.GetAPIToken(ctx, tok.ID); !again.LastUsedAt.Equal(first) {
		t.Fatal("last_used rewritten inside the touch window")
	}
	f.Clock.Advance(2 * time.Minute)
	_, _, err = f.Svc.AuthenticateToken(ctx, secret)
	must(t, err)
	if again, _ := f.Store.GetAPIToken(ctx, tok.ID); !again.LastUsedAt.After(first) {
		t.Fatal("last_used never advanced")
	}

	must(t, f.Svc.RevokeAPIToken(ctx, f.Mom, tok.ID))
	must(t, f.Svc.RevokeAPIToken(ctx, f.Mom, tok.ID)) // twice is fine
	if _, _, err := f.Svc.AuthenticateToken(ctx, secret); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("revoked token still works: %v", err)
	}
	if live, err := f.Svc.APITokens(ctx, f.Mom, f.Mom.ID); err != nil || len(live) != 0 {
		t.Fatalf("live tokens after revoke: %v, %v", live, err)
	}

	var ue *service.UserError
	if _, _, err := f.Svc.CreateAPIToken(ctx, f.Mom, service.TokenInput{Label: "x", Scopes: []string{"root"}}); !errors.As(err, &ue) {
		t.Fatalf("unknown scope: %v", err)
	}
	if _, _, err := f.Svc.CreateAPIToken(ctx, f.Mom, service.TokenInput{Label: "  "}); !errors.As(err, &ue) {
		t.Fatalf("empty label: %v", err)
	}
	for i := 0; i < service.MaxTokensPerUser; i++ {
		_, _, err := f.Svc.CreateAPIToken(ctx, f.Mom, service.TokenInput{Label: "t"})
		must(t, err)
	}
	if _, _, err := f.Svc.CreateAPIToken(ctx, f.Mom, service.TokenInput{Label: "one too many"}); !errors.As(err, &ue) {
		t.Fatalf("over the cap: %v", err)
	}
}

func testTokenOwnership(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)

	// Kids can't mint.
	if _, _, err := f.Svc.CreateAPIToken(ctx, f.Ava, service.TokenInput{Label: "sneaky"}); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("kid minted a token: %v", err)
	}

	secret, tok, err := f.Svc.CreateAPIToken(ctx, f.Dad, service.TokenInput{Label: "dad's"})
	must(t, err)
	if mine, err := f.Svc.APITokens(ctx, f.Mom, f.Dad.ID); err != nil || len(mine) != 1 {
		t.Fatalf("an admin can list another's tokens: %v, %v", mine, err)
	}
	if _, err := f.Svc.APITokens(ctx, f.Ava, f.Dad.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("kid listed an admin's tokens: %v", err)
	}
	if err := f.Svc.RevokeAPIToken(ctx, f.Ava, tok.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("kid revoked a token: %v", err)
	}

	// Actions attribute to the owner, and the audit log records both events.
	u, _, err := f.Svc.AuthenticateToken(ctx, secret)
	must(t, err)
	if u.ID != f.Dad.ID {
		t.Fatalf("token acts as %s, want dad", u.Username)
	}
	must(t, f.Svc.RevokeAPIToken(ctx, f.Mom, tok.ID)) // another admin may revoke
	log, err := f.Store.ListAudit(ctx, f.Dad.ID, 10)
	must(t, err)
	var actions []string
	for _, a := range log {
		actions = append(actions, a.Action)
	}
	for _, want := range []string{"token.create", "token.revoke"} {
		if !slices.Contains(actions, want) {
			t.Fatalf("audit %v lacks %s", actions, want)
		}
	}
	if err := f.Svc.RevokeAPIToken(ctx, f.Mom, "no-such-token"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("revoking a missing token: %v", err)
	}
}

func testTokenDisabledOwner(t *testing.T, open Opener) {
	ctx := context.Background()
	f := NewFixture(t, open)
	secret, _, err := f.Svc.CreateAPIToken(ctx, f.Dad, service.TokenInput{Label: "dad's"})
	must(t, err)
	must(t, f.Svc.SetDisabled(ctx, f.Mom, f.Dad.ID, true))
	if _, _, err := f.Svc.AuthenticateToken(ctx, secret); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("token of a disabled admin works: %v", err)
	}
	// Re-enabling must not revive it: disabling revoked the token for good.
	must(t, f.Svc.SetDisabled(ctx, f.Mom, f.Dad.ID, false))
	if _, _, err := f.Svc.AuthenticateToken(ctx, secret); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("token came back after re-enable: %v", err)
	}
	if live, _ := f.Store.ListAPITokens(ctx, f.Dad.ID); len(live) != 0 {
		t.Fatalf("live tokens after disable: %v", live)
	}
}
