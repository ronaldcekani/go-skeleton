package order

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/example/go-skeleton/internal/domain"
	orderv1 "github.com/example/go-skeleton/internal/gen/proto/order/v1"
)

// GRPCServer implements the generated OrderServiceServer.
type GRPCServer struct {
	orderv1.UnimplementedOrderServiceServer
	svc *Service
}

func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

// Register adds the order service to a gRPC server.
func (s *GRPCServer) Register(r grpc.ServiceRegistrar) {
	orderv1.RegisterOrderServiceServer(r, s)
}

func (s *GRPCServer) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	customerID, err := parseID("customer_id", req.GetCustomerId())
	if err != nil {
		return nil, err
	}
	in := CreateInput{
		CustomerID: customerID,
		Currency:   Currency(req.GetCurrency()),
		Lines:      make([]LineInput, 0, len(req.GetLines())),
	}
	for _, l := range req.GetLines() {
		itemID, err := parseID("item_id", l.GetItemId())
		if err != nil {
			return nil, err
		}
		in.Lines = append(in.Lines, LineInput{ItemID: itemID, Quantity: int(l.GetQuantity())})
	}

	order, err := s.svc.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return &orderv1.CreateOrderResponse{Order: toProto(order)}, nil
}

func (s *GRPCServer) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	order, err := s.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &orderv1.GetOrderResponse{Order: toProto(order)}, nil
}

func parseID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.InvalidInput("%s must be a UUID", field)
	}
	return id, nil
}

func toProto(o Order) *orderv1.Order {
	out := &orderv1.Order{
		Id:          o.ID.String(),
		CustomerId:  o.CustomerID.String(),
		Currency:    string(o.Currency),
		Lines:       make([]*orderv1.OrderLine, 0, len(o.Lines)),
		TotalAmount: o.TotalAmount,
		CreatedAt:   timestamppb.New(o.CreatedAt),
	}
	for _, l := range o.Lines {
		out.Lines = append(out.Lines, &orderv1.OrderLine{
			ItemId:              l.ItemID.String(),
			Quantity:            int32(l.Quantity),
			UnitAmount:          l.UnitAmount,
			DiscountBasisPoints: int32(l.DiscountBasisPoints),
			Amount:              l.Amount,
		})
	}
	return out
}
