package item

import (
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	openapi "github.com/example/go-skeleton/internal/gen/openapi"
)

// The transport tests run the handlers on a real Service over the fakes from
// service_test.go, so they cover the mapping in both directions.

func TestHTTPCreateAndGet(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	h := NewHTTPHandler(svc)
	desc := "a thing"

	resp, err := h.CreateItem(t.Context(), openapi.CreateItemRequestObject{
		Body: &openapi.CreateItemRequest{Name: "widget", Description: &desc},
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	created, ok := resp.(openapi.CreateItem201JSONResponse)
	if !ok {
		t.Fatalf("response is %T, want a 201", resp)
	}
	if created.Id == uuid.Nil || created.Name != "widget" || created.Description != "a thing" {
		t.Errorf("created = %+v", created)
	}

	got, err := h.GetItem(t.Context(), openapi.GetItemRequestObject{Id: created.Id})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got != openapi.GetItem200JSONResponse(created) {
		t.Errorf("got %+v, want %+v", got, created)
	}

	_, err = h.GetItem(t.Context(), openapi.GetItemRequestObject{Id: uuid.New()})
	if !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("unknown id: err = %v, want not found", err)
	}
}

func TestHTTPUpdateSendsOnlySetFields(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	h := NewHTTPHandler(svc)
	item, err := svc.Create(t.Context(), CreateInput{Name: "gadget", Description: "old"})
	if err != nil {
		t.Fatal(err)
	}
	desc := "new"

	resp, err := h.UpdateItem(t.Context(), openapi.UpdateItemRequestObject{
		Id:   item.ID,
		Body: &openapi.UpdateItemRequest{Description: &desc},
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	updated, ok := resp.(openapi.UpdateItem200JSONResponse)
	if !ok {
		t.Fatalf("response is %T, want a 200", resp)
	}
	if updated.Id != item.ID || updated.Name != "gadget" || updated.Description != "new" {
		t.Errorf("updated = %+v, want only the description changed", updated)
	}
	if stored := store.items[item.ID]; stored.Name != "gadget" || stored.Description != "new" {
		t.Errorf("stored = %+v, want only the description changed", stored)
	}
}

func TestHTTPSearchAndList(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	h := NewHTTPHandler(svc)
	if _, err := svc.Create(t.Context(), CreateInput{Name: "widget"}); err != nil {
		t.Fatal(err)
	}
	limit := 5

	resp, err := h.SearchItems(t.Context(), openapi.SearchItemsRequestObject{
		Params: openapi.SearchItemsParams{Q: "wid", Limit: &limit},
	})
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	found, ok := resp.(openapi.SearchItems200JSONResponse)
	if !ok {
		t.Fatalf("response is %T, want a 200", resp)
	}
	if found.Total != 1 || len(found.Items) != 1 || found.Items[0].Name != "widget" {
		t.Errorf("found = %+v, want the one widget", found)
	}
	if store.lastQuery != "wid" || store.lastLimit != 5 {
		t.Errorf("store got query %q limit %d, want what the request asked for", store.lastQuery, store.lastLimit)
	}

	// No limit in the request: the service applies the default.
	listed, err := h.ListItems(t.Context(), openapi.ListItemsRequestObject{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if list, ok := listed.(openapi.ListItems200JSONResponse); !ok || list.Total != 1 || len(list.Items) != 1 {
		t.Errorf("listed = %+v, want the one widget", listed)
	}
}
