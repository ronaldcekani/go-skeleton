package item

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

type fakeStore struct {
	items map[uuid.UUID]Item
	gets  int

	lastQuery string
	lastLimit int
}

func (f *fakeStore) Create(_ context.Context, item Item) (Item, error) {
	f.items[item.ID] = item
	return item, nil
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (Item, error) {
	f.gets++
	item, ok := f.items[id]
	if !ok {
		return Item{}, domain.NotFound("item not found")
	}
	return item, nil
}

func (f *fakeStore) List(_ context.Context, limit, _ int) ([]Item, int64, error) {
	out := make([]Item, 0, limit)
	for _, it := range f.items {
		if len(out) == limit {
			break
		}
		out = append(out, it)
	}
	return out, int64(len(f.items)), nil
}

func (f *fakeStore) Update(_ context.Context, id uuid.UUID, in UpdateInput) (Item, error) {
	item, ok := f.items[id]
	if !ok {
		return Item{}, domain.NotFound("item not found")
	}
	if in.Name != nil {
		item.Name = *in.Name
	}
	if in.Description != nil {
		item.Description = *in.Description
	}
	f.items[id] = item
	return item, nil
}

func (f *fakeStore) Search(_ context.Context, query string, limit, _ int) ([]Item, int64, error) {
	f.lastQuery, f.lastLimit = query, limit
	q := strings.ToLower(query)
	var out []Item
	for _, it := range f.items {
		if strings.Contains(strings.ToLower(it.Name), q) || strings.Contains(strings.ToLower(it.Description), q) {
			out = append(out, it)
		}
	}
	total := int64(len(out))
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.items[id]; !ok {
		return domain.NotFound("item not found")
	}
	delete(f.items, id)
	return nil
}

type fakeCache struct {
	items map[uuid.UUID]Item
	err   error
}

func (f *fakeCache) Get(_ context.Context, id uuid.UUID) (Item, bool, error) {
	if f.err != nil {
		return Item{}, false, f.err
	}
	item, ok := f.items[id]
	return item, ok, nil
}

func (f *fakeCache) Set(_ context.Context, item Item) error {
	if f.err != nil {
		return f.err
	}
	f.items[item.ID] = item
	return nil
}

func (f *fakeCache) Invalidate(_ context.Context, id uuid.UUID) error {
	delete(f.items, id)
	return f.err
}

type fakePublisher struct {
	published []Item
	err       error
}

func (f *fakePublisher) PublishCreated(_ context.Context, item Item) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, item)
	return nil
}

func newTestService(t *testing.T) (*Service, *fakeStore, *fakeCache, *fakePublisher) {
	t.Helper()
	store := &fakeStore{items: map[uuid.UUID]Item{}}
	cache := &fakeCache{items: map[uuid.UUID]Item{}}
	pub := &fakePublisher{}
	svc, err := NewService(store, cache, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, cache, pub
}

func TestCreate(t *testing.T) {
	svc, store, _, pub := newTestService(t)

	item, err := svc.Create(t.Context(), CreateInput{Name: "  widget  ", Description: "a thing"})
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

	for name, input := range map[string]string{"empty": "", "blank": "   ", "too long": strings.Repeat("n", MaxNameLen+1)} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), CreateInput{Name: input})
			if !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Fatalf("err = %v, want invalid input", err)
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

	item, err := svc.Create(t.Context(), CreateInput{Name: "widget", Description: ""})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := store.items[item.ID]; !ok {
		t.Error("item was not stored")
	}
}

func TestGetReadsThroughCache(t *testing.T) {
	svc, store, cache, _ := newTestService(t)
	item, err := svc.Create(t.Context(), CreateInput{Name: "widget", Description: ""})
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
	item, err := svc.Create(t.Context(), CreateInput{Name: "widget", Description: ""})
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
	if _, err := svc.Get(t.Context(), uuid.New()); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestDeleteInvalidatesCache(t *testing.T) {
	svc, _, cache, _ := newTestService(t)
	item, _ := svc.Create(t.Context(), CreateInput{Name: "widget", Description: ""})
	if _, err := svc.Get(t.Context(), item.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := svc.Delete(t.Context(), item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := cache.items[item.ID]; ok {
		t.Error("cache entry survived delete")
	}
	if err := svc.Delete(t.Context(), item.ID); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("second delete err = %v, want not found", err)
	}
}

func TestListValidatesPaging(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	for _, tc := range []struct{ limit, offset int }{{-1, 0}, {MaxPageSize + 1, 0}, {10, -1}} {
		_, _, err := svc.List(t.Context(), ListInput{Limit: tc.limit, Offset: tc.offset})
		if !domain.IsKind(err, domain.ErrKindInvalidInput) {
			t.Errorf("List(%d, %d) err = %v, want invalid input", tc.limit, tc.offset, err)
		}
	}
	if _, _, err := svc.List(t.Context(), ListInput{}); err != nil {
		t.Errorf("List with default limit: %v", err)
	}
}

func TestUpdateChangesOnlySetFields(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	item, err := svc.Create(t.Context(), CreateInput{Name: "widget", Description: "a thing"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := "  gadget  "
	got, err := svc.Update(t.Context(), item.ID, UpdateInput{Name: &name})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "gadget" {
		t.Errorf("name = %q, want trimmed %q", got.Name, "gadget")
	}
	if got.Description != "a thing" {
		t.Errorf("description = %q, want it unchanged", got.Description)
	}
	if store.items[item.ID].Name != "gadget" {
		t.Error("update was not stored")
	}
}

func TestUpdateInvalidatesCache(t *testing.T) {
	svc, _, cache, _ := newTestService(t)
	item, _ := svc.Create(t.Context(), CreateInput{Name: "widget"})
	if _, err := svc.Get(t.Context(), item.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	desc := "new"
	if _, err := svc.Update(t.Context(), item.ID, UpdateInput{Description: &desc}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := cache.items[item.ID]; ok {
		t.Error("cache entry survived update")
	}
	got, err := svc.Get(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "new" {
		t.Errorf("Get after update returned description %q, want %q", got.Description, "new")
	}
}

func TestUpdateErrors(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	item, _ := svc.Create(t.Context(), CreateInput{Name: "widget"})
	blank, name := "   ", "gadget"

	for label, tc := range map[string]struct {
		id   uuid.UUID
		in   UpdateInput
		kind domain.ErrKind
	}{
		"no fields":  {item.ID, UpdateInput{}, domain.ErrKindInvalidInput},
		"blank name": {item.ID, UpdateInput{Name: &blank}, domain.ErrKindInvalidInput},
		"not found":  {uuid.New(), UpdateInput{Name: &name}, domain.ErrKindNotFound},
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := svc.Update(t.Context(), tc.id, tc.in); !domain.IsKind(err, tc.kind) {
				t.Errorf("err = %v, want %v", err, tc.kind)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	for _, in := range []CreateInput{
		{Name: "Blue widget"},
		{Name: "gadget", Description: "works with any WIDGET"},
		{Name: "sprocket"},
	} {
		if _, err := svc.Create(t.Context(), in); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	items, total, err := svc.Search(t.Context(), SearchInput{Query: "  widget "})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Errorf("got %d items, total %d; want 2, 2", len(items), total)
	}
	if store.lastQuery != "widget" || store.lastLimit != DefaultPageSize {
		t.Errorf("store got query %q limit %d, want trimmed query and default limit", store.lastQuery, store.lastLimit)
	}
}

func TestSearchValidatesInput(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	for label, in := range map[string]SearchInput{
		"empty query":    {},
		"blank query":    {Query: "   "},
		"query too long": {Query: strings.Repeat("q", MaxSearchQueryLen+1)},
		"bad limit":      {Query: "widget", Limit: MaxPageSize + 1},
		"bad offset":     {Query: "widget", Offset: -1},
	} {
		t.Run(label, func(t *testing.T) {
			if _, _, err := svc.Search(t.Context(), in); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("err = %v, want invalid input", err)
			}
		})
	}
}
