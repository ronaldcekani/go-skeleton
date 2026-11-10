package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/migrations"
)

// runMigration applies, rolls back or reports the embedded SQL migrations and
// then exits. goose takes a Postgres advisory lock, so several instances can
// start at once (e.g. as init containers) and only one will migrate.
func runMigration(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	db, err := openPostgres(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	locker, err := newSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db.DB, migrations.FS,
		goose.WithSessionLocker(locker),
		goose.WithSlog(log),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	switch cfg.Migration.Command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		for _, r := range results {
			log.InfoContext(ctx, "migration applied",
				slog.String("file", r.Source.Path), slog.Duration("duration", r.Duration))
		}
		if len(results) == 0 {
			log.InfoContext(ctx, "database schema is up to date")
		}
	case "down":
		result, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		log.InfoContext(ctx, "migration rolled back", slog.String("file", result.Source.Path))
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migration status: %w", err)
		}
		for _, s := range statuses {
			attrs := []any{slog.String("file", s.Source.Path), slog.String("state", string(s.State))}
			if !s.AppliedAt.IsZero() {
				attrs = append(attrs, slog.Time("applied_at", s.AppliedAt))
			}
			log.InfoContext(ctx, "migration", attrs...)
		}
	default:
		return fmt.Errorf("unknown migration command %q", cfg.Migration.Command)
	}
	return nil
}

func newSessionLocker() (lock.SessionLocker, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	return locker, nil
}
