package order_test

import (
	"context"
	"math/big"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/feature/order"
	"github.com/example/go-skeleton/internal/platform/postgres"
	"github.com/example/go-skeleton/migrations"
)

// openTestDB connects to a real Postgres and applies the migrations. It skips
// the test unless TEST_POSTGRES_DSN is set.
func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	db, err := postgres.Open(t.Context(), postgres.Options{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The lock makes the test packages, which run in parallel against the
	// same database, migrate one at a time.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		t.Fatalf("migration lock: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db.DB, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		t.Fatalf("migration provider: %v", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// world is the data one test prices orders from. Everything hangs off IDs
// and currency codes made up for the test, and is deleted afterwards.
type world struct {
	customer, group  uuid.UUID
	widget, gadget   uuid.UUID // 10.00 in the home currency, 3.33 in the foreign one
	unpriced         uuid.UUID
	home, foreign    string
	groupDiscount    uuid.UUID
	everyoneDiscount uuid.UUID
}

func seed(t *testing.T, db *sqlx.DB) world {
	t.Helper()
	ctx := t.Context()
	w := world{
		customer: uuid.New(), group: uuid.New(),
		widget: uuid.New(), gadget: uuid.New(), unpriced: uuid.New(),
		// Codes no other test or real row uses.
		home: "X" + randomLetters(2), foreign: "Y" + randomLetters(2),
		groupDiscount: uuid.New(), everyoneDiscount: uuid.New(),
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO customers (id, name) VALUES ($1, 'test customer')`, w.customer)
	exec(`INSERT INTO customer_groups (id, name) VALUES ($1, $2)`, w.group, "group "+w.group.String())
	exec(`INSERT INTO customer_group_members (customer_id, group_id) VALUES ($1, $2)`, w.customer, w.group)
	exec(`INSERT INTO items (id, name) VALUES ($1, 'widget'), ($2, 'gadget'), ($3, 'unpriced')`, w.widget, w.gadget, w.unpriced)
	exec(`INSERT INTO item_prices (item_id, currency, unit_amount) VALUES ($1, $2, 1000), ($3, $4, 333)`,
		w.widget, w.home, w.gadget, w.foreign)
	// The second discount is scoped to the widget so it cannot leak into
	// other tests' orders.
	exec(`INSERT INTO discounts (id, name, group_id, item_id, basis_points) VALUES ($1, 'group', $2, NULL, 1000), ($3, 'everyone', NULL, $4, 500)`,
		w.groupDiscount, w.group, w.everyoneDiscount, w.widget)
	exec(`INSERT INTO exchange_rates (base_currency, quote_currency, rate) VALUES ($1, $2, 0.9)`, w.foreign, w.home)

	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, q := range []string{
			`DELETE FROM orders WHERE customer_id = $1`,
			`DELETE FROM customers WHERE id = $1`,
		} {
			_, _ = db.ExecContext(ctx, q, w.customer)
		}
		_, _ = db.ExecContext(ctx, `DELETE FROM customer_groups WHERE id = $1`, w.group)
		_, _ = db.ExecContext(ctx, `DELETE FROM items WHERE id IN ($1, $2, $3)`, w.widget, w.gadget, w.unpriced)
		_, _ = db.ExecContext(ctx, `DELETE FROM exchange_rates WHERE base_currency = $1`, w.foreign)
	})
	return w
}

func randomLetters(n int) string {
	id := uuid.New()
	out := make([]byte, n)
	for i := range out {
		out[i] = 'A' + id[i]%26
	}
	return string(out)
}

func TestPostgresStoreReadsWhatPricingNeeds(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	store := order.NewPostgresStore(db)
	w := seed(t, db)

	customer, err := store.Customer(ctx, w.customer)
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if customer.ID != w.customer || len(customer.GroupIDs) != 1 || customer.GroupIDs[0] != w.group {
		t.Errorf("customer = %+v, want the seeded customer in its group", customer)
	}
	if _, err := store.Customer(ctx, uuid.New()); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("unknown customer: err = %v, want not found", err)
	}

	prices, err := store.Prices(ctx, []uuid.UUID{w.widget, w.gadget, w.unpriced})
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	got := map[uuid.UUID]order.Price{}
	for _, p := range prices {
		got[p.ItemID] = p
	}
	if len(got) != 2 ||
		got[w.widget] != (order.Price{ItemID: w.widget, Currency: order.Currency(w.home), UnitAmount: 1000}) ||
		got[w.gadget] != (order.Price{ItemID: w.gadget, Currency: order.Currency(w.foreign), UnitAmount: 333}) {
		t.Errorf("prices = %+v, want the widget and the gadget", prices)
	}

	// The gadget alone: the widget-only discount does not come back, the
	// group one does.
	discounts, err := store.Discounts(ctx, customer.GroupIDs, []uuid.UUID{w.gadget})
	if err != nil {
		t.Fatalf("Discounts: %v", err)
	}
	found := map[uuid.UUID]order.Discount{}
	for _, d := range discounts {
		found[d.ID] = d
	}
	group, ok := found[w.groupDiscount]
	if !ok || group.GroupID == nil || *group.GroupID != w.group || group.ItemID != nil || group.BasisPoints != 1000 {
		t.Errorf("group discount = %+v (found %v), want 1000 basis points for the group on every item", group, ok)
	}
	if _, ok := found[w.everyoneDiscount]; ok {
		t.Error("a discount for another item was returned")
	}
	// Without the group, only the widget's discount for everyone applies.
	discounts, err = store.Discounts(ctx, nil, []uuid.UUID{w.widget})
	if err != nil {
		t.Fatalf("Discounts: %v", err)
	}
	found = map[uuid.UUID]order.Discount{}
	for _, d := range discounts {
		found[d.ID] = d
	}
	everyone, ok := found[w.everyoneDiscount]
	if !ok || everyone.GroupID != nil || everyone.ItemID == nil || *everyone.ItemID != w.widget {
		t.Errorf("everyone discount = %+v (found %v), want it for the widget and no group", everyone, ok)
	}
	if _, ok := found[w.groupDiscount]; ok {
		t.Error("a discount for a group the customer is not in was returned")
	}

	rates, err := store.Rates(ctx, order.Currency(w.home), []order.Currency{order.Currency(w.foreign), "ZZZ"})
	if err != nil {
		t.Fatalf("Rates: %v", err)
	}
	if len(rates) != 1 || rates[0].Base != order.Currency(w.foreign) || rates[0].Quote != order.Currency(w.home) ||
		rates[0].Value.Cmp(big.NewRat(9, 10)) != 0 {
		t.Errorf("rates = %+v, want one rate of exactly 0.9", rates)
	}
}

// The whole use case against a real database: the service on the store.
func TestCreateOrderEndToEnd(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	store := order.NewPostgresStore(db)
	svc, err := order.NewService(store, store, store, store, postgres.NewTransactor(db))
	if err != nil {
		t.Fatal(err)
	}
	w := seed(t, db)

	created, err := svc.Create(ctx, order.CreateInput{
		CustomerID: w.customer,
		Currency:   order.Currency(w.home),
		Lines:      []order.LineInput{{ItemID: w.widget, Quantity: 2}, {ItemID: w.gadget, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := []order.Line{
		// The group's 10% beats the 5% everyone gets on widgets.
		{ItemID: w.widget, Quantity: 2, UnitAmount: 1000, DiscountBasisPoints: 1000, Amount: 1800},
		// 3.33 x 0.9 = 3.00 after rounding, less 10%.
		{ItemID: w.gadget, Quantity: 1, UnitAmount: 300, DiscountBasisPoints: 1000, Amount: 270},
	}
	if created.TotalAmount != 2070 || len(created.Lines) != 2 || created.Lines[0] != want[0] || created.Lines[1] != want[1] {
		t.Errorf("created = %+v, want lines %+v and a total of 2070", created, want)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created order has no creation time")
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.CustomerID != w.customer || got.Currency != order.Currency(w.home) ||
		got.TotalAmount != 2070 || !got.CreatedAt.Equal(created.CreatedAt) ||
		len(got.Lines) != 2 || got.Lines[0] != want[0] || got.Lines[1] != want[1] {
		t.Errorf("Get = %+v, want what Create returned: %+v", got, created)
	}

	for name, in := range map[string]order.CreateInput{
		"unknown customer": {CustomerID: uuid.New(), Currency: order.Currency(w.home),
			Lines: []order.LineInput{{ItemID: w.widget, Quantity: 1}}},
		"unpriced item": {CustomerID: w.customer, Currency: order.Currency(w.home),
			Lines: []order.LineInput{{ItemID: w.unpriced, Quantity: 1}}},
		"no rate": {CustomerID: w.customer, Currency: order.Currency(w.foreign),
			Lines: []order.LineInput{{ItemID: w.widget, Quantity: 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Create(ctx, in); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("err = %v, want invalid input", err)
			}
		})
	}
}

// An order whose lines cannot be written must not be left behind.
func TestCreateOrderIsAtomic(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	store := order.NewPostgresStore(db)
	w := seed(t, db)

	// The second line names an item that does not exist, so inserting the
	// lines fails after the order row was inserted.
	doomed := order.Order{
		ID: uuid.New(), CustomerID: w.customer, Currency: order.Currency(w.home), TotalAmount: 1000,
		Lines: []order.Line{
			{ItemID: w.widget, Quantity: 1, UnitAmount: 1000, Amount: 1000},
			{ItemID: uuid.New(), Quantity: 1},
		},
	}
	err := postgres.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		_, err := store.Create(ctx, doomed)
		return err
	})
	if err == nil {
		t.Fatal("Create succeeded with a line for a missing item")
	}
	if _, err := store.Get(ctx, doomed.ID); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("after the failed Create, Get err = %v, want not found", err)
	}
}
