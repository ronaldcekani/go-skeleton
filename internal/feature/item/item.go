// Package item is the item feature, top to bottom: the entity and its rules
// (item.go), the use cases (service.go), storage (store.go, cache.go), events
// (events.go) and the two transports (http.go, grpc.go).
//
// Nothing outside this package depends on how items are stored or sent over
// the wire, and this package depends on no other feature. What a feature
// needs from another one it declares as an interface of its own, the way
// Service declares Store, and the application wiring connects the two.
package item

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
)

// Length limits are in characters (code points), like maxLength in the
// OpenAPI spec, not in bytes.
const (
	MaxNameLen        = 200
	MaxDescriptionLen = 2000
	DefaultPageSize   = 20
	MaxPageSize       = 100
	MaxSearchQueryLen = 200
)

// Item carries no db or json tags on purpose: the store, the cache, the event
// publisher and the transports each map it to a format of their own, so
// renaming a field here cannot silently change a table mapping or a wire
// contract.
type Item struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateInput is what a caller provides to create an item.
type CreateInput struct {
	Name        string
	Description string
}

// Validate checks the rules that depend only on the input itself. Rules that
// need other state (uniqueness, permissions) belong in the service.
func (in CreateInput) Validate() error {
	switch {
	case in.Name == "":
		return domain.InvalidInput("name is required")
	case utf8.RuneCountInString(in.Name) > MaxNameLen:
		return domain.InvalidInput("name must be at most %d characters", MaxNameLen)
	case utf8.RuneCountInString(in.Description) > MaxDescriptionLen:
		return domain.InvalidInput("description must be at most %d characters", MaxDescriptionLen)
	}
	return nil
}

// ListInput selects one page of items.
type ListInput struct {
	Limit  int // 0 means DefaultPageSize
	Offset int
}

func (in ListInput) Validate() error {
	switch {
	case in.Limit < 0 || in.Limit > MaxPageSize:
		return domain.InvalidInput("limit must be between 1 and %d", MaxPageSize)
	case in.Offset < 0:
		return domain.InvalidInput("offset must not be negative")
	}
	return nil
}

// UpdateInput changes an item. A nil field keeps its current value.
type UpdateInput struct {
	Name        *string
	Description *string
}

func (in UpdateInput) Validate() error {
	switch {
	case in.Name == nil && in.Description == nil:
		return domain.InvalidInput("at least one of name or description is required")
	case in.Name != nil && *in.Name == "":
		return domain.InvalidInput("name must not be empty")
	case in.Name != nil && utf8.RuneCountInString(*in.Name) > MaxNameLen:
		return domain.InvalidInput("name must be at most %d characters", MaxNameLen)
	case in.Description != nil && utf8.RuneCountInString(*in.Description) > MaxDescriptionLen:
		return domain.InvalidInput("description must be at most %d characters", MaxDescriptionLen)
	}
	return nil
}

// SearchInput selects one page of the items whose name or description
// contains Query, case-insensitively.
type SearchInput struct {
	Query  string
	Limit  int // 0 means DefaultPageSize
	Offset int
}

func (in SearchInput) Validate() error {
	switch {
	case in.Query == "":
		return domain.InvalidInput("query is required")
	case utf8.RuneCountInString(in.Query) > MaxSearchQueryLen:
		return domain.InvalidInput("query must be at most %d characters", MaxSearchQueryLen)
	}
	return ListInput{Limit: in.Limit, Offset: in.Offset}.Validate()
}
