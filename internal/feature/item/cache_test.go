package item_test

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/example/go-skeleton/internal/feature/item"
)

// openTestRedis connects to a real Redis. It skips the test unless
// TEST_REDIS_ADDR is set. Tests use random item IDs, so they do not collide.
func openTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return client
}

func TestRedisCacheRoundTrip(t *testing.T) {
	ctx := t.Context()
	cache := item.NewRedisCache(openTestRedis(t), time.Minute)
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := item.Item{ID: uuid.New(), Name: "widget", Description: "a thing", CreatedAt: now, UpdatedAt: now}

	if _, found, err := cache.Get(ctx, want.ID); err != nil || found {
		t.Fatalf("Get before Set = found %v, err %v; want a miss", found, err)
	}
	if err := cache.Set(ctx, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := cache.Get(ctx, want.ID)
	if err != nil || !found {
		t.Fatalf("Get after Set = found %v, err %v; want a hit", found, err)
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// A reader that loaded the item before an update must not be able to cache
// that old version after the update invalidated the cache.
func TestRedisCacheDropsStaleWriteAfterInvalidate(t *testing.T) {
	ctx := t.Context()
	cache := item.NewRedisCache(openTestRedis(t), time.Minute)
	stale := item.Item{ID: uuid.New(), Name: "old name"}

	if err := cache.Set(ctx, stale); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Invalidate(ctx, stale.ID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, found, err := cache.Get(ctx, stale.ID); err != nil || found {
		t.Fatalf("Get after Invalidate = found %v, err %v; want a miss", found, err)
	}

	// The late write of the reader that raced with the update.
	if err := cache.Set(ctx, stale); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, found, err := cache.Get(ctx, stale.ID); err != nil || found {
		t.Errorf("Get = %+v, found %v, err %v; the stale version must not be cached", got, found, err)
	}
}
