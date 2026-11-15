// Package domain holds the business entities, inputs and errors shared by all
// layers. It depends on nothing else in the application.
package domain

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Length limits are in characters (code points), like maxLength in the
// OpenAPI spec, not in bytes.
const (
	MaxItemNameLen        = 200
	MaxItemDescriptionLen = 2000
	DefaultPageSize       = 20
	MaxPageSize           = 100
	MaxSearchQueryLen     = 200
)

// Item carries no db or json tags on purpose: the repository, the cache and
// the event publisher each map it to a format of their own, so renaming a
// field here cannot silently change a table mapping or a wire contract.
type Item struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateItemInput is what a caller provides to create an item.
type CreateItemInput struct {
	Name        string
	Description string
}

// Validate checks the rules that depend only on the input itself. Rules that
// need other state (uniqueness, permissions) belong in the service.
func (in CreateItemInput) Validate() error {
	switch {
	case in.Name == "":
		return InvalidInput("name is required")
	case utf8.RuneCountInString(in.Name) > MaxItemNameLen:
		return InvalidInput("name must be at most %d characters", MaxItemNameLen)
	case utf8.RuneCountInString(in.Description) > MaxItemDescriptionLen:
		return InvalidInput("description must be at most %d characters", MaxItemDescriptionLen)
	}
	return nil
}

// ListItemsInput selects one page of items.
type ListItemsInput struct {
	Limit  int // 0 means DefaultPageSize
	Offset int
}

func (in ListItemsInput) Validate() error {
	switch {
	case in.Limit < 0 || in.Limit > MaxPageSize:
		return InvalidInput("limit must be between 1 and %d", MaxPageSize)
	case in.Offset < 0:
		return InvalidInput("offset must not be negative")
	}
	return nil
}

// UpdateItemInput changes an item. A nil field keeps its current value.
type UpdateItemInput struct {
	Name        *string
	Description *string
}

func (in UpdateItemInput) Validate() error {
	switch {
	case in.Name == nil && in.Description == nil:
		return InvalidInput("at least one of name or description is required")
	case in.Name != nil && *in.Name == "":
		return InvalidInput("name must not be empty")
	case in.Name != nil && utf8.RuneCountInString(*in.Name) > MaxItemNameLen:
		return InvalidInput("name must be at most %d characters", MaxItemNameLen)
	case in.Description != nil && utf8.RuneCountInString(*in.Description) > MaxItemDescriptionLen:
		return InvalidInput("description must be at most %d characters", MaxItemDescriptionLen)
	}
	return nil
}

// SearchItemsInput selects one page of the items whose name or description
// contains Query, case-insensitively.
type SearchItemsInput struct {
	Query  string
	Limit  int // 0 means DefaultPageSize
	Offset int
}

func (in SearchItemsInput) Validate() error {
	switch {
	case in.Query == "":
		return InvalidInput("query is required")
	case utf8.RuneCountInString(in.Query) > MaxSearchQueryLen:
		return InvalidInput("query must be at most %d characters", MaxSearchQueryLen)
	}
	return ListItemsInput{Limit: in.Limit, Offset: in.Offset}.Validate()
}
