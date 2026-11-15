package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/platform/postgres"
)

// PostgresStore persists items in Postgres. It contains no business rules.
// Every query goes through postgres.Conn, so calls made inside
// Transactor.WithinTx join that transaction.
type PostgresStore struct {
	db *sqlx.DB
}

func NewPostgresStore(db *sqlx.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

const itemColumns = `id, name, description, created_at, updated_at`

// itemRow is one row of the items table. It is unexported, like every storage
// and wire type of the feature: the schema can change without anything
// outside this file noticing.
type itemRow struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (r itemRow) toItem() Item {
	return Item{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func toItems(rows []itemRow) []Item {
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.toItem())
	}
	return items
}

func (r *PostgresStore) Create(ctx context.Context, item Item) (Item, error) {
	const q = `
		INSERT INTO items (id, name, description)
		VALUES ($1, $2, $3)
		RETURNING ` + itemColumns

	var out itemRow
	if err := postgres.Conn(ctx, r.db).GetContext(ctx, &out, q, item.ID, item.Name, item.Description); err != nil {
		return Item{}, fmt.Errorf("insert item: %w", err)
	}
	return out.toItem(), nil
}

func (r *PostgresStore) GetByID(ctx context.Context, id uuid.UUID) (Item, error) {
	const q = `SELECT ` + itemColumns + ` FROM items WHERE id = $1`

	var out itemRow
	if err := postgres.Conn(ctx, r.db).GetContext(ctx, &out, q, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, domain.NotFound("item not found")
		}
		return Item{}, fmt.Errorf("select item: %w", err)
	}
	return out.toItem(), nil
}

// List returns one page of items, newest first, and the total row count.
func (r *PostgresStore) List(ctx context.Context, limit, offset int) ([]Item, int64, error) {
	const q = `
		SELECT ` + itemColumns + `
		FROM items
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2`

	rows := make([]itemRow, 0, limit)
	if err := postgres.Conn(ctx, r.db).SelectContext(ctx, &rows, q, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("select items: %w", err)
	}
	var total int64
	if err := postgres.Conn(ctx, r.db).GetContext(ctx, &total, `SELECT count(*) FROM items`); err != nil {
		return nil, 0, fmt.Errorf("count items: %w", err)
	}
	return toItems(rows), total, nil
}

// Update sets the non-nil fields of in and bumps updated_at.
func (r *PostgresStore) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Item, error) {
	const q = `
		UPDATE items
		SET name = COALESCE($2, name),
		    description = COALESCE($3, description),
		    updated_at = now()
		WHERE id = $1
		RETURNING ` + itemColumns

	var out itemRow
	if err := postgres.Conn(ctx, r.db).GetContext(ctx, &out, q, id, in.Name, in.Description); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, domain.NotFound("item not found")
		}
		return Item{}, fmt.Errorf("update item: %w", err)
	}
	return out.toItem(), nil
}

// Search returns one page of the items whose name or description contains
// query (case-insensitive), newest first, and the total number of matches.
// This is a sequential scan; add a pg_trgm index or full-text search when
// the table grows.
func (r *PostgresStore) Search(ctx context.Context, query string, limit, offset int) ([]Item, int64, error) {
	const where = ` FROM items WHERE name ILIKE $1 OR description ILIKE $1`
	const q = `SELECT ` + itemColumns + where + `
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`

	pattern := "%" + likeEscaper.Replace(query) + "%"
	rows := make([]itemRow, 0, limit)
	if err := postgres.Conn(ctx, r.db).SelectContext(ctx, &rows, q, pattern, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("search items: %w", err)
	}
	var total int64
	if err := postgres.Conn(ctx, r.db).GetContext(ctx, &total, `SELECT count(*)`+where, pattern); err != nil {
		return nil, 0, fmt.Errorf("count search results: %w", err)
	}
	return toItems(rows), total, nil
}

// likeEscaper makes LIKE wildcards in user input match literally ("\" is
// the default escape character in Postgres).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *PostgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := postgres.Conn(ctx, r.db).ExecContext(ctx, `DELETE FROM items WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n == 0 {
		return domain.NotFound("item not found")
	}
	return nil
}
