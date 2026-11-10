// Command app is the single binary of the service. The --mode flag selects
// what it does: "app" serves HTTP and gRPC, "migration" manages the database
// schema.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/go-skeleton/internal/application/app"
	"github.com/example/go-skeleton/internal/application/migration"
	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/platform/logger"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}

	log, err := logger.New(os.Stdout, logger.Options{
		Level:   cfg.Log.Level,
		Format:  cfg.Log.Format,
		Service: cfg.Service.Name,
		Version: cfg.Service.Version,
		Env:     cfg.Service.Environment,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "logger:", err)
		return 2
	}
	slog.SetDefault(log)

	// The first SIGINT/SIGTERM cancels ctx and starts a graceful shutdown.
	// Signal handling is then reset, so a second signal kills the process
	// immediately if shutdown hangs.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	// Each operation mode is a package under internal/application.
	modes := map[string]func(context.Context, config.Config, *slog.Logger) error{
		config.ModeApp:       app.Run,
		config.ModeMigration: migration.Run,
	}
	if err := modes[cfg.Mode](ctx, cfg, log); err != nil {
		log.Error("exited with error", slog.String("mode", cfg.Mode), slog.Any("error", err))
		return 1
	}
	return 0
}
