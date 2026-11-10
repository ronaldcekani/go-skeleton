package domain

import (
	"context"
	"slices"
)

// Principal is the authenticated caller, taken from a verified access token.
type Principal struct {
	Subject  string // the token's "sub" claim: the stable user ID
	Username string
	Email    string
	ClientID string // the client the token was issued to ("azp")
	Roles    []string
	Scopes   []string
}

func (p Principal) HasRole(role string) bool { return slices.Contains(p.Roles, role) }

func (p Principal) HasScope(scope string) bool { return slices.Contains(p.Scopes, scope) }

type principalKey struct{}

// WithPrincipal returns a context carrying the authenticated caller.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated caller. ok is false for public
// endpoints and when authentication is disabled.
func PrincipalFrom(ctx context.Context) (p Principal, ok bool) {
	p, ok = ctx.Value(principalKey{}).(Principal)
	return p, ok
}
