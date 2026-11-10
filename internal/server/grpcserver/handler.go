package grpcserver

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
)

// ItemService is what the gRPC transport needs from the service layer.
type ItemService interface {
	Create(ctx context.Context, in domain.CreateItemInput) (domain.Item, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Item, error)
	List(ctx context.Context, in domain.ListItemsInput) ([]domain.Item, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// itemServer implements the generated ItemServiceServer. Each method maps the
// request to a domain input, calls the service and maps the result back.
// Errors are returned untouched: errorInterceptor turns them into statuses.
type itemServer struct {
	itemv1.UnimplementedItemServiceServer
	items ItemService
}

func (s *itemServer) CreateItem(ctx context.Context, req *itemv1.CreateItemRequest) (*itemv1.CreateItemResponse, error) {
	item, err := s.items.Create(ctx, domain.CreateItemInput{
		Name:        req.GetName(),
		Description: req.GetDescription(),
	})
	if err != nil {
		return nil, err
	}
	return &itemv1.CreateItemResponse{Item: toProto(item)}, nil
}

func (s *itemServer) GetItem(ctx context.Context, req *itemv1.GetItemRequest) (*itemv1.GetItemResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	item, err := s.items.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &itemv1.GetItemResponse{Item: toProto(item)}, nil
}

func (s *itemServer) ListItems(ctx context.Context, req *itemv1.ListItemsRequest) (*itemv1.ListItemsResponse, error) {
	items, total, err := s.items.List(ctx, domain.ListItemsInput{
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*itemv1.Item, 0, len(items))
	for _, it := range items {
		out = append(out, toProto(it))
	}
	return &itemv1.ListItemsResponse{Items: out, Total: total}, nil
}

func (s *itemServer) DeleteItem(ctx context.Context, req *itemv1.DeleteItemRequest) (*itemv1.DeleteItemResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.items.Delete(ctx, id); err != nil {
		return nil, err
	}
	return &itemv1.DeleteItemResponse{}, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.InvalidInput("id must be a UUID")
	}
	return id, nil
}

func toProto(it domain.Item) *itemv1.Item {
	return &itemv1.Item{
		Id:          it.ID.String(),
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   timestamppb.New(it.CreatedAt),
		UpdatedAt:   timestamppb.New(it.UpdatedAt),
	}
}
