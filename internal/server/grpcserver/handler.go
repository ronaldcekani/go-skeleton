package grpcserver

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/example/go-skeleton/internal/domain"
	itemv1 "github.com/example/go-skeleton/internal/gen/proto/item/v1"
)

// ItemService is what the gRPC transport needs from the service layer.
type ItemService interface {
	Create(ctx context.Context, name, description string) (domain.Item, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Item, error)
	List(ctx context.Context, limit, offset int) ([]domain.Item, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type itemServer struct {
	itemv1.UnimplementedItemServiceServer
	items ItemService
}

func (s *itemServer) CreateItem(ctx context.Context, req *itemv1.CreateItemRequest) (*itemv1.CreateItemResponse, error) {
	item, err := s.items.Create(ctx, req.GetName(), req.GetDescription())
	if err != nil {
		return nil, toStatus(err)
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
		return nil, toStatus(err)
	}
	return &itemv1.GetItemResponse{Item: toProto(item)}, nil
}

func (s *itemServer) ListItems(ctx context.Context, req *itemv1.ListItemsRequest) (*itemv1.ListItemsResponse, error) {
	items, total, err := s.items.List(ctx, int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, toStatus(err)
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
		return nil, toStatus(err)
	}
	return &itemv1.DeleteItemResponse{}, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, "id must be a UUID")
	}
	return id, nil
}

// toStatus maps service errors to gRPC status codes. Unknown errors are
// reported as Internal without leaking their message to the client.
func toStatus(err error) error {
	if verr, ok := errors.AsType[*domain.ValidationError](err); ok {
		return status.Error(codes.InvalidArgument, verr.Message)
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "item not found")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	}
	return &internalError{cause: err}
}

// internalError carries the cause for the logging interceptor while exposing
// only a generic message on the wire.
type internalError struct {
	cause error
}

func (e *internalError) Error() string { return e.cause.Error() }
func (e *internalError) Unwrap() error { return e.cause }
func (e *internalError) GRPCStatus() *status.Status {
	return status.New(codes.Internal, "internal server error")
}

func (e *internalError) LogValue() slog.Value { return slog.StringValue(e.cause.Error()) }

func toProto(it domain.Item) *itemv1.Item {
	return &itemv1.Item{
		Id:          it.ID.String(),
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   timestamppb.New(it.CreatedAt),
		UpdatedAt:   timestamppb.New(it.UpdatedAt),
	}
}
