// Package postgres opens an instrumented sqlx connection pool.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/jmoiron/sqlx"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Options struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Open connects to Postgres and verifies the connection. Every query made
// through the returned pool produces a span, and pool statistics are exported
// as metrics.
func Open(ctx context.Context, opts Options) (*sqlx.DB, error) {
	attrs := otelsql.WithAttributes(semconv.DBSystemPostgreSQL)
	sqlDB, err := otelsql.Open("pgx", opts.DSN, attrs,
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			// Keep traces readable: no span per rows.Next / driver ping.
			OmitRows:             true,
			OmitConnResetSession: true,
			OmitConnectorConnect: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(opts.ConnMaxIdleTime)

	if _, err := otelsql.RegisterDBStatsMetrics(sqlDB, attrs); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("register postgres pool metrics: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return sqlx.NewDb(sqlDB, "pgx"), nil
}
