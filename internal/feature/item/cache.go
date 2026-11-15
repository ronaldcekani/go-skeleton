package item

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	// tombstone marks an item that was just changed or deleted. It is not
	// valid JSON, so it can never be mistaken for a cached item.
	tombstone = "-"
	// tombstoneTTL has to outlast a read that started before the change: the
	// time between loading an item from Postgres and caching it.
	tombstoneTTL = 10 * time.Second
)

// RedisCache caches items in Redis as JSON.
//
// A reader that misses the cache loads the item from Postgres and then caches
// it. If the item changes between those two steps, the reader would cache
// the old version after the writer invalidated it. To prevent that,
// Invalidate leaves a short-lived tombstone instead of deleting the key, and
// Set never overwrites an existing key: the late write of the old version is
// dropped, and the next read after the tombstone expired caches the new one.
type RedisCache struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewRedisCache(client *goredis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, ttl: ttl}
}

func cacheKey(id uuid.UUID) string { return "item:" + id.String() }

// cachedItem is the JSON stored in Redis. Entries written in an older format
// only live for the cache TTL, but a field that changes meaning needs a new
// key prefix.
type cachedItem struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Get returns the cached item; found is false on a cache miss.
func (c *RedisCache) Get(ctx context.Context, id uuid.UUID) (item Item, found bool, err error) {
	raw, err := c.client.Get(ctx, cacheKey(id)).Bytes()
	if errors.Is(err, goredis.Nil) || string(raw) == tombstone {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, fmt.Errorf("cache get item: %w", err)
	}
	var cached cachedItem
	if err := json.Unmarshal(raw, &cached); err != nil {
		return Item{}, false, fmt.Errorf("cache decode item: %w", err)
	}
	return Item(cached), true, nil
}

// Set caches the item unless the key already holds an item or a tombstone.
func (c *RedisCache) Set(ctx context.Context, item Item) error {
	raw, err := json.Marshal(cachedItem(item))
	if err != nil {
		return fmt.Errorf("cache encode item: %w", err)
	}
	if err := c.client.SetNX(ctx, cacheKey(item.ID), raw, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache set item: %w", err)
	}
	return nil
}

// Invalidate drops the cached item after it was changed or deleted.
func (c *RedisCache) Invalidate(ctx context.Context, id uuid.UUID) error {
	if err := c.client.Set(ctx, cacheKey(id), tombstone, tombstoneTTL).Err(); err != nil {
		return fmt.Errorf("cache invalidate item: %w", err)
	}
	return nil
}
