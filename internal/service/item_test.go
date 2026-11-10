package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

type fakeStore struct {
	items map[uuid.UUID]domain.Item
	gets  int
}

func (f *fakeStore) Create(_ context.Context, item domain.Item) (domain.Item, error) {
	f.items[item.ID] = item
	return item, nil
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (domain.Item, error) {
	f.gets++
	item, ok := f.items[id]
	if !ok {
		return domain.Item{}, domain.ErrNotFound
	}
	return item, nil
}

func (f *fakeStore) List(_ context.Context, limit, _ int) ([]domain.Item, int64, error) {
	out := make([]domain.Item, 0, limit)
	for _, it := range f.items {
		if len(out) == limit {
			break
		}
		out = append(out, it)
	}
	return out, int64(len(f.items)), nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

type fakeCache struct {
	items map[uuid.UUID]domain.Item
	err   error
}

func (f *fakeCache) Get(_ context.Context, id uuid.UUID) (domain.Item, bool, error) {
	if f.err != nil {
		return domain.Item{}, false, f.err
	}
	item, ok := f.items[id]
	return item, ok, nil
}

func (f *fakeCache) Set(_ context.Context, item domain.Item) error {
	if f.err != nil {
		return f.err
	}
	f.items[item.ID] = item
	return nil
}

func (f *fakeCache) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.items, id)
	return f.err
}

type fakePublisher struct {
	published []domain.Item
	err       error
}

func (f *fakePublisher) PublishItemCreated(_ context.Context, item domain.Item) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, item)
	return nil
}

func newTestService(t *testing.T) (*ItemService, *fakeStore, *fakeCache, *fakePublisher) {
	t.Helper()
	store := &fakeStore{items: map[uuid.UUID]domain.Item{}}
	cache := &fakeCache{items: map[uuid.UUID]domain.Item{}}
	pub := &fakePublisher{}
	svc, err := NewItemService(store, cache, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewItemService: %v", err)
	}
	return svc, store, cache, pub
}

func TestCreate(t *testing.T) {
	svc, store, _, pub := newTestService(t)

	item, err := svc.Create(t.Context(), "  widget  ", "a thing")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Name != "widget" {
		t.Errorf("name = %q, want trimmed %q", item.Name, "widget")
	}
	if _, ok := store.items[item.ID]; !ok {
		t.Error("item was not stored")
	}
	if len(pub.published) != 1 || pub.published[0].ID != item.ID {
		t.Errorf("published = %v, want one event for %s", pub.published, item.ID)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	svc, store, _, pub := newTestService(t)

	for name, input := range map[string]string{"empty": "", "blank": "   ", "too long": string(make([]byte, maxNameLen+1))} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), input, "")
			if _, ok := errors.AsType[*domain.ValidationError](err); !ok {
				t.Fatalf("err = %v, want *domain.ValidationError", err)
			}
		})
	}
	if len(store.items) != 0 || len(pub.published) != 0 {
		t.Error("invalid input must not be stored or published")
	}
}

func TestCreateSucceedsWhenPublishFails(t *testing.T) {
	svc, store, _, pub := newTestService(t)
	pub.err = errors.New("broker down")

	item, err := svc.Create(t.Context(), "widget", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := store.items[item.ID]; !ok {
		t.Error("item was not stored")
	}
}

func TestGetReadsThroughCache(t *testing.T) {
	svc, store, cache, _ := newTestService(t)
	item, err := svc.Create(t.Context(), "widget", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := range 2 {
		got, err := svc.Get(t.Context(), item.ID)
		if err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}
		if got.ID != item.ID {
			t.Fatalf("Get #%d returned %s, want %s", i, got.ID, item.ID)
		}
	}
	if store.gets != 1 {
		t.Errorf("store reads = %d, want 1 (second read served from cache)", store.gets)
	}
	if _, ok := cache.items[item.ID]; !ok {
		t.Error("item was not cached")
	}
}

func TestGetFallsBackToStoreWhenCacheFails(t *testing.T) {
	svc, _, cache, _ := newTestService(t)
	item, err := svc.Create(t.Context(), "widget", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cache.err = errors.New("redis down")

	got, err := svc.Get(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != item.ID {
		t.Errorf("got %s, want %s", got.ID, item.ID)
	}
}

func TestGetNotFound(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.Get(t.Context(), uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteInvalidatesCache(t *testing.T) {
	svc, _, cache, _ := newTestService(t)
	item, _ := svc.Create(t.Context(), "widget", "")
	if _, err := svc.Get(t.Context(), item.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := svc.Delete(t.Context(), item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := cache.items[item.ID]; ok {
		t.Error("cache entry survived delete")
	}
	if err := svc.Delete(t.Context(), item.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestListValidatesPaging(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	for _, tc := range []struct{ limit, offset int }{{-1, 0}, {MaxPageSize + 1, 0}, {10, -1}} {
		_, _, err := svc.List(t.Context(), tc.limit, tc.offset)
		if _, ok := errors.AsType[*domain.ValidationError](err); !ok {
			t.Errorf("List(%d, %d) err = %v, want *domain.ValidationError", tc.limit, tc.offset, err)
		}
	}
	if _, _, err := svc.List(t.Context(), 0, 0); err != nil {
		t.Errorf("List with default limit: %v", err)
	}
}
