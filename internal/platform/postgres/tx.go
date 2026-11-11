package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Querier is the query surface shared by *sqlx.DB and *sqlx.Tx. Repositories
// run their SQL through it so the same method works inside and outside a
// transaction.
type Querier interface {
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error)
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
	QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row
}

type txKey struct{}

// Conn returns the transaction carried by ctx, or db if there is none.
// Repositories call it for every query instead of using their pool directly:
//
//	postgres.Conn(ctx, r.db).GetContext(ctx, &out, q, id)
func Conn(ctx context.Context, db *sqlx.DB) Querier {
	if tx, ok := ctx.Value(txKey{}).(*sqlx.Tx); ok {
		return tx
	}
	return db
}

// Transactor runs functions inside a database transaction.
type Transactor struct {
	db *sqlx.DB
}

func NewTransactor(db *sqlx.DB) *Transactor {
	return &Transactor{db: db}
}

// WithinTx runs fn in a transaction with the default isolation level. See
// WithinTxOptions.
func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.WithinTxOptions(ctx, nil, fn)
}

// WithinTxOptions runs fn in a transaction. The context passed to fn carries
// the transaction, and every repository call made with it takes part. The
// transaction is committed if fn returns nil, and rolled back if fn returns
// an error or panics, or if ctx is cancelled.
//
// Calls nest: when ctx already carries a transaction, fn joins it, opts are
// ignored, and the outermost call commits or rolls back. An error from a
// joined fn must therefore be returned all the way up to undo its work.
//
// Only Postgres is transactional. Do cache writes and event publishing after
// WithinTx returns nil, not inside fn. The transaction also pins one
// connection, so do not use its context from several goroutines at once.
func (t *Transactor) WithinTxOptions(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(*sqlx.Tx); ok {
		return fn(ctx)
	}

	tx, err := t.db.BeginTxx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	committed := false
	// Deferred so that a panic in fn rolls back too; the panic then continues.
	defer func() {
		if committed {
			return
		}
		// ErrTxDone: the context was cancelled and database/sql already
		// rolled back.
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback transaction: %w", rbErr))
		}
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}
