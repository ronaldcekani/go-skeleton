package policy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/platform/ratelimit"
)

type fakeAuthn struct{ calls int }

func (f *fakeAuthn) Authenticate(_ context.Context, token string) (domain.Principal, error) {
	f.calls++
	if token != "good" {
		return domain.Principal{}, domain.Unauthenticated("invalid or expired token")
	}
	return domain.Principal{Subject: "user-1"}, nil
}

type fakeLimiter struct {
	keys     []string
	decision ratelimit.Decision
	err      error
}

func (f *fakeLimiter) Allow(_ context.Context, key string, _ ratelimit.Limit) (ratelimit.Decision, error) {
	f.keys = append(f.keys, key)
	return f.decision, f.err
}

func newEnforcer(t *testing.T, authn Authenticator, limiter Limiter) *Enforcer {
	t.Helper()
	e, err := NewEnforcer(authn, limiter, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

var limited = Policy{Name: "write-items", Limit: &ratelimit.Limit{Requests: 1, Period: time.Minute}}

func TestEnforcePublicSkipsEverything(t *testing.T) {
	authn, limiter := &fakeAuthn{}, &fakeLimiter{}
	e := newEnforcer(t, authn, limiter)

	ctx, _, err := e.Enforce(t.Context(), Policy{Name: "open", Public: true}, "")
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if _, ok := domain.PrincipalFrom(ctx); ok {
		t.Error("public endpoint must not carry a principal")
	}
	if authn.calls != 0 || len(limiter.keys) != 0 {
		t.Error("public endpoint must not authenticate or count")
	}
}

func TestEnforceRequiresToken(t *testing.T) {
	e := newEnforcer(t, &fakeAuthn{}, &fakeLimiter{})
	for name, token := range map[string]string{"missing": "", "invalid": "bad"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := e.Enforce(t.Context(), Default, token); !domain.IsKind(err, domain.ErrKindUnauthenticated) {
				t.Errorf("err = %v, want unauthenticated", err)
			}
		})
	}
}

func TestEnforceAttachesPrincipalAndCountsPerSubject(t *testing.T) {
	limiter := &fakeLimiter{decision: ratelimit.Decision{Allowed: true}}
	e := newEnforcer(t, &fakeAuthn{}, limiter)

	ctx, _, err := e.Enforce(t.Context(), limited, "good")
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if p, ok := domain.PrincipalFrom(ctx); !ok || p.Subject != "user-1" {
		t.Errorf("principal = %+v, %v", p, ok)
	}
	if len(limiter.keys) != 1 || limiter.keys[0] != "write-items:user-1" {
		t.Errorf("limiter keys = %v, want [write-items:user-1]", limiter.keys)
	}

	// No limit on the policy: authenticated, not counted.
	if _, _, err := e.Enforce(t.Context(), Default, "good"); err != nil {
		t.Fatalf("Enforce default: %v", err)
	}
	if len(limiter.keys) != 1 {
		t.Error("an endpoint without a limit must not be counted")
	}
}

func TestEnforceRateLimited(t *testing.T) {
	e := newEnforcer(t, &fakeAuthn{}, &fakeLimiter{decision: ratelimit.Decision{RetryAfter: 7 * time.Second}})

	_, retryAfter, err := e.Enforce(t.Context(), limited, "good")
	if !domain.IsKind(err, domain.ErrKindRateLimited) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if retryAfter != 7*time.Second {
		t.Errorf("retryAfter = %v, want 7s", retryAfter)
	}
}

func TestEnforceFailsOpenWhenLimiterFails(t *testing.T) {
	e := newEnforcer(t, &fakeAuthn{}, &fakeLimiter{err: errors.New("redis down")})
	if _, _, err := e.Enforce(t.Context(), limited, "good"); err != nil {
		t.Errorf("Enforce = %v, want the request allowed", err)
	}
}

func TestEnforceWithAuthDisabled(t *testing.T) {
	limiter := &fakeLimiter{}
	e := newEnforcer(t, nil, limiter)
	if _, _, err := e.Enforce(t.Context(), limited, ""); err != nil {
		t.Errorf("Enforce = %v, want everything allowed when auth is disabled", err)
	}
	if len(limiter.keys) != 0 {
		t.Error("nothing can be counted without a subject")
	}
}

func TestTable(t *testing.T) {
	table, err := NewTable([]Rule{
		{Name: "read", HTTP: []string{"listItems"}, GRPC: []string{"item.v1.ItemService/ListItems"}, Public: true},
		{Name: "write", HTTP: []string{"createItem"}, Limit: limited.Limit},
	})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	if p := table.HTTP("listItems"); !p.Public || p.Name != "read" {
		t.Errorf("listItems = %+v", p)
	}
	if p := table.GRPC("item.v1.ItemService/ListItems"); !p.Public {
		t.Errorf("ListItems = %+v", p)
	}
	if p := table.HTTP("createItem"); p.Public || p.Limit == nil {
		t.Errorf("createItem = %+v", p)
	}
	if p := table.HTTP("deleteItem"); p.Public || p.Limit != nil || p.Name != Default.Name {
		t.Errorf("unlisted endpoint = %+v, want the default (token required, no limit)", p)
	}

	if p := table.HTTP("ListItems"); !p.Public {
		t.Errorf("operation IDs must match case-insensitively, got %+v", p)
	}
	if err := table.CheckHTTP([]string{"ListItems", "CreateItem", "GetItem"}); err != nil {
		t.Errorf("CheckHTTP: %v", err)
	}
	if err := table.CheckHTTP([]string{"listItems"}); err == nil {
		t.Error("CheckHTTP must reject a rule naming an unknown operation")
	}
	if err := table.CheckGRPC([]string{"item.v1.ItemService/GetItem"}); err == nil {
		t.Error("CheckGRPC must reject a rule naming an unknown method")
	}

	if _, err := NewTable([]Rule{{Name: "a", HTTP: []string{"x"}}, {Name: "b", HTTP: []string{"x"}}}); err == nil {
		t.Error("NewTable must reject an endpoint claimed by two rules")
	}
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer abc":  "abc",
		"bearer abc":  "abc",
		"BEARER  abc": "abc",
		"Basic abc":   "",
		"Bearer":      "",
		"":            "",
		"abc":         "",
	} {
		if got := BearerToken(header); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}
