package order

import (
	"math"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

// The pricing rules need no database and no fakes: they are a function.
func TestPrice(t *testing.T) {
	var (
		widget, gadget = uuid.New(), uuid.New()
		wholesale      = uuid.New()
		otherGroup     = uuid.New()
		retailer       = Customer{ID: uuid.New()}
		wholesaler     = Customer{ID: uuid.New(), GroupIDs: []uuid.UUID{wholesale}}
	)
	prices := []Price{
		{ItemID: widget, Currency: "EUR", UnitAmount: 1000}, // 10.00 EUR
		{ItemID: gadget, Currency: "USD", UnitAmount: 333},  // 3.33 USD
	}
	usdToEUR := []Rate{{Base: "USD", Quote: "EUR", Value: big.NewRat(9, 10)}}
	discount := func(group, item *uuid.UUID, basisPoints int) Discount {
		return Discount{ID: uuid.New(), GroupID: group, ItemID: item, BasisPoints: basisPoints}
	}
	line := func(item uuid.UUID, quantity int) LineInput { return LineInput{ItemID: item, Quantity: quantity} }

	for name, tc := range map[string]struct {
		customer  Customer
		currency  Currency
		lines     []LineInput
		discounts []Discount
		rates     []Rate
		want      []Line
		total     int64
	}{
		"list price in the price's own currency": {
			customer: retailer, currency: "EUR",
			lines: []LineInput{line(widget, 3)},
			want:  []Line{{ItemID: widget, Quantity: 3, UnitAmount: 1000, Amount: 3000}},
			total: 3000,
		},
		"price converted to the order currency": {
			customer: retailer, currency: "EUR",
			lines: []LineInput{line(gadget, 3)},
			rates: usdToEUR,
			// 3.33 USD x 0.9 = 2.997 EUR, rounded to 3.00 before multiplying.
			want:  []Line{{ItemID: gadget, Quantity: 3, UnitAmount: 300, Amount: 900}},
			total: 900,
		},
		"group discount applies to members only": {
			customer: wholesaler, currency: "EUR",
			lines:     []LineInput{line(widget, 2)},
			discounts: []Discount{discount(&wholesale, nil, 1000), discount(&otherGroup, nil, 5000)},
			want:      []Line{{ItemID: widget, Quantity: 2, UnitAmount: 1000, DiscountBasisPoints: 1000, Amount: 1800}},
			total:     1800,
		},
		"non-members pay the list price": {
			customer: retailer, currency: "EUR",
			lines:     []LineInput{line(widget, 2)},
			discounts: []Discount{discount(&wholesale, nil, 1000)},
			want:      []Line{{ItemID: widget, Quantity: 2, UnitAmount: 1000, Amount: 2000}},
			total:     2000,
		},
		"the largest discount wins, they do not add up": {
			customer: wholesaler, currency: "EUR",
			lines: []LineInput{line(widget, 1)},
			discounts: []Discount{
				discount(nil, nil, 500),            // everyone, every item
				discount(&wholesale, nil, 1000),    // the group
				discount(&wholesale, &widget, 250), // the group, this item
			},
			want:  []Line{{ItemID: widget, Quantity: 1, UnitAmount: 1000, DiscountBasisPoints: 1000, Amount: 900}},
			total: 900,
		},
		"item discounts apply to that item only": {
			customer: retailer, currency: "EUR",
			lines:     []LineInput{line(widget, 1), line(gadget, 1)},
			discounts: []Discount{discount(nil, &gadget, 5000)},
			rates:     usdToEUR,
			want: []Line{
				{ItemID: widget, Quantity: 1, UnitAmount: 1000, Amount: 1000},
				{ItemID: gadget, Quantity: 1, UnitAmount: 300, DiscountBasisPoints: 5000, Amount: 150},
			},
			total: 1150,
		},
		"discounted amounts round half up": {
			customer: retailer, currency: "USD",
			lines:     []LineInput{line(gadget, 1)},
			discounts: []Discount{discount(nil, nil, 5000)}, // half of 3.33 is 1.665
			want:      []Line{{ItemID: gadget, Quantity: 1, UnitAmount: 333, DiscountBasisPoints: 5000, Amount: 167}},
			total:     167,
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := CreateInput{CustomerID: tc.customer.ID, Currency: tc.currency, Lines: tc.lines}
			got, err := price(in, tc.customer, prices, tc.discounts, tc.rates)
			if err != nil {
				t.Fatalf("price: %v", err)
			}
			if got.CustomerID != tc.customer.ID || got.Currency != tc.currency || got.TotalAmount != tc.total {
				t.Errorf("order = %+v, want customer %s, currency %s, total %d", got, tc.customer.ID, tc.currency, tc.total)
			}
			if len(got.Lines) != len(tc.want) {
				t.Fatalf("got %d lines, want %d", len(got.Lines), len(tc.want))
			}
			for i, want := range tc.want {
				if got.Lines[i] != want {
					t.Errorf("line %d = %+v, want %+v", i, got.Lines[i], want)
				}
			}
		})
	}

	for name, tc := range map[string]struct {
		currency Currency
		lines    []LineInput
		prices   []Price
	}{
		"item without a price":          {"EUR", []LineInput{line(uuid.New(), 1)}, prices},
		"no rate to the order currency": {"GBP", []LineInput{line(widget, 1)}, prices},
		"amount too large": {"EUR", []LineInput{line(widget, MaxQuantity)},
			[]Price{{ItemID: widget, Currency: "EUR", UnitAmount: math.MaxInt64}}},
	} {
		t.Run(name, func(t *testing.T) {
			in := CreateInput{CustomerID: retailer.ID, Currency: tc.currency, Lines: tc.lines}
			if _, err := price(in, retailer, tc.prices, nil, usdToEUR); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("err = %v, want invalid input", err)
			}
		})
	}
}

func TestCreateInputValidate(t *testing.T) {
	customer, item := uuid.New(), uuid.New()
	valid := CreateInput{CustomerID: customer, Currency: "EUR", Lines: []LineInput{{ItemID: item, Quantity: 1}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}

	tooMany := make([]LineInput, MaxLines+1)
	for i := range tooMany {
		tooMany[i] = LineInput{ItemID: uuid.New(), Quantity: 1}
	}
	for name, change := range map[string]func(*CreateInput){
		"no customer":         func(in *CreateInput) { in.CustomerID = uuid.Nil },
		"lower-case currency": func(in *CreateInput) { in.Currency = "eur" },
		"long currency":       func(in *CreateInput) { in.Currency = "EURO" },
		"no lines":            func(in *CreateInput) { in.Lines = nil },
		"too many lines":      func(in *CreateInput) { in.Lines = tooMany },
		"no item":             func(in *CreateInput) { in.Lines = []LineInput{{Quantity: 1}} },
		"zero quantity":       func(in *CreateInput) { in.Lines = []LineInput{{ItemID: item}} },
		"huge quantity":       func(in *CreateInput) { in.Lines = []LineInput{{ItemID: item, Quantity: MaxQuantity + 1}} },
		"item twice": func(in *CreateInput) {
			in.Lines = []LineInput{{ItemID: item, Quantity: 1}, {ItemID: item, Quantity: 2}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := valid
			change(&in)
			if err := in.Validate(); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("Validate = %v, want invalid input", err)
			}
		})
	}
}
