package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
	"github.com/example/go-skeleton/internal/server/policy"
)

// stubItems answers GetItem with a domain error; every other method is
// unimplemented.
type stubItems struct {
	itemv1.UnimplementedItemServiceServer
}

func (stubItems) GetItem(context.Context, *itemv1.GetItemRequest) (*itemv1.GetItemResponse, error) {
	return nil, domain.NotFound("item not found")
}

// newTestConn serves the full gRPC stack (interceptors included) with
// authentication disabled, over an in-memory connection.
func newTestConn(t *testing.T) (*Server, *grpc.ClientConn) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer, err := policy.NewEnforcer(nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.NewTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	register := func(r grpc.ServiceRegistrar) { itemv1.RegisterItemServiceServer(r, stubItems{}) }
	srv, err := New(Options{}, []Registration{register}, enforcer, table, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn
}

// An error a registered service returns is answered according to its domain
// kind.
func TestServiceErrorsBecomeStatuses(t *testing.T) {
	_, conn := newTestConn(t)

	_, err := itemv1.NewItemServiceClient(conn).GetItem(t.Context(), &itemv1.GetItemRequest{})
	if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "item not found" {
		t.Errorf("err = %v, want NotFound with the error's public message", err)
	}
}

// The health of every registered service follows the app's readiness.
func TestSetServingCoversRegisteredServices(t *testing.T) {
	srv, conn := newTestConn(t)
	health := healthpb.NewHealthClient(conn)
	check := func() healthpb.HealthCheckResponse_ServingStatus {
		t.Helper()
		resp, err := health.Check(t.Context(), &healthpb.HealthCheckRequest{Service: "item.v1.ItemService"})
		if err != nil {
			return healthpb.HealthCheckResponse_UNKNOWN
		}
		return resp.GetStatus()
	}

	srv.SetServing(true)
	if got := check(); got != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("after SetServing(true): %v, want SERVING", got)
	}
	srv.SetServing(false)
	if got := check(); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("after SetServing(false): %v, want NOT_SERVING", got)
	}
}
