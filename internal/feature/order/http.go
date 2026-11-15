package order

import (
	"context"

	openapi "github.com/example/go-skeleton/internal/gen/openapi"
)

// HTTPHandler implements the order operations of the generated
// openapi.StrictServerInterface.
type HTTPHandler struct {
	svc *Service
}

func NewHTTPHandler(svc *Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

func (h *HTTPHandler) CreateOrder(ctx context.Context, req openapi.CreateOrderRequestObject) (openapi.CreateOrderResponseObject, error) {
	in := CreateInput{
		CustomerID: req.Body.CustomerId,
		Currency:   Currency(req.Body.Currency),
		Lines:      make([]LineInput, 0, len(req.Body.Lines)),
	}
	for _, l := range req.Body.Lines {
		in.Lines = append(in.Lines, LineInput{ItemID: l.ItemId, Quantity: l.Quantity})
	}

	order, err := h.svc.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return openapi.CreateOrder201JSONResponse(toAPIOrder(order)), nil
}

func (h *HTTPHandler) GetOrder(ctx context.Context, req openapi.GetOrderRequestObject) (openapi.GetOrderResponseObject, error) {
	order, err := h.svc.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return openapi.GetOrder200JSONResponse(toAPIOrder(order)), nil
}

func toAPIOrder(o Order) openapi.Order {
	out := openapi.Order{
		Id:          o.ID,
		CustomerId:  o.CustomerID,
		Currency:    string(o.Currency),
		Lines:       make([]openapi.OrderLine, 0, len(o.Lines)),
		TotalAmount: o.TotalAmount,
		CreatedAt:   o.CreatedAt,
	}
	for _, l := range o.Lines {
		out.Lines = append(out.Lines, openapi.OrderLine{
			ItemId:              l.ItemID,
			Quantity:            l.Quantity,
			UnitAmount:          l.UnitAmount,
			DiscountBasisPoints: l.DiscountBasisPoints,
			Amount:              l.Amount,
		})
	}
	return out
}
