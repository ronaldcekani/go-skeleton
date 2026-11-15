package order

import (
	"math/big"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

// Customer is what pricing needs to know about who is ordering.
type Customer struct {
	ID       uuid.UUID
	GroupIDs []uuid.UUID
}

// Price is the list price of one unit of an item, in minor units of
// Currency.
type Price struct {
	ItemID     uuid.UUID
	Currency   Currency
	UnitAmount int64
}

// Discount takes BasisPoints off a line (100 basis points are 1%). A nil
// GroupID applies to every customer, a nil ItemID to every item.
type Discount struct {
	ID          uuid.UUID
	GroupID     *uuid.UUID
	ItemID      *uuid.UUID
	BasisPoints int
}

func (d Discount) appliesTo(c Customer, itemID uuid.UUID) bool {
	if d.ItemID != nil && *d.ItemID != itemID {
		return false
	}
	if d.GroupID == nil {
		return true
	}
	for _, id := range c.GroupIDs {
		if id == *d.GroupID {
			return true
		}
	}
	return false
}

// Rate converts amounts in Base to amounts in Quote: one unit of Base costs
// Value units of Quote. It assumes both currencies have the same number of
// decimal places.
type Rate struct {
	Base  Currency
	Quote Currency
	Value *big.Rat
}

const fullBasisPoints = 10000

// price turns the input into a priced order. It is the whole pricing rule,
// and it does no I/O: everything it needs is passed in.
//
// For each line:
//
//  1. the item's list price is converted to the order currency;
//  2. of the discounts that apply to the customer and the item, the largest
//     one wins (discounts do not add up);
//  3. amount = unit amount x quantity, less the discount.
//
// Amounts are rounded half up to whole minor units, once after converting
// and once after discounting.
func price(in CreateInput, customer Customer, prices []Price, discounts []Discount, rates []Rate) (Order, error) {
	priceOf := make(map[uuid.UUID]Price, len(prices))
	for _, p := range prices {
		priceOf[p.ItemID] = p
	}
	rateFrom := make(map[Currency]*big.Rat, len(rates))
	for _, r := range rates {
		if r.Quote == in.Currency {
			rateFrom[r.Base] = r.Value
		}
	}

	order := Order{
		CustomerID: customer.ID,
		Currency:   in.Currency,
		Lines:      make([]Line, 0, len(in.Lines)),
	}
	total := new(big.Int)
	for _, l := range in.Lines {
		listed, ok := priceOf[l.ItemID]
		if !ok {
			return Order{}, domain.InvalidInput("item %s cannot be ordered: it has no price", l.ItemID)
		}

		unit := new(big.Rat).SetInt64(listed.UnitAmount)
		if listed.Currency != in.Currency {
			rate, ok := rateFrom[listed.Currency]
			if !ok {
				return Order{}, domain.InvalidInput("no exchange rate from %s to %s", listed.Currency, in.Currency)
			}
			unit.Mul(unit, rate)
		}
		unitAmount := roundHalfUp(unit)

		basisPoints := 0
		for _, d := range discounts {
			if d.appliesTo(customer, l.ItemID) && d.BasisPoints > basisPoints {
				basisPoints = d.BasisPoints
			}
		}
		basisPoints = min(basisPoints, fullBasisPoints)

		gross := new(big.Int).Mul(unitAmount, big.NewInt(int64(l.Quantity)))
		amount := roundHalfUp(new(big.Rat).SetFrac(
			new(big.Int).Mul(gross, big.NewInt(int64(fullBasisPoints-basisPoints))),
			big.NewInt(fullBasisPoints),
		))
		total.Add(total, amount)

		// gross >= amount, so checking the unit amount and the total covers
		// every number stored.
		if !unitAmount.IsInt64() || !total.IsInt64() {
			return Order{}, domain.InvalidInput("the order amount is too large")
		}
		order.Lines = append(order.Lines, Line{
			ItemID:              l.ItemID,
			Quantity:            l.Quantity,
			UnitAmount:          unitAmount.Int64(),
			DiscountBasisPoints: basisPoints,
			Amount:              amount.Int64(),
		})
	}
	order.TotalAmount = total.Int64()
	return order, nil
}

// roundHalfUp rounds a non-negative amount to the nearest whole number.
func roundHalfUp(r *big.Rat) *big.Int {
	// floor((2*num + denom) / (2*denom))
	num := new(big.Int).Lsh(r.Num(), 1)
	num.Add(num, r.Denom())
	return num.Quo(num, new(big.Int).Lsh(r.Denom(), 1))
}
