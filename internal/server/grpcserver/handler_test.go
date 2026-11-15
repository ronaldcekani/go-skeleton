package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
	"github.com/example/go-skeleton/internal/server/policy"
)

// fakeItems records the input of the last call and answers with item.
type fakeItems struct {
	item   domain.Item
	update domain.UpdateItemInput
	search domain.SearchItemsInput
}

func (f *fakeItems) Create(context.Context, domain.CreateItemInput) (domain.Item, error) {
	return f.item, nil
}
func (f *fakeItems) Get(context.Context, uuid.UUID) (domain.Item, error) { return f.item, nil }
func (f *fakeItems) List(context.Context, domain.ListItemsInput) ([]domain.Item, int64, error) {
	return []domain.Item{f.item}, 1, nil
}
func (f *fakeItems) Update(_ context.Context, _ uuid.UUID, in domain.UpdateItemInput) (domain.Item, error) {
	f.update = in
	return f.item, in.Validate()
}
func (f *fakeItems) Search(_ context.Context, in domain.SearchItemsInput) ([]domain.Item, int64, error) {
	f.search = in
	return []domain.Item{f.item}, 1, nil
}
func (f *fakeItems) Delete(context.Context, uuid.UUID) error { return nil }

// newTestClient serves the full gRPC stack (interceptors included) with
// authentication disabled, over an in-memory connection.
func newTestClient(t *testing.T, items ItemService) itemv1.ItemServiceClient {
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
	srv, err := New(Options{}, items, enforcer, table, log)
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
	return itemv1.NewItemServiceClient(conn)
}

func TestUpdateItem(t *testing.T) {
	items := &fakeItems{item: domain.Item{ID: uuid.New(), Name: "gadget"}}
	client := newTestClient(t, items)

	resp, err := client.UpdateItem(t.Context(), &itemv1.UpdateItemRequest{
		Id:          items.item.ID.String(),
		Description: proto.String("new"),
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if resp.GetItem().GetId() != items.item.ID.String() {
		t.Errorf("id = %s, want %s", resp.GetItem().GetId(), items.item.ID)
	}
	if items.update.Name != nil || items.update.Description == nil || *items.update.Description != "new" {
		t.Errorf("service got %+v, want only description set", items.update)
	}

	for name, req := range map[string]*itemv1.UpdateItemRequest{
		"bad id":    {Id: "nope", Name: proto.String("x")},
		"no fields": {Id: items.item.ID.String()},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.UpdateItem(t.Context(), req)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestSearchItems(t *testing.T) {
	items := &fakeItems{item: domain.Item{ID: uuid.New(), Name: "widget"}}
	client := newTestClient(t, items)

	resp, err := client.SearchItems(t.Context(), &itemv1.SearchItemsRequest{Query: "wid", Limit: 5})
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if resp.GetTotal() != 1 || len(resp.GetItems()) != 1 {
		t.Errorf("got %d items, total %d; want 1, 1", len(resp.GetItems()), resp.GetTotal())
	}
	if items.search != (domain.SearchItemsInput{Query: "wid", Limit: 5}) {
		t.Errorf("service got %+v", items.search)
	}
}
