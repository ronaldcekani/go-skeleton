// Package service is the business layer. It is transport-agnostic: the HTTP
// and gRPC servers both call it, and it reaches storage, cache and messaging
// only through the interfaces declared here.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/example/go-skeleton/internal/domain"
)

const instrumentationName = "github.com/example/go-skeleton/internal/service"

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	maxNameLen      = 200
	maxDescLen      = 2000
)

type ItemStore interface {
	Create(ctx context.Context, item domain.Item) (domain.Item, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Item, error)
	List(ctx context.Context, limit, offset int) ([]domain.Item, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type ItemCache interface {
	Get(ctx context.Context, id uuid.UUID) (item domain.Item, found bool, err error)
	Set(ctx context.Context, item domain.Item) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type EventPublisher interface {
	PublishItemCreated(ctx context.Context, item domain.Item) error
}

type ItemService struct {
	store     ItemStore
	cache     ItemCache
	publisher EventPublisher
	log       *slog.Logger
	tracer    trace.Tracer

	created   metric.Int64Counter
	cacheHits metric.Int64Counter
}

func NewItemService(store ItemStore, cache ItemCache, publisher EventPublisher, log *slog.Logger) (*ItemService, error) {
	meter := otel.Meter(instrumentationName)
	created, err := meter.Int64Counter("items.created",
		metric.WithDescription("Number of items created"))
	if err != nil {
		return nil, fmt.Errorf("create items.created counter: %w", err)
	}
	cacheHits, err := meter.Int64Counter("items.cache.lookups",
		metric.WithDescription("Item cache lookups by result"))
	if err != nil {
		return nil, fmt.Errorf("create items.cache.lookups counter: %w", err)
	}
	return &ItemService{
		store:     store,
		cache:     cache,
		publisher: publisher,
		log:       log,
		tracer:    otel.Tracer(instrumentationName),
		created:   created,
		cacheHits: cacheHits,
	}, nil
}

func (s *ItemService) Create(ctx context.Context, name, description string) (_ domain.Item, err error) {
	ctx, span := s.tracer.Start(ctx, "ItemService.Create")
	defer func() { endSpan(span, err) }()

	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return domain.Item{}, &domain.ValidationError{Message: "name is required"}
	case len(name) > maxNameLen:
		return domain.Item{}, &domain.ValidationError{Message: fmt.Sprintf("name must be at most %d characters", maxNameLen)}
	case len(description) > maxDescLen:
		return domain.Item{}, &domain.ValidationError{Message: fmt.Sprintf("description must be at most %d characters", maxDescLen)}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return domain.Item{}, fmt.Errorf("generate item id: %w", err)
	}
	item, err := s.store.Create(ctx, domain.Item{ID: id, Name: name, Description: description})
	if err != nil {
		return domain.Item{}, err
	}
	span.SetAttributes(attribute.String("item.id", item.ID.String()))
	s.created.Add(ctx, 1)

	// The item is already committed, so a failed publish must not fail the
	// request. If events must never be lost, replace this with a
	// transactional outbox.
	if err := s.publisher.PublishItemCreated(ctx, item); err != nil {
		s.log.ErrorContext(ctx, "publish item created event failed",
			slog.String("item_id", item.ID.String()), slog.Any("error", err))
	}
	return item, nil
}

// Get reads through the cache: on a miss the item is loaded from the store
// and cached. Cache failures degrade to store reads instead of failing.
func (s *ItemService) Get(ctx context.Context, id uuid.UUID) (_ domain.Item, err error) {
	ctx, span := s.tracer.Start(ctx, "ItemService.Get",
		trace.WithAttributes(attribute.String("item.id", id.String())))
	defer func() { endSpan(span, err) }()

	item, found, cacheErr := s.cache.Get(ctx, id)
	if cacheErr != nil {
		s.log.WarnContext(ctx, "item cache read failed", slog.Any("error", cacheErr))
	}
	span.SetAttributes(attribute.Bool("cache.hit", found))
	s.cacheHits.Add(ctx, 1, metric.WithAttributes(attribute.Bool("hit", found)))
	if found {
		return item, nil
	}

	item, err = s.store.GetByID(ctx, id)
	if err != nil {
		return domain.Item{}, err
	}
	if err := s.cache.Set(ctx, item); err != nil {
		s.log.WarnContext(ctx, "item cache write failed", slog.Any("error", err))
	}
	return item, nil
}

// List returns a page of items. A limit of zero means DefaultPageSize.
func (s *ItemService) List(ctx context.Context, limit, offset int) (_ []domain.Item, _ int64, err error) {
	ctx, span := s.tracer.Start(ctx, "ItemService.List")
	defer func() { endSpan(span, err) }()

	if limit == 0 {
		limit = DefaultPageSize
	}
	switch {
	case limit < 0 || limit > MaxPageSize:
		return nil, 0, &domain.ValidationError{Message: fmt.Sprintf("limit must be between 1 and %d", MaxPageSize)}
	case offset < 0:
		return nil, 0, &domain.ValidationError{Message: "offset must not be negative"}
	}
	return s.store.List(ctx, limit, offset)
}

func (s *ItemService) Delete(ctx context.Context, id uuid.UUID) (err error) {
	ctx, span := s.tracer.Start(ctx, "ItemService.Delete",
		trace.WithAttributes(attribute.String("item.id", id.String())))
	defer func() { endSpan(span, err) }()

	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.cache.Delete(ctx, id); err != nil {
		s.log.WarnContext(ctx, "item cache invalidation failed", slog.Any("error", err))
	}
	return nil
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
