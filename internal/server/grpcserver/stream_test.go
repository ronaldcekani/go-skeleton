package grpcserver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeStream) Context() context.Context { return s.ctx }

// A panic in a streaming handler must become codes.Internal and be logged,
// like in a unary one, instead of crashing the process.
func TestStreamInterceptorsRecoverAndLog(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	info := &grpc.StreamServerInfo{FullMethod: "/item.v1.ItemService/WatchItems"}
	stream := &fakeStream{ctx: t.Context()}
	handler := func(any, grpc.ServerStream) error { panic("boom") }

	// The same order as in New: logging outermost, recovery innermost.
	recovered := func(srv any, ss grpc.ServerStream) error {
		return recoveryStreamInterceptor(log)(srv, ss, info, handler)
	}
	err := loggingStreamInterceptor(log)(nil, stream, info, recovered)

	if status.Code(err) != codes.Internal {
		t.Fatalf("err = %v, want codes.Internal", err)
	}
	if strings.Contains(status.Convert(err).Message(), "boom") {
		t.Errorf("the panic value leaked to the client: %v", err)
	}
	for _, want := range []string{"grpc handler panicked", "boom", "grpc request", "code=Internal", "WatchItems"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log does not contain %q:\n%s", want, logs.String())
		}
	}
}
