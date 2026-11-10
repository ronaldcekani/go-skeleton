// Package domain holds the business entities and errors shared by all layers.
// It depends on nothing else in the application.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// ValidationError describes input that violates a business rule.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

type Item struct {
	ID          uuid.UUID `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// ItemCreatedEvent is published when an item has been created.
type ItemCreatedEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Item       Item      `json:"item"`
}

const EventTypeItemCreated = "item.created"
