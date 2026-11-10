package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/example/go-skeleton/internal/domain"
)

// ItemCache caches items in Redis as JSON.
type ItemCache struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewItemCache(client *goredis.Client, ttl time.Duration) *ItemCache {
	return &ItemCache{client: client, ttl: ttl}
}

func itemKey(id uuid.UUID) string { return "item:" + id.String() }

// Get returns the cached item; found is false on a cache miss.
func (c *ItemCache) Get(ctx context.Context, id uuid.UUID) (item domain.Item, found bool, err error) {
	raw, err := c.client.Get(ctx, itemKey(id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return domain.Item{}, false, nil
	}
	if err != nil {
		return domain.Item{}, false, fmt.Errorf("cache get item: %w", err)
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return domain.Item{}, false, fmt.Errorf("cache decode item: %w", err)
	}
	return item, true, nil
}

func (c *ItemCache) Set(ctx context.Context, item domain.Item) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("cache encode item: %w", err)
	}
	if err := c.client.Set(ctx, itemKey(item.ID), raw, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache set item: %w", err)
	}
	return nil
}

func (c *ItemCache) Delete(ctx context.Context, id uuid.UUID) error {
	if err := c.client.Del(ctx, itemKey(id)).Err(); err != nil {
		return fmt.Errorf("cache delete item: %w", err)
	}
	return nil
}
