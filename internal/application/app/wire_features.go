package app

import (
	"log/slog"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/feature/item"
)

// features holds the service of every feature. Features do not import each
// other: when one needs another, pass the service (or a small adapter around
// it) to the constructor here, as the interface the consumer declares.
type features struct {
	items *item.Service
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
	return &features{items: items}, nil
}
