package repository_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/platform/postgres"
	"github.com/example/go-skeleton/internal/repository"
	"github.com/example/go-skeleton/migrations"
)

// Needs a real Postgres: skipped unless TEST_POSTGRES_DSN is set. The
// migrations are applied to that database; rows the test commits are deleted
// again.
func TestItemRepositoryJoinsTransaction(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	ctx := t.Context()
	db, err := postgres.Open(ctx, postgres.Options{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	provider, err := goose.NewProvider(goose.DialectPostgres, db.DB, migrations.FS)
	if err != nil {
		t.Fatalf("migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := repository.NewItemRepository(db)
	tx := postgres.NewTransactor(db)
	first, second := uuid.New(), uuid.New()

	// Rolled back: neither item exists afterwards.
	boom := errors.New("boom")
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := repo.Create(ctx, domain.Item{ID: first, Name: "first"}); err != nil {
			return err
		}
		if _, err := repo.Create(ctx, domain.Item{ID: second, Name: "second"}); err != nil {
			return err
		}
		if _, err := repo.GetByID(ctx, first); err != nil {
			t.Errorf("item not visible inside its own transaction: %v", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	for _, id := range []uuid.UUID{first, second} {
		if _, err := repo.GetByID(ctx, id); !domain.IsKind(err, domain.ErrKindNotFound) {
			t.Errorf("after rollback GetByID(%s) err = %v, want not found", id, err)
		}
	}

	// Committed: both exist.
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := repo.Create(ctx, domain.Item{ID: first, Name: "first"}); err != nil {
			return err
		}
		_, err := repo.Create(ctx, domain.Item{ID: second, Name: "second"})
		return err
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}
	for _, id := range []uuid.UUID{first, second} {
		if _, err := repo.GetByID(ctx, id); err != nil {
			t.Errorf("after commit GetByID(%s): %v", id, err)
		}
		if err := repo.Delete(ctx, id); err != nil {
			t.Errorf("cleanup Delete(%s): %v", id, err)
		}
	}
}
