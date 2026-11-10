package grpcserver

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/example/go-skeleton/internal/domain"
)

// codeFromKind is the single place where domain error kinds become gRPC codes.
func codeFromKind(kind domain.ErrKind) codes.Code {
	switch kind {
	case domain.ErrKindInvalidInput:
		return codes.InvalidArgument
	case domain.ErrKindNotFound:
		return codes.NotFound
	case domain.ErrKindConflict:
		return codes.AlreadyExists
	case domain.ErrKindUnauthenticated:
		return codes.Unauthenticated
	case domain.ErrKindPermissionDenied:
		return codes.PermissionDenied
	case domain.ErrKindRateLimited:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}

// errorInterceptor translates the error a handler returns into a gRPC status
// based on its domain kind, so handlers only return errors and never pick
// codes themselves. Internal errors are logged with their cause and answered
// with a generic message.
func errorInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		resp, err := next(ctx, req)
		if err == nil {
			return resp, nil
		}
		return nil, toStatusError(ctx, err, info.FullMethod, log)
	}
}

// errorStreamInterceptor is errorInterceptor for streaming RPCs.
func errorStreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		err := next(srv, ss)
		if err == nil {
			return nil
		}
		return toStatusError(ss.Context(), err, info.FullMethod, log)
	}
}

type grpcStatusError interface {
	error
	GRPCStatus() *status.Status
}

func toStatusError(ctx context.Context, err error, method string, log *slog.Logger) error {
	// Already a gRPC status, e.g. from the recovery interceptor.
	if _, ok := errors.AsType[grpcStatusError](err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	}

	kind := domain.KindOf(err)
	if kind == domain.ErrKindInternal {
		log.ErrorContext(ctx, "request failed", slog.String("method", method), slog.Any("error", err))
	}
	return status.Error(codeFromKind(kind), domain.PublicMessage(err))
}
