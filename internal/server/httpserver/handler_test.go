package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	openapi "github.com/example/go-skeleton/internal/gen/openapi"
	"github.com/example/go-skeleton/internal/server/policy"
)

// fakeItems records the input of the last call and answers with item.
type fakeItems struct {
	item   domain.Item
	update domain.UpdateItemInput
	search domain.SearchItemsInput
}

func (f *fakeItems) Create(context.Context, domain.CreateItemInput) (domain.Item, error) {
	return f.item, nil
}
func (f *fakeItems) Get(context.Context, uuid.UUID) (domain.Item, error) { return f.item, nil }
func (f *fakeItems) List(context.Context, domain.ListItemsInput) ([]domain.Item, int64, error) {
	return []domain.Item{f.item}, 1, nil
}
func (f *fakeItems) Update(_ context.Context, _ uuid.UUID, in domain.UpdateItemInput) (domain.Item, error) {
	f.update = in
	return f.item, nil
}
func (f *fakeItems) Search(_ context.Context, in domain.SearchItemsInput) ([]domain.Item, int64, error) {
	f.search = in
	return []domain.Item{f.item}, 1, nil
}
func (f *fakeItems) Delete(context.Context, uuid.UUID) error { return nil }

// newTestServer serves the full HTTP stack (routing, OpenAPI validation,
// strict handlers) with authentication disabled.
func newTestServer(t *testing.T, items ItemService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer, err := policy.NewEnforcer(nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.NewTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{}, items, func(context.Context) error { return nil }, enforcer, table, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestUpdateItem(t *testing.T) {
	items := &fakeItems{item: domain.Item{ID: uuid.New(), Name: "gadget"}}
	ts := newTestServer(t, items)

	resp := do(t, http.MethodPatch, ts.URL+"/v1/items/"+items.item.ID.String(), `{"description":"new"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got openapi.Item
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Id != items.item.ID {
		t.Errorf("id = %s, want %s", got.Id, items.item.ID)
	}
	if items.update.Name != nil || items.update.Description == nil || *items.update.Description != "new" {
		t.Errorf("service got %+v, want only description set", items.update)
	}

	for name, body := range map[string]string{
		"empty body":    `{}`,
		"empty name":    `{"name":""}`,
		"unknown field": `{"colour":"red"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPatch, ts.URL+"/v1/items/"+items.item.ID.String(), body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestSearchItems(t *testing.T) {
	items := &fakeItems{item: domain.Item{ID: uuid.New(), Name: "widget"}}
	ts := newTestServer(t, items)

	resp := do(t, http.MethodGet, ts.URL+"/v1/items:search?q=wid&limit=5", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got openapi.ItemList
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || len(got.Items) != 1 {
		t.Errorf("got %d items, total %d; want 1, 1", len(got.Items), got.Total)
	}
	if items.search != (domain.SearchItemsInput{Query: "wid", Limit: 5}) {
		t.Errorf("service got %+v", items.search)
	}

	if resp := do(t, http.MethodGet, ts.URL+"/v1/items:search", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing q: status = %d, want 400", resp.StatusCode)
	}
}
