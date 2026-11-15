package item

import (
	"testing"

	openapi "github.com/example/go-skeleton/internal/gen/openapi"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
	"github.com/example/go-skeleton/internal/testkit/mapcheck"
)

// Every mapper between Item and a storage or wire format is checked here:
// a field added to Item, to the table, to the API or to the event fails this
// test until each mapper deals with it (or names it as left out on purpose).
func TestMappersAreComplete(t *testing.T) {
	t.Run("row to item", func(t *testing.T) {
		mapcheck.Check(t, itemRow.toItem)
	})
	t.Run("item to cache entry and back", func(t *testing.T) {
		mapcheck.Check(t, func(it Item) cachedItem { return cachedItem(it) })
		mapcheck.Check(t, func(c cachedItem) Item { return Item(c) })
	})
	t.Run("item to event payload", func(t *testing.T) {
		mapcheck.Check(t, toPayload)
	})
	t.Run("item to HTTP response", func(t *testing.T) {
		mapcheck.Check(t, func(it Item) openapi.Item { return toAPIItem(it) })
	})
	t.Run("item to gRPC response", func(t *testing.T) {
		mapcheck.Check(t, func(it Item) *itemv1.Item { return toProto(it) })
	})
}
