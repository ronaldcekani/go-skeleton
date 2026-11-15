package item_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/feature/item"
	"github.com/example/go-skeleton/internal/platform/postgres"
	"github.com/example/go-skeleton/migrations"
)

// openTestDB connects to a real Postgres and applies the migrations. It skips
// the test unless TEST_POSTGRES_DSN is set. Tests delete the rows they commit.
func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	db, err := postgres.Open(t.Context(), postgres.Options{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The lock makes the test packages, which run in parallel against the
	// same database, migrate one at a time.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		t.Fatalf("migration lock: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db.DB, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		t.Fatalf("migration provider: %v", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPostgresStoreJoinsTransaction(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	store := item.NewPostgresStore(db)
	tx := postgres.NewTransactor(db)
	first, second := uuid.New(), uuid.New()

	// Rolled back: neither item exists afterwards.
	boom := errors.New("boom")
	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := store.Create(ctx, item.Item{ID: first, Name: "first"}); err != nil {
			return err
		}
		if _, err := store.Create(ctx, item.Item{ID: second, Name: "second"}); err != nil {
			return err
		}
		if _, err := store.GetByID(ctx, first); err != nil {
			t.Errorf("item not visible inside its own transaction: %v", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	for _, id := range []uuid.UUID{first, second} {
		if _, err := store.GetByID(ctx, id); !domain.IsKind(err, domain.ErrKindNotFound) {
			t.Errorf("after rollback GetByID(%s) err = %v, want not found", id, err)
		}
	}

	// Committed: both exist.
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := store.Create(ctx, item.Item{ID: first, Name: "first"}); err != nil {
			return err
		}
		_, err := store.Create(ctx, item.Item{ID: second, Name: "second"})
		return err
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}
	for _, id := range []uuid.UUID{first, second} {
		if _, err := store.GetByID(ctx, id); err != nil {
			t.Errorf("after commit GetByID(%s): %v", id, err)
		}
		if err := store.Delete(ctx, id); err != nil {
			t.Errorf("cleanup Delete(%s): %v", id, err)
		}
	}
}

func TestPostgresStoreUpdate(t *testing.T) {
	ctx := t.Context()
	store := item.NewPostgresStore(openTestDB(t))

	created, err := store.Create(ctx, item.Item{ID: uuid.New(), Name: "widget", Description: "a thing"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(context.WithoutCancel(ctx), created.ID) })

	name := "gadget"
	updated, err := store.Update(ctx, created.ID, item.UpdateInput{Name: &name})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "gadget" || updated.Description != "a thing" {
		t.Errorf("got name %q description %q, want only the name changed", updated.Name, updated.Description)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at = %v, want after %v", updated.UpdatedAt, created.UpdatedAt)
	}

	if _, err := store.Update(ctx, uuid.New(), item.UpdateInput{Name: &name}); !domain.IsKind(err, domain.ErrKindNotFound) {
		t.Errorf("Update of missing item: err = %v, want not found", err)
	}
}

func TestPostgresStoreSearch(t *testing.T) {
	ctx := t.Context()
	store := item.NewPostgresStore(openTestDB(t))

	// A per-run marker keeps the search away from rows other tests left.
	marker := uuid.NewString()[:8]
	names := []string{marker + " Blue Widget", marker + " 100% cotton", marker + " sprocket"}
	for _, name := range names {
		created, err := store.Create(ctx, item.Item{ID: uuid.New(), Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Cleanup(func() { _ = store.Delete(context.WithoutCancel(ctx), created.ID) })
	}

	for query, want := range map[string]int64{
		marker:             3,
		marker + " blue w": 1, // case-insensitive
		marker + " 100%":   1, // % is matched literally
		marker + "%":       0, // ...and is not a wildcard
		marker + "_":       0, // nor is _
	} {
		items, total, err := store.Search(ctx, query, 2, 0)
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		if total != want || int64(len(items)) != min(want, 2) {
			t.Errorf("Search(%q) = %d items, total %d; want total %d", query, len(items), total, want)
		}
	}
}
