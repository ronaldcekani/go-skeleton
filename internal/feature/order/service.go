package order

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/example/go-skeleton/internal/domain"
)

const instrumentationName = "github.com/example/go-skeleton/internal/feature/order"

// The service asks for what it needs in its own terms, one small interface
// per source. Here a single Postgres store implements all of them; when
// customers or prices move to a feature (or a service) of their own, only
// the implementation handed to NewService changes.

// Customers finds who is ordering. It returns a not-found error for an
// unknown ID.
type Customers interface {
	Customer(ctx context.Context, id uuid.UUID) (Customer, error)
}

// Catalog knows what items cost.
type Catalog interface {
	// Prices returns the price of each of the items that has one.
	Prices(ctx context.Context, itemIDs []uuid.UUID) ([]Price, error)
	// Discounts returns the discounts that could apply to a customer in
	// these groups ordering these items, general ones included.
	Discounts(ctx context.Context, groupIDs, itemIDs []uuid.UUID) ([]Discount, error)
}

// Rates returns the exchange rates from each of the base currencies to
// quote, for those that have one.
type Rates interface {
	Rates(ctx context.Context, quote Currency, bases []Currency) ([]Rate, error)
}

// Store persists orders. Create writes the order and its lines with separate
// statements, so it has to be called inside a transaction.
type Store interface {
	Create(ctx context.Context, order Order) (Order, error)
	Get(ctx context.Context, id uuid.UUID) (Order, error)
}

// Transactor runs fn in a database transaction: the store calls made with
// the context fn receives commit together or not at all.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	customers Customers
	catalog   Catalog
	rates     Rates
	store     Store
	tx        Transactor
	tracer    trace.Tracer

	created metric.Int64Counter
}

func NewService(customers Customers, catalog Catalog, rates Rates, store Store, tx Transactor) (*Service, error) {
	created, err := otel.Meter(instrumentationName).Int64Counter("orders.created",
		metric.WithDescription("Number of orders created"))
	if err != nil {
		return nil, fmt.Errorf("create orders.created counter: %w", err)
	}
	return &Service{
		customers: customers,
		catalog:   catalog,
		rates:     rates,
		store:     store,
		tx:        tx,
		tracer:    otel.Tracer(instrumentationName),
		created:   created,
	}, nil
}

// Create prices the order and stores it. It has three parts: load what
// pricing needs, apply the rules (price, which does no I/O), save the
// result.
func (s *Service) Create(ctx context.Context, in CreateInput) (_ Order, err error) {
	ctx, span := s.tracer.Start(ctx, "order.Service.Create")
	defer func() { endSpan(span, err) }()

	if err := in.Validate(); err != nil {
		return Order{}, err
	}

	// Load.
	customer, err := s.customers.Customer(ctx, in.CustomerID)
	if domain.IsKind(err, domain.ErrKindNotFound) {
		// Not a 404: the order is what the caller addressed, and the
		// customer is a part of the request that is wrong.
		return Order{}, domain.InvalidInput("customer %s does not exist", in.CustomerID)
	}
	if err != nil {
		return Order{}, err
	}
	itemIDs := make([]uuid.UUID, 0, len(in.Lines))
	for _, l := range in.Lines {
		itemIDs = append(itemIDs, l.ItemID)
	}
	prices, err := s.catalog.Prices(ctx, itemIDs)
	if err != nil {
		return Order{}, err
	}
	discounts, err := s.catalog.Discounts(ctx, customer.GroupIDs, itemIDs)
	if err != nil {
		return Order{}, err
	}
	var rates []Rate
	if bases := foreignCurrencies(prices, in.Currency); len(bases) > 0 {
		if rates, err = s.rates.Rates(ctx, in.Currency, bases); err != nil {
			return Order{}, err
		}
	}

	// Decide.
	order, err := price(in, customer, prices, discounts, rates)
	if err != nil {
		return Order{}, err
	}
	if order.ID, err = uuid.NewV7(); err != nil {
		return Order{}, fmt.Errorf("generate order id: %w", err)
	}

	// Save. Only the writes need the transaction: the order keeps the
	// prices it was calculated with.
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		order, err = s.store.Create(ctx, order)
		return err
	})
	if err != nil {
		return Order{}, err
	}
	span.SetAttributes(attribute.String("order.id", order.ID.String()))
	s.created.Add(ctx, 1, metric.WithAttributes(attribute.String("currency", string(order.Currency))))
	return order, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (_ Order, err error) {
	ctx, span := s.tracer.Start(ctx, "order.Service.Get",
		trace.WithAttributes(attribute.String("order.id", id.String())))
	defer func() { endSpan(span, err) }()

	return s.store.Get(ctx, id)
}

// foreignCurrencies returns the currencies of the prices that have to be
// converted to the order currency.
func foreignCurrencies(prices []Price, to Currency) []Currency {
	var out []Currency
	seen := map[Currency]bool{to: true}
	for _, p := range prices {
		if !seen[p.Currency] {
			seen[p.Currency] = true
			out = append(out, p.Currency)
		}
	}
	return out
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
