package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/platform/ratelimit"
	"github.com/example/go-skeleton/internal/server/grpcserver"
	"github.com/example/go-skeleton/internal/server/httpserver"
	"github.com/example/go-skeleton/internal/server/policy"
)

type servers struct {
	components []component
	onReady    []func(ready bool)
}

// wireServers builds the HTTP and gRPC servers and binds their ports, so a
// port conflict fails the build instead of surfacing after the app reported
// ready.
func wireServers(ctx context.Context, cfg config.Config, p *platform, svc *services, appReady func() bool, log *slog.Logger, cleanup *cleanupStack) (*servers, error) {
	policies, enforcer, err := wirePolicy(cfg, p, log)
	if err != nil {
		return nil, err
	}

	ready := func(ctx context.Context) error {
		if !appReady() {
			return errors.New("not ready")
		}
		if err := p.db.PingContext(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := p.redis.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	}

	httpSrv, err := httpserver.New(httpserver.Options{
		Host:              cfg.HTTP.Host,
		Port:              cfg.HTTP.Port,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}, svc.items, ready, enforcer, policies, log)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	httpLis, err := listen(ctx, "http", httpSrv.Addr, cleanup)
	if err != nil {
		return nil, err
	}

	grpcSrv, err := grpcserver.New(grpcserver.Options{Reflection: cfg.GRPC.Reflection}, svc.items, enforcer, policies, log)
	if err != nil {
		return nil, fmt.Errorf("grpc: %w", err)
	}
	grpcLis, err := listen(ctx, "grpc", net.JoinHostPort(cfg.GRPC.Host, strconv.Itoa(cfg.GRPC.Port)), cleanup)
	if err != nil {
		return nil, err
	}

	return &servers{
		onReady: []func(bool){grpcSrv.SetServing},
		components: []component{
			{
				name: "http server " + httpLis.Addr().String(),
				start: func() error {
					if err := httpSrv.Serve(httpLis); !errors.Is(err, http.ErrServerClosed) {
						return err
					}
					return nil
				},
				// Stops accepting connections and waits for in-flight
				// requests; whatever is left at the deadline is cut off.
				stop: func(ctx context.Context) error {
					if err := httpSrv.Shutdown(ctx); err != nil {
						_ = httpSrv.Close()
						return err
					}
					return nil
				},
			},
			{
				name:  "grpc server " + grpcLis.Addr().String(),
				start: func() error { return grpcSrv.Serve(grpcLis) },
				stop: func(ctx context.Context) error {
					stopped := make(chan struct{})
					go func() {
						grpcSrv.GracefulStop()
						close(stopped)
					}()
					select {
					case <-stopped:
						return nil
					case <-ctx.Done():
						grpcSrv.Stop()
						return ctx.Err()
					}
				},
			},
		},
	}, nil
}

// wirePolicy turns the endpoint rules from the config into the policy table
// and the enforcer both servers share.
func wirePolicy(cfg config.Config, p *platform, log *slog.Logger) (policy.Table, *policy.Enforcer, error) {
	rules := make([]policy.Rule, 0, len(cfg.Endpoints.Rules))
	for _, r := range cfg.Endpoints.Rules {
		rule := policy.Rule{Name: r.Name, HTTP: r.HTTP, GRPC: r.GRPC, Public: r.Public}
		if l := r.RateLimit; l != nil {
			rule.Limit = &ratelimit.Limit{Requests: l.Requests, Period: l.Period, Burst: l.Burst}
		}
		rules = append(rules, rule)
	}
	policies, err := policy.NewTable(rules)
	if err != nil {
		return policy.Table{}, nil, fmt.Errorf("endpoint rules: %w", err)
	}

	// Typed nil pointers must not end up in the interfaces: the enforcer
	// checks them against nil to see what is enabled.
	var authn policy.Authenticator
	if p.authenticator != nil {
		authn = p.authenticator
	}
	var limiter policy.Limiter
	if p.limiter != nil {
		limiter = p.limiter
	}
	enforcer, err := policy.NewEnforcer(authn, limiter, log)
	if err != nil {
		return policy.Table{}, nil, err
	}
	return policies, enforcer, nil
}

// listen binds addr. The cleanup step only matters when Build fails before
// the server takes ownership of the listener; afterwards the listener is
// already closed and the error is ignored.
func listen(ctx context.Context, name, addr string, cleanup *cleanupStack) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", name, err)
	}
	cleanup.push(name+" listener", func(context.Context) error {
		if err := lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	return lis, nil
}
