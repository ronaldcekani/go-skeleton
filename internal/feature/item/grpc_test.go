package item

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
)

func TestGRPCCreateAndGet(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	s := NewGRPCServer(svc)

	created, err := s.CreateItem(t.Context(), &itemv1.CreateItemRequest{Name: "widget", Description: "a thing"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	item := created.GetItem()
	if _, err := uuid.Parse(item.GetId()); err != nil || item.GetName() != "widget" || item.GetDescription() != "a thing" {
		t.Errorf("created = %v", item)
	}

	got, err := s.GetItem(t.Context(), &itemv1.GetItemRequest{Id: item.GetId()})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !proto.Equal(got.GetItem(), item) {
		t.Errorf("got %v, want %v", got.GetItem(), item)
	}

	for name, id := range map[string]string{"bad id": "nope", "empty id": ""} {
		if _, err := s.GetItem(t.Context(), &itemv1.GetItemRequest{Id: id}); !domain.IsKind(err, domain.ErrKindInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
	_, err = s.GetItem(t.Context(), &itemv1.GetItemRequest{Id: uuid.NewString()})
	if !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("unknown id: err = %v, want not found", err)
	}
}

func TestGRPCUpdateSendsOnlySetFields(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	s := NewGRPCServer(svc)
	item, err := svc.Create(t.Context(), CreateInput{Name: "gadget", Description: "old"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := s.UpdateItem(t.Context(), &itemv1.UpdateItemRequest{
		Id:          item.ID.String(),
		Description: proto.String("new"),
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if got := resp.GetItem(); got.GetId() != item.ID.String() || got.GetName() != "gadget" || got.GetDescription() != "new" {
		t.Errorf("updated = %v, want only the description changed", got)
	}
	if stored := store.items[item.ID]; stored.Name != "gadget" || stored.Description != "new" {
		t.Errorf("stored = %+v, want only the description changed", stored)
	}

	for name, req := range map[string]*itemv1.UpdateItemRequest{
		"bad id":    {Id: "nope", Name: proto.String("x")},
		"no fields": {Id: item.ID.String()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.UpdateItem(t.Context(), req); !domain.IsKind(err, domain.ErrKindInvalidInput) {
				t.Errorf("err = %v, want invalid input", err)
			}
		})
	}
}

func TestGRPCSearch(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	s := NewGRPCServer(svc)
	if _, err := svc.Create(t.Context(), CreateInput{Name: "widget"}); err != nil {
		t.Fatal(err)
	}

	resp, err := s.SearchItems(t.Context(), &itemv1.SearchItemsRequest{Query: "wid", Limit: 5})
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if resp.GetTotal() != 1 || len(resp.GetItems()) != 1 || resp.GetItems()[0].GetName() != "widget" {
		t.Errorf("found = %v, want the one widget", resp)
	}
	if store.lastQuery != "wid" || store.lastLimit != 5 {
		t.Errorf("store got query %q limit %d, want what the request asked for", store.lastQuery, store.lastLimit)
	}
}
