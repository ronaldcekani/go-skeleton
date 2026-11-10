package httpserver

import (
	"context"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/gen/openapi"
)

// ItemService is what the HTTP transport needs from the service layer.
type ItemService interface {
	Create(ctx context.Context, in domain.CreateItemInput) (domain.Item, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Item, error)
	List(ctx context.Context, in domain.ListItemsInput) ([]domain.Item, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// handler implements the generated openapi.StrictServerInterface. Each method
// maps the request to a domain input, calls the service and maps the result
// back. Errors are returned untouched: writeError turns them into responses.
type handler struct {
	items ItemService
}

var _ openapi.StrictServerInterface = (*handler)(nil)

func (h *handler) ListItems(ctx context.Context, req openapi.ListItemsRequestObject) (openapi.ListItemsResponseObject, error) {
	var in domain.ListItemsInput
	if req.Params.Limit != nil {
		in.Limit = *req.Params.Limit
	}
	if req.Params.Offset != nil {
		in.Offset = *req.Params.Offset
	}

	items, total, err := h.items.List(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.Item, 0, len(items))
	for _, it := range items {
		out = append(out, toAPI(it))
	}
	return openapi.ListItems200JSONResponse{Items: out, Total: total}, nil
}

func (h *handler) CreateItem(ctx context.Context, req openapi.CreateItemRequestObject) (openapi.CreateItemResponseObject, error) {
	in := domain.CreateItemInput{Name: req.Body.Name}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}

	item, err := h.items.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return openapi.CreateItem201JSONResponse(toAPI(item)), nil
}

func (h *handler) GetItem(ctx context.Context, req openapi.GetItemRequestObject) (openapi.GetItemResponseObject, error) {
	item, err := h.items.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return openapi.GetItem200JSONResponse(toAPI(item)), nil
}

func (h *handler) DeleteItem(ctx context.Context, req openapi.DeleteItemRequestObject) (openapi.DeleteItemResponseObject, error) {
	if err := h.items.Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return openapi.DeleteItem204Response{}, nil
}

func toAPI(it domain.Item) openapi.Item {
	return openapi.Item{
		Id:          it.ID,
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   it.CreatedAt,
		UpdatedAt:   it.UpdatedAt,
	}
}
