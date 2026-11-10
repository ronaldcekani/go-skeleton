package httpserver

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/gen/openapi"
)

// ItemService is what the HTTP transport needs from the service layer.
type ItemService interface {
	Create(ctx context.Context, name, description string) (domain.Item, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Item, error)
	List(ctx context.Context, limit, offset int) ([]domain.Item, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// handler implements the generated openapi.StrictServerInterface. Errors that
// are not mapped to a documented response are returned as-is and become a 500.
type handler struct {
	items ItemService
}

var _ openapi.StrictServerInterface = (*handler)(nil)

func (h *handler) ListItems(ctx context.Context, req openapi.ListItemsRequestObject) (openapi.ListItemsResponseObject, error) {
	var limit, offset int
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if req.Params.Offset != nil {
		offset = *req.Params.Offset
	}

	items, total, err := h.items.List(ctx, limit, offset)
	if err != nil {
		if verr, ok := errors.AsType[*domain.ValidationError](err); ok {
			return openapi.ListItems400JSONResponse{Code: "bad_request", Message: verr.Message}, nil
		}
		return nil, err
	}
	out := make([]openapi.Item, 0, len(items))
	for _, it := range items {
		out = append(out, toAPI(it))
	}
	return openapi.ListItems200JSONResponse{Items: out, Total: total}, nil
}

func (h *handler) CreateItem(ctx context.Context, req openapi.CreateItemRequestObject) (openapi.CreateItemResponseObject, error) {
	var description string
	if req.Body.Description != nil {
		description = *req.Body.Description
	}

	item, err := h.items.Create(ctx, req.Body.Name, description)
	if err != nil {
		if verr, ok := errors.AsType[*domain.ValidationError](err); ok {
			return openapi.CreateItem400JSONResponse{Code: "bad_request", Message: verr.Message}, nil
		}
		return nil, err
	}
	return openapi.CreateItem201JSONResponse(toAPI(item)), nil
}

func (h *handler) GetItem(ctx context.Context, req openapi.GetItemRequestObject) (openapi.GetItemResponseObject, error) {
	item, err := h.items.Get(ctx, req.Id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return openapi.GetItem404JSONResponse{Code: "not_found", Message: "item not found"}, nil
		}
		return nil, err
	}
	return openapi.GetItem200JSONResponse(toAPI(item)), nil
}

func (h *handler) DeleteItem(ctx context.Context, req openapi.DeleteItemRequestObject) (openapi.DeleteItemResponseObject, error) {
	if err := h.items.Delete(ctx, req.Id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return openapi.DeleteItem404JSONResponse{Code: "not_found", Message: "item not found"}, nil
		}
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
