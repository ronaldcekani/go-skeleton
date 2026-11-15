package order

import (
	"testing"

	"github.com/google/uuid"

	orderv1 "github.com/example/go-skeleton/internal/gen/proto/order/v1"
	"github.com/example/go-skeleton/internal/testkit/mapcheck"
)

// See the same test in the item feature.
func TestMappersAreComplete(t *testing.T) {
	t.Run("rows to customer", func(t *testing.T) {
		type rows struct {
			Customer customerRow
			GroupIDs []uuid.UUID
		}
		mapcheck.Check(t, func(r rows) Customer { return toCustomer(r.Customer, r.GroupIDs) })
	})
	t.Run("row to price", func(t *testing.T) {
		mapcheck.Check(t, priceRow.toPrice)
	})
	t.Run("row to discount", func(t *testing.T) {
		mapcheck.Check(t, discountRow.toDiscount)
	})
	t.Run("row to rate", func(t *testing.T) {
		mapcheck.Check(t, func(r rateRow) Rate {
			rate, err := r.toRate()
			if err != nil {
				t.Fatal(err)
			}
			return rate
		})
	})
	t.Run("rows to order", func(t *testing.T) {
		type rows struct {
			Order orderRow
			Lines []lineRow
		}
		mapcheck.Check(t, func(r rows) Order { return toOrder(r.Order, r.Lines) })
	})
	t.Run("order to HTTP response", func(t *testing.T) {
		mapcheck.Check(t, toAPIOrder)
	})
	t.Run("order to gRPC response", func(t *testing.T) {
		mapcheck.Check(t, func(o Order) *orderv1.Order { return toProto(o) })
	})
}
