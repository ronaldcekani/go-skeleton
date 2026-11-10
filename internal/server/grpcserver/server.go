// Package grpcserver is the gRPC transport. Service stubs come from code
// generated out of api/proto; this package adapts them to the service layer.
package grpcserver

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
)

type Options struct {
	Reflection bool
}

// Server bundles the gRPC server with its health service so the app can flip
// the serving status during shutdown.
type Server struct {
	*grpc.Server
	Health *health.Server
}

// New builds the gRPC server. The caller owns its lifecycle (Serve/GracefulStop).
func New(opts Options, items ItemService, log *slog.Logger) *Server {
	srv := grpc.NewServer(
		// Traces and metrics for every RPC except health checks.
		grpc.StatsHandler(otelgrpc.NewServerHandler(
			otelgrpc.WithFilter(filters.Not(filters.HealthCheck())),
		)),
		// Outermost first: the request log sees the final status, the
		// error interceptor translates what handlers return, and recovery
		// sits next to the handler.
		grpc.ChainUnaryInterceptor(
			loggingInterceptor(log),
			errorInterceptor(log),
			recoveryInterceptor(log),
		),
	)

	itemv1.RegisterItemServiceServer(srv, &itemServer{items: items})

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus(itemv1.ItemService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)

	if opts.Reflection {
		reflection.Register(srv)
	}
	return &Server{Server: srv, Health: healthSrv}
}

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if info.FullMethod == healthpb.Health_Check_FullMethodName {
			return next(ctx, req)
		}
		start := time.Now()
		resp, err := next(ctx, req)

		code := status.Code(err)
		level := slog.LevelInfo
		switch code {
		case codes.Internal, codes.Unknown, codes.DataLoss, codes.Unavailable, codes.DeadlineExceeded:
			level = slog.LevelError
		}
		log.LogAttrs(ctx, level, "grpc request",
			slog.String("method", info.FullMethod),
			slog.String("code", code.String()),
			slog.Duration("duration", time.Since(start)),
		)
		return resp, err
	}
}

// recoveryInterceptor turns a handler panic into codes.Internal instead of
// crashing the process.
func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if v := recover(); v != nil {
				log.ErrorContext(ctx, "grpc handler panicked",
					slog.String("method", info.FullMethod),
					slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return next(ctx, req)
	}
}
