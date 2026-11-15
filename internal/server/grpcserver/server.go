// Package grpcserver is the gRPC transport. Service stubs come from code
// generated out of api/proto. This package adds what every method shares
// (authentication, rate limiting, error translation, logging, health); the
// services themselves are implemented by the features.
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/server/policy"
)

type Options struct {
	Reflection bool
}

// Server bundles the gRPC server with its health service so the app can flip
// the serving status during shutdown.
type Server struct {
	*grpc.Server
	Health *health.Server

	services []string // the application's own services, by full name
}

// Registration adds one feature's service to the server.
type Registration func(grpc.ServiceRegistrar)

// New builds the gRPC server and registers the given services on it. The
// caller owns its lifecycle (Serve/GracefulStop).
func New(opts Options, services []Registration, enforcer *policy.Enforcer, policies policy.Table, log *slog.Logger) (*Server, error) {
	srv := grpc.NewServer(
		// Traces and metrics for every RPC except health checks.
		grpc.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithFilter(filters.Not(filters.HealthCheck())),
		)),
		// Outermost first: the request log sees the final status, the
		// error interceptor translates what the inner ones return, recovery
		// catches panics, and authorize authenticates and rate limits.
		grpc.ChainUnaryInterceptor(
			loggingInterceptor(log),
			errorInterceptor(log),
			recoveryInterceptor(log),
			authorize(enforcer, policies),
		),
		// The skeleton has no streaming RPCs of its own, but any that are
		// added get the same chain: they must not bypass authentication, and
		// a panic in one must not crash the process.
		grpc.ChainStreamInterceptor(
			loggingStreamInterceptor(log),
			errorStreamInterceptor(log),
			recoveryStreamInterceptor(log),
			authorizeStream(enforcer, policies),
		),
	)

	for _, register := range services {
		register(srv)
	}

	// Rules may only name methods of the application's own services, which
	// are the only ones registered so far.
	var names, methods []string
	for service, info := range srv.GetServiceInfo() {
		names = append(names, service)
		for _, m := range info.Methods {
			methods = append(methods, service+"/"+m.Name)
		}
	}
	if err := policies.CheckGRPC(methods); err != nil {
		return nil, fmt.Errorf("endpoint rules: %w", err)
	}

	// Not serving until the app reports ready.
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)

	if opts.Reflection {
		reflection.Register(srv)
	}
	return &Server{Server: srv, Health: healthSrv, services: names}, nil
}

// SetServing switches the health status of the whole server and of each
// application service.
func (s *Server) SetServing(serving bool) {
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		status = healthpb.HealthCheckResponse_SERVING
	}
	s.Health.SetServingStatus("", status)
	for _, name := range s.services {
		s.Health.SetServingStatus(name, status)
	}
}

// isInfrastructure reports whether the method belongs to the health or
// reflection service, which are never authenticated.
func isInfrastructure(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/") ||
		strings.HasPrefix(fullMethod, "/grpc.reflection.")
}

func bearerToken(ctx context.Context) string {
	if values := metadata.ValueFromIncomingContext(ctx, "authorization"); len(values) > 0 {
		return policy.BearerToken(values[0])
	}
	return ""
}

// enforce applies the method's policy and, when the caller is rate limited,
// tells them when to retry in a "retry-after" trailer (seconds).
func enforce(ctx context.Context, enforcer *policy.Enforcer, policies policy.Table, fullMethod string) (context.Context, error) {
	p := policies.GRPC(strings.TrimPrefix(fullMethod, "/"))
	ctx, retryAfter, err := enforcer.Enforce(ctx, p, bearerToken(ctx))
	if err != nil && domain.IsKind(err, domain.ErrKindRateLimited) {
		seconds := strconv.Itoa(int(math.Ceil(retryAfter.Seconds())))
		_ = grpc.SetTrailer(ctx, metadata.Pairs("retry-after", seconds))
	}
	return ctx, err
}

// authorize authenticates and rate limits the call according to the policy
// of its method. On success the context carries the caller's domain.Principal.
func authorize(enforcer *policy.Enforcer, policies policy.Table) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if isInfrastructure(info.FullMethod) {
			return next(ctx, req)
		}
		ctx, err := enforce(ctx, enforcer, policies, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// authorizeStream is authorize for streaming RPCs.
func authorizeStream(enforcer *policy.Enforcer, policies policy.Table) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if isInfrastructure(info.FullMethod) {
			return next(srv, ss)
		}
		ctx, err := enforce(ss.Context(), enforcer, policies, info.FullMethod)
		if err != nil {
			return err
		}
		return next(srv, &contextStream{ServerStream: ss, ctx: ctx})
	}
}

// contextStream replaces the context of a server stream.
type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextStream) Context() context.Context { return s.ctx }

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if isHealthMethod(info.FullMethod) {
			return next(ctx, req)
		}
		start := time.Now()
		resp, err := next(ctx, req)
		logCall(ctx, log, info.FullMethod, start, err)
		return resp, err
	}
}

// loggingStreamInterceptor is loggingInterceptor for streaming RPCs: one
// record per stream, when it ends.
func loggingStreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if isHealthMethod(info.FullMethod) {
			return next(srv, ss)
		}
		start := time.Now()
		err := next(srv, ss)
		logCall(ss.Context(), log, info.FullMethod, start, err)
		return err
	}
}

func isHealthMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/")
}

func logCall(ctx context.Context, log *slog.Logger, method string, start time.Time, err error) {
	code := status.Code(err)
	level := slog.LevelInfo
	switch code {
	case codes.Internal, codes.Unknown, codes.DataLoss, codes.Unavailable, codes.DeadlineExceeded:
		level = slog.LevelError
	}
	log.LogAttrs(ctx, level, "grpc request",
		slog.String("method", method),
		slog.String("code", code.String()),
		slog.Duration("duration", time.Since(start)),
	)
}

// recoveryInterceptor turns a handler panic into codes.Internal instead of
// crashing the process.
func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if v := recover(); v != nil {
				err = panicError(ctx, log, info.FullMethod, v)
			}
		}()
		return next(ctx, req)
	}
}

// recoveryStreamInterceptor is recoveryInterceptor for streaming RPCs.
func recoveryStreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) (err error) {
		defer func() {
			if v := recover(); v != nil {
				err = panicError(ss.Context(), log, info.FullMethod, v)
			}
		}()
		return next(srv, ss)
	}
}

func panicError(ctx context.Context, log *slog.Logger, method string, v any) error {
	log.ErrorContext(ctx, "grpc handler panicked",
		slog.String("method", method),
		slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
	return status.Error(codes.Internal, "internal server error")
}
