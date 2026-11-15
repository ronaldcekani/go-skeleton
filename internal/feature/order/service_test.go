package order

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

// fakeSources answers the read interfaces from memory and records what the
// service asked for.
type fakeSources struct {
	customers map[uuid.UUID]Customer
	prices    []Price
	discounts []Discount
	rates     []Rate

	askedGroups []uuid.UUID
	askedBases  []Currency
	rateCalls   int
}

func (f *fakeSources) Customer(_ context.Context, id uuid.UUID) (Customer, error) {
	c, ok := f.customers[id]
	if !ok {
		return Customer{}, domain.NotFound("customer not found")
	}
	return c, nil
}

func (f *fakeSources) Prices(context.Context, []uuid.UUID) ([]Price, error) { return f.prices, nil }

func (f *fakeSources) Discounts(_ context.Context, groupIDs, _ []uuid.UUID) ([]Discount, error) {
	f.askedGroups = groupIDs
	return f.discounts, nil
}

func (f *fakeSources) Rates(_ context.Context, _ Currency, bases []Currency) ([]Rate, error) {
	f.rateCalls++
	f.askedBases = bases
	return f.rates, nil
}

// fakeStore keeps orders in memory and fails Create when it is called
// outside the fake transaction.
type fakeStore struct {
	orders map[uuid.UUID]Order
	inTx   bool
	err    error
}

func (f *fakeStore) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	f.inTx = true
	defer func() { f.inTx = false }()
	return fn(ctx)
}

func (f *fakeStore) Create(_ context.Context, order Order) (Order, error) {
	if !f.inTx {
		return Order{}, errors.New("Create called outside a transaction")
	}
	if f.err != nil {
		return Order{}, f.err
	}
	order.CreatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f.orders[order.ID] = order
	return order, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (Order, error) {
	order, ok := f.orders[id]
	if !ok {
		return Order{}, domain.NotFound("order not found")
	}
	return order, nil
}

type fixture struct {
	svc      *Service
	sources  *fakeSources
	store    *fakeStore
	customer Customer
	widget   uuid.UUID // 10.00 EUR
	gadget   uuid.UUID // 3.33 USD
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	group := uuid.New()
	f := &fixture{
		customer: Customer{ID: uuid.New(), GroupIDs: []uuid.UUID{group}},
		widget:   uuid.New(),
		gadget:   uuid.New(),
		store:    &fakeStore{orders: map[uuid.UUID]Order{}},
	}
	f.sources = &fakeSources{
		customers: map[uuid.UUID]Customer{f.customer.ID: f.customer},
		prices: []Price{
			{ItemID: f.widget, Currency: "EUR", UnitAmount: 1000},
			{ItemID: f.gadget, Currency: "USD", UnitAmount: 333},
		},
		discounts: []Discount{{ID: uuid.New(), GroupID: &group, BasisPoints: 1000}},
		rates:     []Rate{{Base: "USD", Quote: "EUR", Value: big.NewRat(9, 10)}},
	}
	svc, err := NewService(f.sources, f.sources, f.sources, f.store, f.store)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	f.svc = svc
	return f
}

func TestCreate(t *testing.T) {
	f := newFixture(t)

	order, err := f.svc.Create(t.Context(), CreateInput{
		CustomerID: f.customer.ID,
		Currency:   "EUR",
		Lines:      []LineInput{{ItemID: f.widget, Quantity: 2}, {ItemID: f.gadget, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 2 x 10.00 less 10% = 18.00; 3.33 USD = 3.00 EUR less 10% = 2.70.
	if order.TotalAmount != 2070 || len(order.Lines) != 2 {
		t.Errorf("order = %+v, want two lines and a total of 2070", order)
	}
	if order.ID == uuid.Nil || order.CreatedAt.IsZero() {
		t.Errorf("order = %+v, want an ID and a creation time", order)
	}
	if stored, ok := f.store.orders[order.ID]; !ok || stored.TotalAmount != order.TotalAmount {
		t.Errorf("stored = %+v, want the returned order", stored)
	}
	if len(f.sources.askedGroups) != 1 || f.sources.askedGroups[0] != f.customer.GroupIDs[0] {
		t.Errorf("discounts were asked for groups %v, want the customer's", f.sources.askedGroups)
	}
	if len(f.sources.askedBases) != 1 || f.sources.askedBases[0] != "USD" {
		t.Errorf("rates were asked for %v, want only USD", f.sources.askedBases)
	}

	got, err := f.svc.Get(t.Context(), order.ID)
	if err != nil || got.ID != order.ID {
		t.Errorf("Get = %+v, %v; want the created order", got, err)
	}
}

func TestCreateSkipsRatesWhenNothingIsConverted(t *testing.T) {
	f := newFixture(t)
	f.sources.prices = f.sources.prices[:1] // only the widget, priced in EUR

	_, err := f.svc.Create(t.Context(), CreateInput{
		CustomerID: f.customer.ID,
		Currency:   "EUR",
		Lines:      []LineInput{{ItemID: f.widget, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if f.sources.rateCalls != 0 {
		t.Errorf("rates were loaded %d times, want 0 for an order in the price's currency", f.sources.rateCalls)
	}
}

func TestCreateErrors(t *testing.T) {
	f := newFixture(t)
	line := []LineInput{{ItemID: f.widget, Quantity: 1}}

	for name, tc := range map[string]struct {
		in   CreateInput
		kind domain.ErrKind
	}{
		"invalid input":    {CreateInput{CustomerID: f.customer.ID, Currency: "EUR"}, domain.ErrKindInvalidInput},
		"unknown customer": {CreateInput{CustomerID: uuid.New(), Currency: "EUR", Lines: line}, domain.ErrKindInvalidInput},
		"unpriced item": {CreateInput{CustomerID: f.customer.ID, Currency: "EUR",
			Lines: []LineInput{{ItemID: uuid.New(), Quantity: 1}}}, domain.ErrKindInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.Create(t.Context(), tc.in); !domain.IsKind(err, tc.kind) {
				t.Errorf("err = %v, want %v", err, tc.kind)
			}
		})
	}
	if len(f.store.orders) != 0 {
		t.Error("a rejected order was stored")
	}

	f.store.err = errors.New("connection lost")
	_, err := f.svc.Create(t.Context(), CreateInput{CustomerID: f.customer.ID, Currency: "EUR", Lines: line})
	if !domain.IsKind(err, domain.ErrKindInternal) {
		t.Errorf("store failure: err = %v, want internal", err)
	}

	if _, err := f.svc.Get(t.Context(), uuid.New()); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("Get of unknown order: err = %v, want not found", err)
	}
}
