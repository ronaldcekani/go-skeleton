package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/example/go-skeleton/internal/domain"
)

func TestErrorInterceptor(t *testing.T) {
	intercept := errorInterceptor(slog.New(slog.NewTextHandler(io.Discard, nil)))
	info := &grpc.UnaryServerInfo{FullMethod: "/item.v1.ItemService/GetItem"}

	for name, tc := range map[string]struct {
		err     error
		code    codes.Code
		message string
	}{
		"invalid input":       {domain.InvalidInput("name is required"), codes.InvalidArgument, "name is required"},
		"not found":           {domain.NotFound("item not found"), codes.NotFound, "item not found"},
		"wrapped not found":   {fmt.Errorf("get: %w", domain.NotFound("item not found")), codes.NotFound, "item not found"},
		"conflict":            {domain.Conflict("name already taken"), codes.AlreadyExists, "name already taken"},
		"unauthenticated":     {domain.NewError(domain.ErrKindUnauthenticated, "sign in"), codes.Unauthenticated, "sign in"},
		"permission denied":   {domain.NewError(domain.ErrKindPermissionDenied, "not yours"), codes.PermissionDenied, "not yours"},
		"unknown error":       {errors.New("pq: password is hunter2"), codes.Internal, "internal server error"},
		"deadline exceeded":   {context.DeadlineExceeded, codes.DeadlineExceeded, "deadline exceeded"},
		"canceled":            {context.Canceled, codes.Canceled, "request canceled"},
		"status passes as-is": {status.Error(codes.ResourceExhausted, "slow down"), codes.ResourceExhausted, "slow down"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := intercept(t.Context(), nil, info, func(context.Context, any) (any, error) {
				return nil, tc.err
			})
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("err = %v, want a gRPC status", err)
			}
			if st.Code() != tc.code || st.Message() != tc.message {
				t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), tc.code, tc.message)
			}
		})
	}

	resp, err := intercept(t.Context(), nil, info, func(context.Context, any) (any, error) { return "ok", nil })
	if err != nil || resp != "ok" {
		t.Errorf("success passed through as (%v, %v), want (ok, nil)", resp, err)
	}
}
