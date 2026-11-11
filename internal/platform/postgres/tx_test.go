package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/example/go-skeleton/internal/platform/postgres"
)

// These tests need a real Postgres. They are skipped unless TEST_POSTGRES_DSN
// is set, e.g. to the DSN of the docker-compose database.
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

	if _, err := db.Exec(`DROP TABLE IF EXISTS tx_test; CREATE TABLE tx_test (name TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS tx_test`) })
	return db
}

// insert stands in for a repository method.
func insert(ctx context.Context, db *sqlx.DB, name string) error {
	_, err := postgres.Conn(ctx, db).ExecContext(ctx, `INSERT INTO tx_test (name) VALUES ($1)`, name)
	return err
}

func count(t *testing.T, db *sqlx.DB) int {
	t.Helper()
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM tx_test`); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestWithinTxCommits(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)

	err := tx.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := insert(ctx, db, "a"); err != nil {
			return err
		}
		if n := count(t, db); n != 0 {
			t.Errorf("rows visible outside the transaction before commit = %d, want 0", n)
		}
		return insert(ctx, db, "b")
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}
	if n := count(t, db); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestWithinTxRollsBackOnError(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)
	boom := errors.New("boom")

	err := tx.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := insert(ctx, db, "a"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := count(t, db); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestWithinTxRollsBackOnFailedQuery(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)

	err := tx.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := insert(ctx, db, "a"); err != nil {
			return err
		}
		return insert(ctx, db, "a") // primary key violation
	})
	if err == nil {
		t.Fatal("err = nil, want a unique violation")
	}
	if n := count(t, db); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = tx.WithinTx(t.Context(), func(ctx context.Context) error {
			if err := insert(ctx, db, "a"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if n := count(t, db); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
	if open := db.Stats().InUse; open != 0 {
		t.Errorf("connections in use = %d, want 0", open)
	}
}

func TestWithinTxRollsBackOnCancel(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)
	ctx, cancel := context.WithCancel(t.Context())

	err := tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, db, "a"); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if err == nil {
		t.Fatal("err = nil, want a commit failure")
	}
	if n := count(t, db); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestWithinTxNestedJoinsOuter(t *testing.T) {
	db := openTestDB(t)
	tx := postgres.NewTransactor(db)
	boom := errors.New("boom")

	// The inner call succeeds, the outer one fails afterwards: both undone.
	err := tx.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := tx.WithinTx(ctx, func(ctx context.Context) error {
			return insert(ctx, db, "inner")
		}); err != nil {
			return err
		}
		if err := insert(ctx, db, "outer"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := count(t, db); n != 0 {
		t.Errorf("rows after rollback = %d, want 0", n)
	}

	err = tx.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := insert(ctx, db, "outer"); err != nil {
			return err
		}
		return tx.WithinTx(ctx, func(ctx context.Context) error {
			return insert(ctx, db, "inner")
		})
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}
	if n := count(t, db); n != 2 {
		t.Errorf("rows after commit = %d, want 2", n)
	}
}
