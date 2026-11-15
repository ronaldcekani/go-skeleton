package kafka

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// With no broker reachable, Publish must return at once, and the record that
// could not be delivered must show up in the log.
func TestPublishDoesNotWaitForTheBroker(t *testing.T) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers("127.0.0.1:1"), // nothing listens here
		kgo.RecordDeliveryTimeout(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	logs := &syncBuffer{}
	p := &Producer{client: client, log: slog.New(slog.NewTextHandler(logs, nil))}

	start := time.Now()
	p.Publish(t.Context(), "items.events", []byte("key-1"), []byte("value"))
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Publish took %v with the broker down, want it to return immediately", elapsed)
	}

	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), "kafka record not delivered") {
		if time.Now().After(deadline) {
			t.Fatalf("the undelivered record was never logged; log: %q", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "key-1") {
		t.Errorf("log does not identify the record: %q", logs.String())
	}
}
