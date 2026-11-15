// Package order is the order feature. Creating an order is the example of a
// use case that reads a lot and returns little: it loads the customer and
// their groups, the item prices, the discounts and the exchange rates, and
// out of all that comes one Order.
//
// Customer, Price, Discount and Rate (pricing.go) are this feature's own
// view of that data: only what pricing needs, in the shape pricing needs it.
// They are not the types of whichever feature owns customers or prices, and
// they are not rows either. The rules in pricing.go are plain functions over
// them, tested without a database.
package order

import (
	"time"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

const (
	MaxLines    = 100
	MaxQuantity = 10000
)

// Currency is an ISO 4217 code such as "EUR".
type Currency string

func (c Currency) valid() bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// Order is a priced order. All amounts are in minor units (cents) of
// Currency.
type Order struct {
	ID          uuid.UUID
	CustomerID  uuid.UUID
	Currency    Currency
	Lines       []Line
	TotalAmount int64 // sum of the line amounts
	CreatedAt   time.Time
}

// Line keeps the price and discount it was sold at, so later changes to
// prices, discounts or rates do not rewrite past orders.
type Line struct {
	ItemID              uuid.UUID
	Quantity            int
	UnitAmount          int64 // price of one unit, before discount
	DiscountBasisPoints int   // 100 basis points are 1%
	Amount              int64 // what the line costs after the discount
}

// CreateInput is what a caller provides to create an order.
type CreateInput struct {
	CustomerID uuid.UUID
	Currency   Currency // what the order is priced in
	Lines      []LineInput
}

type LineInput struct {
	ItemID   uuid.UUID
	Quantity int
}

// Validate checks the rules that depend only on the input itself. Whether
// the customer exists and the items can be priced is checked by the service.
func (in CreateInput) Validate() error {
	switch {
	case in.CustomerID == uuid.Nil:
		return domain.InvalidInput("customer_id is required")
	case !in.Currency.valid():
		return domain.InvalidInput("currency must be a three-letter ISO 4217 code")
	case len(in.Lines) == 0:
		return domain.InvalidInput("at least one line is required")
	case len(in.Lines) > MaxLines:
		return domain.InvalidInput("an order can have at most %d lines", MaxLines)
	}
	seen := make(map[uuid.UUID]bool, len(in.Lines))
	for _, l := range in.Lines {
		switch {
		case l.ItemID == uuid.Nil:
			return domain.InvalidInput("item_id is required on every line")
		case l.Quantity < 1 || l.Quantity > MaxQuantity:
			return domain.InvalidInput("quantity must be between 1 and %d", MaxQuantity)
		case seen[l.ItemID]:
			return domain.InvalidInput("item %s is on more than one line", l.ItemID)
		}
		seen[l.ItemID] = true
	}
	return nil
}
