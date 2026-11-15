// Package messaging maps domain events to and from Kafka records.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/example/go-skeleton/internal/domain"
)

// RecordPublisher is the part of the Kafka producer this package needs.
// Publish does not wait for the broker.
type RecordPublisher interface {
	Publish(ctx context.Context, topic string, key, value []byte)
}

const eventTypeItemCreated = "item.created"

// itemEvent and itemPayload are the JSON format of item events. They are a
// contract with every consumer of the topic and deliberately separate from
// domain.Item: change them only in ways consumers can cope with.
type itemEvent struct {
	Type       string      `json:"type"`
	OccurredAt time.Time   `json:"occurred_at"`
	Item       itemPayload `json:"item"`
}

type itemPayload struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ItemPublisher publishes item events, keyed by item ID so events for one
// item stay ordered within a partition.
type ItemPublisher struct {
	producer RecordPublisher
	topic    string
	now      func() time.Time
}

func NewItemPublisher(producer RecordPublisher, topic string) *ItemPublisher {
	return &ItemPublisher{producer: producer, topic: topic, now: time.Now}
}

// PublishItemCreated queues the event for delivery. It only fails if the
// event cannot be encoded; delivery failures are logged by the producer.
func (p *ItemPublisher) PublishItemCreated(ctx context.Context, item domain.Item) error {
	payload, err := json.Marshal(itemEvent{
		Type:       eventTypeItemCreated,
		OccurredAt: p.now().UTC(),
		Item: itemPayload{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		},
	})
	if err != nil {
		return fmt.Errorf("encode item created event: %w", err)
	}
	p.producer.Publish(ctx, p.topic, []byte(item.ID.String()), payload)
	return nil
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
	var event itemEvent
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
