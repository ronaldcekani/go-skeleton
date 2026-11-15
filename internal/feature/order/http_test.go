package order

import (
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	openapi "github.com/example/go-skeleton/internal/gen/openapi"
)

func TestHTTPCreateAndGet(t *testing.T) {
	f := newFixture(t)
	h := NewHTTPHandler(f.svc)

	resp, err := h.CreateOrder(t.Context(), openapi.CreateOrderRequestObject{Body: &openapi.CreateOrderRequest{
		CustomerId: f.customer.ID,
		Currency:   "EUR",
		Lines:      []openapi.CreateOrderLine{{ItemId: f.widget, Quantity: 2}},
	}})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	created, ok := resp.(openapi.CreateOrder201JSONResponse)
	if !ok {
		t.Fatalf("response is %T, want a 201", resp)
	}
	want := openapi.OrderLine{ItemId: f.widget, Quantity: 2, UnitAmount: 1000, DiscountBasisPoints: 1000, Amount: 1800}
	if created.CustomerId != f.customer.ID || created.Currency != "EUR" || created.TotalAmount != 1800 ||
		len(created.Lines) != 1 || created.Lines[0] != want {
		t.Errorf("created = %+v", created)
	}

	got, err := h.GetOrder(t.Context(), openapi.GetOrderRequestObject{Id: created.Id})
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if order, ok := got.(openapi.GetOrder200JSONResponse); !ok || order.Id != created.Id || order.TotalAmount != 1800 {
		t.Errorf("got %+v, want the created order", got)
	}

	_, err = h.GetOrder(t.Context(), openapi.GetOrderRequestObject{Id: uuid.New()})
	if !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("unknown id: err = %v, want not found", err)
	}
}
