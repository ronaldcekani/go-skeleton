package messaging

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/example/go-skeleton/internal/domain"
)

type fakeProducer struct {
	topic      string
	key, value []byte
}

func (f *fakeProducer) Publish(_ context.Context, topic string, key, value []byte) {
	f.topic, f.key, f.value = topic, key, value
}

// The event JSON is a contract with consumers. If this test fails, the wire
// format changed: make sure that is intended before updating it.
func TestItemCreatedWireFormat(t *testing.T) {
	producer := &fakeProducer{}
	p := NewItemPublisher(producer, "items.events")
	p.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	id := uuid.MustParse("0195a5e0-0000-7000-8000-000000000001")
	created := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	err := p.PublishItemCreated(t.Context(), domain.Item{
		ID: id, Name: "widget", Description: "a thing", CreatedAt: created, UpdatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}

	const want = `{"type":"item.created","occurred_at":"2026-01-02T03:04:05Z","item":{` +
		`"id":"0195a5e0-0000-7000-8000-000000000001","name":"widget","description":"a thing",` +
		`"created_at":"2026-01-02T03:04:00Z","updated_at":"2026-01-02T03:04:00Z"}}`
	if got := string(producer.value); got != want {
		t.Errorf("event =\n%s\nwant\n%s", got, want)
	}
	if producer.topic != "items.events" || string(producer.key) != id.String() {
		t.Errorf("topic = %q, key = %q; want the items topic and the item ID as key", producer.topic, producer.key)
	}

	// What is published can be consumed.
	h := NewItemEventHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Handle(t.Context(), &kgo.Record{Value: producer.value}); err != nil {
		t.Errorf("Handle = %v", err)
	}
	if err := h.Handle(t.Context(), &kgo.Record{Value: []byte("not json")}); err == nil {
		t.Error("Handle accepted a malformed event")
	}
}
