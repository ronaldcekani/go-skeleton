// Package domain holds the business entities, inputs and errors shared by all
// layers. It depends on nothing else in the application.
package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	MaxItemNameLen        = 200
	MaxItemDescriptionLen = 2000
	DefaultPageSize       = 20
	MaxPageSize           = 100
)

type Item struct {
	ID          uuid.UUID `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
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
	case len(in.Name) > MaxItemNameLen:
		return InvalidInput("name must be at most %d characters", MaxItemNameLen)
	case len(in.Description) > MaxItemDescriptionLen:
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

// ItemCreatedEvent is published when an item has been created.
type ItemCreatedEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Item       Item      `json:"item"`
}

const EventTypeItemCreated = "item.created"
