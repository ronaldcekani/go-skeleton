// Package repository is the data access layer: Postgres for storage and Redis
// for caching. It contains no business rules.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/example/go-skeleton/internal/domain"
)

// ItemRepository persists items in Postgres.
type ItemRepository struct {
	db *sqlx.DB
}

func NewItemRepository(db *sqlx.DB) *ItemRepository {
	return &ItemRepository{db: db}
}

const itemColumns = `id, name, description, created_at, updated_at`

func (r *ItemRepository) Create(ctx context.Context, item domain.Item) (domain.Item, error) {
	const q = `
		INSERT INTO items (id, name, description)
		VALUES ($1, $2, $3)
		RETURNING ` + itemColumns

	var out domain.Item
	if err := r.db.GetContext(ctx, &out, q, item.ID, item.Name, item.Description); err != nil {
		return domain.Item{}, fmt.Errorf("insert item: %w", err)
	}
	return out, nil
}

func (r *ItemRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM items WHERE id = $1`

	var out domain.Item
	if err := r.db.GetContext(ctx, &out, q, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Item{}, domain.ErrNotFound
		}
		return domain.Item{}, fmt.Errorf("select item: %w", err)
	}
	return out, nil
}

// List returns one page of items, newest first, and the total row count.
func (r *ItemRepository) List(ctx context.Context, limit, offset int) ([]domain.Item, int64, error) {
	const q = `
		SELECT ` + itemColumns + `
		FROM items
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2`

	items := make([]domain.Item, 0, limit)
	if err := r.db.SelectContext(ctx, &items, q, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("select items: %w", err)
	}
	var total int64
	if err := r.db.GetContext(ctx, &total, `SELECT count(*) FROM items`); err != nil {
		return nil, 0, fmt.Errorf("count items: %w", err)
	}
	return items, total, nil
}

func (r *ItemRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM items WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
