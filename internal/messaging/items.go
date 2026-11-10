// Package messaging maps domain events to and from Kafka records.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/example/go-skeleton/internal/domain"
)

// RecordPublisher is the part of the Kafka producer this package needs.
type RecordPublisher interface {
	Publish(ctx context.Context, topic string, key, value []byte) error
}

// ItemPublisher publishes item events, keyed by item ID so events for one
// item stay ordered within a partition.
type ItemPublisher struct {
	producer RecordPublisher
	topic    string
}

func NewItemPublisher(producer RecordPublisher, topic string) *ItemPublisher {
	return &ItemPublisher{producer: producer, topic: topic}
}

func (p *ItemPublisher) PublishItemCreated(ctx context.Context, item domain.Item) error {
	payload, err := json.Marshal(domain.ItemCreatedEvent{
		Type:       domain.EventTypeItemCreated,
		OccurredAt: time.Now().UTC(),
		Item:       item,
	})
	if err != nil {
		return fmt.Errorf("encode item created event: %w", err)
	}
	return p.producer.Publish(ctx, p.topic, []byte(item.ID.String()), payload)
}

// NoopPublisher discards events. It is used when Kafka is disabled.
type NoopPublisher struct{}

func (NoopPublisher) PublishItemCreated(context.Context, domain.Item) error { return nil }

// ItemEventHandler consumes item events. It only logs them; replace the body
// with real work (projections, notifications, calls to other services).
type ItemEventHandler struct {
	log *slog.Logger
}

func NewItemEventHandler(log *slog.Logger) *ItemEventHandler {
	return &ItemEventHandler{log: log}
}

func (h *ItemEventHandler) Handle(ctx context.Context, rec *kgo.Record) error {
	var event domain.ItemCreatedEvent
	if err := json.Unmarshal(rec.Value, &event); err != nil {
		return fmt.Errorf("decode item event: %w", err)
	}
	h.log.InfoContext(ctx, "item event received",
		slog.String("type", event.Type),
		slog.String("item_id", event.Item.ID.String()),
		slog.String("topic", rec.Topic),
		slog.Int("partition", int(rec.Partition)),
		slog.Int64("offset", rec.Offset))
	return nil
}
