package order

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/example/go-skeleton/internal/domain"
	orderv1 "github.com/example/go-skeleton/internal/gen/proto/order/v1"
)

func TestGRPCCreateAndGet(t *testing.T) {
	f := newFixture(t)
	s := NewGRPCServer(f.svc)

	created, err := s.CreateOrder(t.Context(), &orderv1.CreateOrderRequest{
		CustomerId: f.customer.ID.String(),
		Currency:   "EUR",
		Lines:      []*orderv1.CreateOrderLine{{ItemId: f.widget.String(), Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	order := created.GetOrder()
	want := &orderv1.OrderLine{ItemId: f.widget.String(), Quantity: 2, UnitAmount: 1000, DiscountBasisPoints: 1000, Amount: 1800}
	if order.GetCustomerId() != f.customer.ID.String() || order.GetCurrency() != "EUR" || order.GetTotalAmount() != 1800 ||
		len(order.GetLines()) != 1 || !proto.Equal(order.GetLines()[0], want) {
		t.Errorf("created = %v", order)
	}

	got, err := s.GetOrder(t.Context(), &orderv1.GetOrderRequest{Id: order.GetId()})
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if !proto.Equal(got.GetOrder(), order) {
		t.Errorf("got %v, want %v", got.GetOrder(), order)
	}

	for name, req := range map[string]*orderv1.CreateOrderRequest{
		"bad customer id": {CustomerId: "nope", Currency: "EUR",
			Lines: []*orderv1.CreateOrderLine{{ItemId: f.widget.String(), Quantity: 1}}},
		"bad item id": {CustomerId: f.customer.ID.String(), Currency: "EUR",
			Lines: []*orderv1.CreateOrderLine{{ItemId: "nope", Quantity: 1}}},
		"no lines": {CustomerId: f.customer.ID.String(), Currency: "EUR"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateOrder(t.Context(), req); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("err = %v, want invalid input", err)
			}
		})
	}
	if _, err := s.GetOrder(t.Context(), &orderv1.GetOrderRequest{Id: uuid.NewString()}); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("unknown id: err = %v, want not found", err)
	}
}
