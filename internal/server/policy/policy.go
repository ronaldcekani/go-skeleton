// Package policy decides, for one request, whether the caller is
// authenticated and within their rate limit. It is transport-neutral: the
// HTTP middleware and the gRPC interceptor both call Enforcer.Enforce, so the
// two transports cannot drift apart.
package policy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/platform/ratelimit"
)

// Rule applies one policy to a set of HTTP operations and gRPC methods.
type Rule struct {
	Name   string
	HTTP   []string // OpenAPI operationIds
	GRPC   []string // package.Service/Method
	Public bool
	Limit  *ratelimit.Limit
}

// Policy is what applies to a single endpoint.
type Policy struct {
	Name   string // the rule's name; also the rate limit bucket
	Public bool
	Limit  *ratelimit.Limit
}

// Default applies to every endpoint no rule mentions: a valid token is
// required and there is no rate limit.
var Default = Policy{Name: "default"}

// Table maps endpoints to policies.
type Table struct {
	http map[string]Policy
	grpc map[string]Policy
}

// NewTable indexes the rules and rejects an endpoint claimed by two rules.
func NewTable(rules []Rule) (Table, error) {
	t := Table{http: map[string]Policy{}, grpc: map[string]Policy{}}
	var errs []error
	add := func(index map[string]Policy, kind, endpoint string, p Policy) {
		if prev, dup := index[endpoint]; dup {
			errs = append(errs, fmt.Errorf("%s %q is in both rule %q and rule %q", kind, endpoint, prev.Name, p.Name))
			return
		}
		index[endpoint] = p
	}
	for _, r := range rules {
		p := Policy{Name: r.Name, Public: r.Public, Limit: r.Limit}
		for _, op := range r.HTTP {
			add(t.http, "http operation", strings.ToLower(op), p)
		}
		for _, m := range r.GRPC {
			add(t.grpc, "grpc method", m, p)
		}
	}
	return t, errors.Join(errs...)
}

// HTTP returns the policy for an OpenAPI operationId. Operation IDs are
// matched case-insensitively, because the code generator changes their case
// (listItems in the spec becomes ListItems in the generated code).
func (t Table) HTTP(operationID string) Policy { return lookup(t.http, strings.ToLower(operationID)) }

// GRPC returns the policy for a method named package.Service/Method.
func (t Table) GRPC(method string) Policy { return lookup(t.grpc, method) }

func lookup(index map[string]Policy, endpoint string) Policy {
	if p, ok := index[endpoint]; ok {
		return p
	}
	return Default
}

// CheckHTTP fails if a rule names an operationId the server does not have,
// which catches typos and stale rules at startup.
func (t Table) CheckHTTP(known []string) error {
	return checkKnown("http operation", t.http, known, strings.EqualFold)
}

// CheckGRPC is CheckHTTP for gRPC methods.
func (t Table) CheckGRPC(known []string) error {
	return checkKnown("grpc method", t.grpc, known, func(a, b string) bool { return a == b })
}

func checkKnown(kind string, index map[string]Policy, known []string, equal func(a, b string) bool) error {
	var errs []error
	for endpoint, p := range index {
		if !slices.ContainsFunc(known, func(k string) bool { return equal(k, endpoint) }) {
			errs = append(errs, fmt.Errorf("rule %q: unknown %s %q", p.Name, kind, endpoint))
		}
	}
	return errors.Join(errs...)
}

// Authenticator verifies a bearer token.
type Authenticator interface {
	Authenticate(ctx context.Context, rawToken string) (domain.Principal, error)
}

// Limiter counts one request against a key.
type Limiter interface {
	Allow(ctx context.Context, key string, limit ratelimit.Limit) (ratelimit.Decision, error)
}

const instrumentationName = "github.com/example/go-skeleton/internal/server/policy"

// Enforcer applies policies. A nil Authenticator disables authentication
// (and with it rate limiting, which needs the token subject); a nil Limiter
// disables rate limiting only.
type Enforcer struct {
	authn   Authenticator
	limiter Limiter
	log     *slog.Logger

	rejected      metric.Int64Counter
	limiterErrors metric.Int64Counter
}

func NewEnforcer(authn Authenticator, limiter Limiter, log *slog.Logger) (*Enforcer, error) {
	meter := otel.Meter(instrumentationName)
	rejected, err := meter.Int64Counter("policy.requests.rejected",
		metric.WithDescription("Requests rejected by authentication or rate limiting"))
	if err != nil {
		return nil, fmt.Errorf("create policy.requests.rejected counter: %w", err)
	}
	limiterErrors, err := meter.Int64Counter("policy.ratelimit.errors",
		metric.WithDescription("Rate limit checks that failed and were allowed through"))
	if err != nil {
		return nil, fmt.Errorf("create policy.ratelimit.errors counter: %w", err)
	}
	return &Enforcer{authn: authn, limiter: limiter, log: log, rejected: rejected, limiterErrors: limiterErrors}, nil
}

// Enforce checks one request against its policy. On success the returned
// context carries the caller's domain.Principal (except for public endpoints).
// On failure the error is of kind Unauthenticated or RateLimited; for the
// latter, retryAfter says when the caller may try again.
func (e *Enforcer) Enforce(ctx context.Context, p Policy, bearerToken string) (_ context.Context, retryAfter time.Duration, err error) {
	if p.Public || e.authn == nil {
		return ctx, 0, nil
	}

	if bearerToken == "" {
		e.reject(ctx, p, "unauthenticated")
		return ctx, 0, domain.Unauthenticated("missing bearer token")
	}
	principal, err := e.authn.Authenticate(ctx, bearerToken)
	if err != nil {
		e.reject(ctx, p, "unauthenticated")
		e.log.DebugContext(ctx, "token rejected", slog.Any("error", err))
		return ctx, 0, err
	}
	ctx = domain.WithPrincipal(ctx, principal)
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("enduser.id", principal.Subject))

	if p.Limit == nil || e.limiter == nil {
		return ctx, 0, nil
	}
	decision, err := e.limiter.Allow(ctx, p.Name+":"+principal.Subject, *p.Limit)
	if err != nil {
		// Fail open: a Redis outage must not take the API down with it.
		e.limiterErrors.Add(ctx, 1)
		e.log.WarnContext(ctx, "rate limit check failed, allowing request", slog.Any("error", err))
		return ctx, 0, nil
	}
	if !decision.Allowed {
		e.reject(ctx, p, "rate_limited")
		return ctx, decision.RetryAfter, domain.NewError(domain.ErrKindRateLimited, "rate limit exceeded")
	}
	return ctx, 0, nil
}

func (e *Enforcer) reject(ctx context.Context, p Policy, reason string) {
	e.rejected.Add(ctx, 1, metric.WithAttributes(
		attribute.String("rule", p.Name), attribute.String("reason", reason)))
}

// BearerToken extracts the token from an Authorization header value. It
// returns "" if the header is missing or is not a bearer credential.
func BearerToken(header string) string {
	const prefix = "bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
