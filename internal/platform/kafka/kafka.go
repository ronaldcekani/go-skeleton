// Package kafka wraps franz-go with OpenTelemetry instrumentation and exposes
// a small producer and consumer-group API.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

type Options struct {
	Brokers  []string
	ClientID string
}

func newKotel() (*kotel.Kotel, *kotel.Tracer) {
	tracer := kotel.NewTracer(
		kotel.TracerProvider(otel.GetTracerProvider()),
		kotel.TracerPropagator(otel.GetTextMapPropagator()),
	)
	meter := kotel.NewMeter(kotel.MeterProvider(otel.GetMeterProvider()))
	return kotel.NewKotel(kotel.WithTracer(tracer), kotel.WithMeter(meter)), tracer
}

func ping(ctx context.Context, client *kgo.Client) error {
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		return fmt.Errorf("ping kafka: %w", err)
	}
	return nil
}

// Producer publishes records asynchronously: Publish buffers the record and
// the client delivers it in the background, retrying until the delivery
// timeout. A slow or unreachable broker therefore never delays the caller.
type Producer struct {
	client *kgo.Client
	log    *slog.Logger
}

func NewProducer(ctx context.Context, opts Options, log *slog.Logger) (*Producer, error) {
	k, _ := newKotel()
	client, err := kgo.NewClient(
		kgo.SeedBrokers(opts.Brokers...),
		kgo.ClientID(opts.ClientID),
		kgo.WithHooks(k.Hooks()...),
		kgo.AllowAutoTopicCreation(),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.RecordDeliveryTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka producer: %w", err)
	}
	if err := ping(ctx, client); err != nil {
		client.Close()
		return nil, err
	}
	return &Producer{client: client, log: log}, nil
}

// Publish queues one record for delivery and returns immediately. The trace
// context in ctx is injected into the record headers.
//
// Delivery is best effort: a record that cannot be delivered within the
// delivery timeout, or that does not fit in the buffer because the brokers
// have been unreachable for a while, is dropped and logged. Records still
// buffered when the process dies are lost; Close flushes them on a normal
// shutdown. Events that must never be lost need a transactional outbox.
func (p *Producer) Publish(ctx context.Context, topic string, key, value []byte) {
	rec := &kgo.Record{Topic: topic, Key: key, Value: value}
	// The record outlives the request that produced it, so it must not be
	// cancelled with it. TryProduce fails a record instead of blocking when
	// the buffer is full.
	p.client.TryProduce(context.WithoutCancel(ctx), rec, func(rec *kgo.Record, err error) {
		if err != nil {
			p.log.ErrorContext(rec.Context, "kafka record not delivered",
				slog.String("topic", rec.Topic), slog.String("key", string(rec.Key)), slog.Any("error", err))
		}
	})
}

// Close flushes buffered records, bounded by ctx, then closes the client.
func (p *Producer) Close(ctx context.Context) error {
	err := p.client.Flush(ctx)
	p.client.Close()
	if err != nil {
		return fmt.Errorf("flush kafka producer: %w", err)
	}
	return nil
}

// Handler processes one record. Returning an error logs it and moves on; a
// handler that must not lose messages should retry or dead-letter internally.
type Handler func(ctx context.Context, rec *kgo.Record) error

// Consumer reads topics as part of a consumer group, committing offsets only
// after records have been handled (at-least-once delivery).
type Consumer struct {
	client  *kgo.Client
	tracer  *kotel.Tracer
	handler Handler
	log     *slog.Logger
}

func NewConsumer(ctx context.Context, opts Options, group string, topics []string, handler Handler, log *slog.Logger) (*Consumer, error) {
	k, tracer := newKotel()
	client, err := kgo.NewClient(
		kgo.SeedBrokers(opts.Brokers...),
		kgo.ClientID(opts.ClientID),
		kgo.WithHooks(k.Hooks()...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.AllowAutoTopicCreation(),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka consumer: %w", err)
	}
	if err := ping(ctx, client); err != nil {
		client.Close()
		return nil, err
	}
	return &Consumer{client: client, tracer: tracer, handler: handler, log: log}, nil
}

// Close leaves the consumer group, so partitions are reassigned immediately,
// and closes the client. Call it after Run has returned.
func (c *Consumer) Close() {
	c.client.Close()
}

// Run polls until ctx is cancelled. The batch being processed when ctx is
// cancelled is finished and committed before Run returns.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			if !errors.Is(err, context.Canceled) {
				c.log.ErrorContext(ctx, "kafka fetch failed",
					slog.String("topic", topic), slog.Int("partition", int(partition)), slog.Any("error", err))
			}
		})

		// Handling and committing do not observe ctx, so a batch that is in
		// flight when shutdown starts is completed and committed.
		fetches.EachRecord(c.handle)
		if fetches.NumRecords() == 0 {
			continue
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.client.CommitUncommittedOffsets(commitCtx); err != nil {
			c.log.ErrorContext(ctx, "kafka commit failed", slog.Any("error", err))
		}
		cancel()
	}
}

func (c *Consumer) handle(rec *kgo.Record) {
	// Continues the producer's trace from the record headers.
	ctx, span := c.tracer.WithProcessSpan(rec)
	defer span.End()

	if err := c.handler(ctx, rec); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "kafka record handling failed",
			slog.String("topic", rec.Topic),
			slog.Int("partition", int(rec.Partition)),
			slog.Int64("offset", rec.Offset),
			slog.Any("error", err))
	}
}
