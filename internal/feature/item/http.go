package item

import (
	"context"

	openapi "github.com/example/go-skeleton/internal/gen/openapi"
)

// HTTPHandler implements the item operations of the generated
// openapi.StrictServerInterface. Each method maps the request to an input,
// calls the service and maps the result back. Errors are returned untouched:
// the HTTP server turns them into responses.
//
// The application wiring embeds the handlers of all features into one value
// that satisfies the whole generated interface.
type HTTPHandler struct {
	svc *Service
}

func NewHTTPHandler(svc *Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

func (h *HTTPHandler) ListItems(ctx context.Context, req openapi.ListItemsRequestObject) (openapi.ListItemsResponseObject, error) {
	var in ListInput
	if req.Params.Limit != nil {
		in.Limit = *req.Params.Limit
	}
	if req.Params.Offset != nil {
		in.Offset = *req.Params.Offset
	}

	items, total, err := h.svc.List(ctx, in)
	if err != nil {
		return nil, err
	}
	return openapi.ListItems200JSONResponse(toAPIItemList(items, total)), nil
}

func (h *HTTPHandler) SearchItems(ctx context.Context, req openapi.SearchItemsRequestObject) (openapi.SearchItemsResponseObject, error) {
	in := SearchInput{Query: req.Params.Q}
	if req.Params.Limit != nil {
		in.Limit = *req.Params.Limit
	}
	if req.Params.Offset != nil {
		in.Offset = *req.Params.Offset
	}

	items, total, err := h.svc.Search(ctx, in)
	if err != nil {
		return nil, err
	}
	return openapi.SearchItems200JSONResponse(toAPIItemList(items, total)), nil
}

func (h *HTTPHandler) CreateItem(ctx context.Context, req openapi.CreateItemRequestObject) (openapi.CreateItemResponseObject, error) {
	in := CreateInput{Name: req.Body.Name}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}

	item, err := h.svc.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return openapi.CreateItem201JSONResponse(toAPIItem(item)), nil
}

func (h *HTTPHandler) GetItem(ctx context.Context, req openapi.GetItemRequestObject) (openapi.GetItemResponseObject, error) {
	item, err := h.svc.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return openapi.GetItem200JSONResponse(toAPIItem(item)), nil
}

func (h *HTTPHandler) UpdateItem(ctx context.Context, req openapi.UpdateItemRequestObject) (openapi.UpdateItemResponseObject, error) {
	item, err := h.svc.Update(ctx, req.Id, UpdateInput{
		Name:        req.Body.Name,
		Description: req.Body.Description,
	})
	if err != nil {
		return nil, err
	}
	return openapi.UpdateItem200JSONResponse(toAPIItem(item)), nil
}

func (h *HTTPHandler) DeleteItem(ctx context.Context, req openapi.DeleteItemRequestObject) (openapi.DeleteItemResponseObject, error) {
	if err := h.svc.Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return openapi.DeleteItem204Response{}, nil
}

func toAPIItem(it Item) openapi.Item {
	return openapi.Item{
		Id:          it.ID,
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   it.CreatedAt,
		UpdatedAt:   it.UpdatedAt,
	}
}

func toAPIItemList(items []Item, total int64) openapi.ItemList {
	out := make([]openapi.Item, 0, len(items))
	for _, it := range items {
		out = append(out, toAPIItem(it))
	}
	return openapi.ItemList{Items: out, Total: total}
}
