package app

import (
	"context"
	"log/slog"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/messaging"
	"github.com/example/go-skeleton/internal/platform/kafka"
)

type consumers struct {
	components []component
}

// wireConsumers builds the Kafka consumers. Each one is a component: stopping
// it lets the current batch finish and commit; its client is closed (leaving
// the consumer group) by the cleanup stack afterwards.
func wireConsumers(ctx context.Context, cfg config.Config, log *slog.Logger, cleanup *cleanupStack) (*consumers, error) {
	if !cfg.Kafka.Enabled {
		return &consumers{}, nil
	}

	handler := messaging.NewItemEventHandler(log)
	consumer, err := kafka.NewConsumer(ctx,
		kafka.Options{Brokers: cfg.Kafka.Brokers, ClientID: cfg.Kafka.ClientID},
		cfg.Kafka.ConsumerGroup, []string{cfg.Kafka.ItemsTopic}, handler.Handle, log)
	if err != nil {
		return nil, err
	}
	cleanup.push("kafka consumer", closeNoErr(consumer.Close))

	// The poll loop runs until this context is cancelled by stop.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &consumers{components: []component{{
		name:  "kafka consumer " + cfg.Kafka.ItemsTopic,
		start: func() error { return consumer.Run(runCtx) },
		stop:  func(context.Context) error { cancel(); return nil },
	}}}, nil
}
