// Package redis opens an instrumented Redis client.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"
)

type Options struct {
	Addr     string
	Username string
	Password string
	DB       int
}

// Open connects to Redis and verifies the connection. Commands produce spans
// and client metrics.
func Open(ctx context.Context, opts Options) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     opts.Addr,
		Username: opts.Username,
		Password: opts.Password,
		DB:       opts.DB,
	})
	if err := redisotel.InstrumentTracing(client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("instrument redis tracing: %w", err)
	}
	if err := redisotel.InstrumentMetrics(client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("instrument redis metrics: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
