package item

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
)

// GRPCServer implements the generated ItemServiceServer. Each method maps the
// request to an input, calls the service and maps the result back. Errors are
// returned untouched: the gRPC server turns them into statuses.
type GRPCServer struct {
	itemv1.UnimplementedItemServiceServer
	svc *Service
}

func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

// Register adds the item service to a gRPC server.
func (s *GRPCServer) Register(r grpc.ServiceRegistrar) {
	itemv1.RegisterItemServiceServer(r, s)
}

func (s *GRPCServer) CreateItem(ctx context.Context, req *itemv1.CreateItemRequest) (*itemv1.CreateItemResponse, error) {
	item, err := s.svc.Create(ctx, CreateInput{
		Name:        req.GetName(),
		Description: req.GetDescription(),
	})
	if err != nil {
		return nil, err
	}
	return &itemv1.CreateItemResponse{Item: toProto(item)}, nil
}

func (s *GRPCServer) GetItem(ctx context.Context, req *itemv1.GetItemRequest) (*itemv1.GetItemResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	item, err := s.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &itemv1.GetItemResponse{Item: toProto(item)}, nil
}

func (s *GRPCServer) ListItems(ctx context.Context, req *itemv1.ListItemsRequest) (*itemv1.ListItemsResponse, error) {
	items, total, err := s.svc.List(ctx, ListInput{
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, err
	}
	return &itemv1.ListItemsResponse{Items: toProtoList(items), Total: total}, nil
}

func (s *GRPCServer) UpdateItem(ctx context.Context, req *itemv1.UpdateItemRequest) (*itemv1.UpdateItemResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	// The fields are proto3 optional, so unset ones arrive as nil pointers.
	item, err := s.svc.Update(ctx, id, UpdateInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		return nil, err
	}
	return &itemv1.UpdateItemResponse{Item: toProto(item)}, nil
}

func (s *GRPCServer) SearchItems(ctx context.Context, req *itemv1.SearchItemsRequest) (*itemv1.SearchItemsResponse, error) {
	items, total, err := s.svc.Search(ctx, SearchInput{
		Query:  req.GetQuery(),
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, err
	}
	return &itemv1.SearchItemsResponse{Items: toProtoList(items), Total: total}, nil
}

func (s *GRPCServer) DeleteItem(ctx context.Context, req *itemv1.DeleteItemRequest) (*itemv1.DeleteItemResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.svc.Delete(ctx, id); err != nil {
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

func toProto(it Item) *itemv1.Item {
	return &itemv1.Item{
		Id:          it.ID.String(),
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   timestamppb.New(it.CreatedAt),
		UpdatedAt:   timestamppb.New(it.UpdatedAt),
	}
}

func toProtoList(items []Item) []*itemv1.Item {
	out := make([]*itemv1.Item, 0, len(items))
	for _, it := range items {
		out = append(out, toProto(it))
	}
	return out
}
