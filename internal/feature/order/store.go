package order

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/platform/postgres"
)

// PostgresStore reads what an order is priced from and persists orders. It
// implements Customers, Catalog, Rates and Store.
//
// The row types below follow the tables; the methods return the feature's
// types. The difference is the point: a nullable column becomes a pointer, a
// NUMERIC becomes a big.Rat, two queries become one Customer, and none of it
// reaches the pricing rules.
type PostgresStore struct {
	db *sqlx.DB
}

func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

type customerRow struct {
	ID uuid.UUID `db:"id"`
}

func toCustomer(row customerRow, groupIDs []uuid.UUID) Customer {
	return Customer{ID: row.ID, GroupIDs: groupIDs}
}

func (s *PostgresStore) Customer(ctx context.Context, id uuid.UUID) (Customer, error) {
	conn := postgres.Conn(ctx, s.db)

	var row customerRow
	if err := conn.GetContext(ctx, &row, `SELECT id FROM customers WHERE id = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Customer{}, domain.NotFound("customer not found")
		}
		return Customer{}, fmt.Errorf("select customer: %w", err)
	}
	groupIDs := []uuid.UUID{}
	const q = `SELECT group_id FROM customer_group_members WHERE customer_id = $1 ORDER BY group_id`
	if err := conn.SelectContext(ctx, &groupIDs, q, id); err != nil {
		return Customer{}, fmt.Errorf("select customer groups: %w", err)
	}
	return toCustomer(row, groupIDs), nil
}

type priceRow struct {
	ItemID     uuid.UUID `db:"item_id"`
	Currency   string    `db:"currency"`
	UnitAmount int64     `db:"unit_amount"`
}

func (r priceRow) toPrice() Price {
	return Price{ItemID: r.ItemID, Currency: Currency(r.Currency), UnitAmount: r.UnitAmount}
}

func (s *PostgresStore) Prices(ctx context.Context, itemIDs []uuid.UUID) ([]Price, error) {
	const q = `SELECT item_id, currency, unit_amount FROM item_prices WHERE item_id = ANY($1::uuid[])`

	var rows []priceRow
	if err := postgres.Conn(ctx, s.db).SelectContext(ctx, &rows, q, uuidStrings(itemIDs)); err != nil {
		return nil, fmt.Errorf("select item prices: %w", err)
	}
	prices := make([]Price, 0, len(rows))
	for _, row := range rows {
		prices = append(prices, row.toPrice())
	}
	return prices, nil
}

type discountRow struct {
	ID          uuid.UUID     `db:"id"`
	GroupID     uuid.NullUUID `db:"group_id"`
	ItemID      uuid.NullUUID `db:"item_id"`
	BasisPoints int           `db:"basis_points"`
}

func (r discountRow) toDiscount() Discount {
	d := Discount{ID: r.ID, BasisPoints: r.BasisPoints}
	if r.GroupID.Valid {
		d.GroupID = &r.GroupID.UUID
	}
	if r.ItemID.Valid {
		d.ItemID = &r.ItemID.UUID
	}
	return d
}

func (s *PostgresStore) Discounts(ctx context.Context, groupIDs, itemIDs []uuid.UUID) ([]Discount, error) {
	const q = `
		SELECT id, group_id, item_id, basis_points
		FROM discounts
		WHERE (group_id IS NULL OR group_id = ANY($1::uuid[]))
		  AND (item_id IS NULL OR item_id = ANY($2::uuid[]))`

	var rows []discountRow
	err := postgres.Conn(ctx, s.db).SelectContext(ctx, &rows, q, uuidStrings(groupIDs), uuidStrings(itemIDs))
	if err != nil {
		return nil, fmt.Errorf("select discounts: %w", err)
	}
	discounts := make([]Discount, 0, len(rows))
	for _, row := range rows {
		discounts = append(discounts, row.toDiscount())
	}
	return discounts, nil
}

type rateRow struct {
	Base  string `db:"base_currency"`
	Quote string `db:"quote_currency"`
	Rate  string `db:"rate"` // NUMERIC, read as text to keep it exact
}

func (r rateRow) toRate() (Rate, error) {
	value, ok := new(big.Rat).SetString(r.Rate)
	if !ok {
		return Rate{}, fmt.Errorf("exchange rate %s/%s: %q is not a number", r.Base, r.Quote, r.Rate)
	}
	return Rate{Base: Currency(r.Base), Quote: Currency(r.Quote), Value: value}, nil
}

func (s *PostgresStore) Rates(ctx context.Context, quote Currency, bases []Currency) ([]Rate, error) {
	const q = `
		SELECT base_currency, quote_currency, rate::text AS rate
		FROM exchange_rates
		WHERE quote_currency = $1 AND base_currency = ANY($2::text[])`

	codes := make([]string, 0, len(bases))
	for _, b := range bases {
		codes = append(codes, string(b))
	}
	var rows []rateRow
	if err := postgres.Conn(ctx, s.db).SelectContext(ctx, &rows, q, string(quote), codes); err != nil {
		return nil, fmt.Errorf("select exchange rates: %w", err)
	}
	rates := make([]Rate, 0, len(rows))
	for _, row := range rows {
		rate, err := row.toRate()
		if err != nil {
			return nil, err
		}
		rates = append(rates, rate)
	}
	return rates, nil
}

type orderRow struct {
	ID          uuid.UUID `db:"id"`
	CustomerID  uuid.UUID `db:"customer_id"`
	Currency    string    `db:"currency"`
	TotalAmount int64     `db:"total_amount"`
	CreatedAt   time.Time `db:"created_at"`
}

type lineRow struct {
	ItemID              uuid.UUID `db:"item_id"`
	Quantity            int       `db:"quantity"`
	UnitAmount          int64     `db:"unit_amount"`
	DiscountBasisPoints int       `db:"discount_basis_points"`
	Amount              int64     `db:"amount"`
}

// toOrder builds the order from its row and the rows of its lines.
func toOrder(row orderRow, lines []lineRow) Order {
	order := Order{
		ID:          row.ID,
		CustomerID:  row.CustomerID,
		Currency:    Currency(row.Currency),
		Lines:       make([]Line, 0, len(lines)),
		TotalAmount: row.TotalAmount,
		CreatedAt:   row.CreatedAt,
	}
	for _, l := range lines {
		order.Lines = append(order.Lines, Line{
			ItemID:              l.ItemID,
			Quantity:            l.Quantity,
			UnitAmount:          l.UnitAmount,
			DiscountBasisPoints: l.DiscountBasisPoints,
			Amount:              l.Amount,
		})
	}
	return order
}

const (
	orderColumns = `id, customer_id, currency, total_amount, created_at`
	lineColumns  = `item_id, quantity, unit_amount, discount_basis_points, amount`
)

// Create inserts the order and its lines. The caller provides the
// transaction (Transactor.WithinTx); without one, a failure between the two
// statements would leave an order without lines.
func (s *PostgresStore) Create(ctx context.Context, order Order) (Order, error) {
	conn := postgres.Conn(ctx, s.db)

	const insertOrder = `
		INSERT INTO orders (id, customer_id, currency, total_amount)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + orderColumns
	var row orderRow
	err := conn.GetContext(ctx, &row, insertOrder, order.ID, order.CustomerID, string(order.Currency), order.TotalAmount)
	if err != nil {
		return Order{}, fmt.Errorf("insert order: %w", err)
	}

	// One statement for all lines: each column travels as an array.
	n := len(order.Lines)
	lineNos, itemIDs, quantities := make([]int32, n), make([]string, n), make([]int32, n)
	unitAmounts, basisPoints, amounts := make([]int64, n), make([]int32, n), make([]int64, n)
	for i, l := range order.Lines {
		lineNos[i], itemIDs[i], quantities[i] = int32(i+1), l.ItemID.String(), int32(l.Quantity)
		unitAmounts[i], basisPoints[i], amounts[i] = l.UnitAmount, int32(l.DiscountBasisPoints), l.Amount
	}
	const insertLines = `
		INSERT INTO order_lines (order_id, line_no, ` + lineColumns + `)
		SELECT $1::uuid, *
		FROM unnest($2::int[], $3::uuid[], $4::int[], $5::bigint[], $6::int[], $7::bigint[])
		ORDER BY 1
		RETURNING ` + lineColumns
	var lines []lineRow
	err = conn.SelectContext(ctx, &lines, insertLines, order.ID, lineNos, itemIDs, quantities, unitAmounts, basisPoints, amounts)
	if err != nil {
		return Order{}, fmt.Errorf("insert order lines: %w", err)
	}
	return toOrder(row, lines), nil
}

func (s *PostgresStore) Get(ctx context.Context, id uuid.UUID) (Order, error) {
	conn := postgres.Conn(ctx, s.db)

	var row orderRow
	if err := conn.GetContext(ctx, &row, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Order{}, domain.NotFound("order not found")
		}
		return Order{}, fmt.Errorf("select order: %w", err)
	}
	var lines []lineRow
	const q = `SELECT ` + lineColumns + ` FROM order_lines WHERE order_id = $1 ORDER BY line_no`
	if err := conn.SelectContext(ctx, &lines, q, id); err != nil {
		return Order{}, fmt.Errorf("select order lines: %w", err)
	}
	return toOrder(row, lines), nil
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
