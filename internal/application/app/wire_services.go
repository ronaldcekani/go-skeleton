package app

import (
	"log/slog"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/messaging"
	"github.com/example/go-skeleton/internal/repository"
	"github.com/example/go-skeleton/internal/service"
)

type services struct {
	items *service.ItemService
}

// wireServices builds the data access layer and the services on top of it.
func wireServices(cfg config.Config, p *platform, log *slog.Logger) (*services, error) {
	var publisher service.EventPublisher = messaging.NoopPublisher{}
	if p.producer != nil {
		publisher = messaging.NewItemPublisher(p.producer, cfg.Kafka.ItemsTopic)
	}

	items, err := service.NewItemService(
		repository.NewItemRepository(p.db),
		repository.NewItemCache(p.redis, cfg.Redis.CacheTTL),
		publisher,
		log,
	)
	if err != nil {
		return nil, err
	}
	return &services{items: items}, nil
}
