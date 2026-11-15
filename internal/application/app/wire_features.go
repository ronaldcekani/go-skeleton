package app

import (
	"log/slog"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/feature/item"
	"github.com/example/go-skeleton/internal/feature/order"
)

// features holds the service of every feature. Features do not import each
// other: when one needs another, pass the service (or a small adapter around
// it) to the constructor here, as the interface the consumer declares.
type features struct {
	items  *item.Service
	orders *order.Service
}

// wireFeatures builds each feature's service on top of its storage, cache
// and event publisher.
func wireFeatures(cfg config.Config, p *platform, log *slog.Logger) (*features, error) {
	var publisher item.EventPublisher = item.NoopPublisher{}
	if p.producer != nil {
		publisher = item.NewKafkaPublisher(p.producer, cfg.Kafka.ItemsTopic)
	}

	items, err := item.NewService(
		item.NewPostgresStore(p.db),
		item.NewRedisCache(p.redis, cfg.Redis.CacheTTL),
		publisher,
		log,
	)
	if err != nil {
		return nil, err
	}

	// The order service asks for customers, the catalog, exchange rates and
	// its own storage separately. One store answers all four today; a source
	// that moves elsewhere only changes the argument passed here.
	orderStore := order.NewPostgresStore(p.db)
	orders, err := order.NewService(orderStore, orderStore, orderStore, orderStore, p.tx)
	if err != nil {
		return nil, err
	}

	return &features{items: items, orders: orders}, nil
}
