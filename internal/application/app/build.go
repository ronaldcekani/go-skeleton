// Package app is the "app" operation mode: it serves HTTP and gRPC and runs
// the Kafka consumers until it is asked to stop.
//
// Build wires everything and Run drives the lifecycle. The wire_*.go files
// each construct one layer, bottom-up: platform -> services -> servers and
// consumers.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/example/go-skeleton/internal/config"
)

type App struct {
	log        *slog.Logger
	shutdown   config.Shutdown
	components []component
	cleanup    *cleanupStack

	// ready is what the readiness probes report, together with dependency
	// checks. onReady hooks are told when it changes.
	ready    atomic.Bool
	onReady  []func(ready bool)
	stopping atomic.Bool
}

// Run builds the app and runs it until ctx is cancelled or a component fails.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	a, err := Build(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}
	return a.Run(ctx)
}

// Build connects to every dependency and constructs every component, without
// starting any of them. If it fails halfway, whatever was already created is
// closed again.
func Build(ctx context.Context, cfg config.Config, log *slog.Logger) (_ *App, err error) {
	cleanup := &cleanupStack{log: log}
	defer func() {
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Shutdown.Timeout)
			defer cancel()
			err = errors.Join(err, cleanup.run(closeCtx))
		}
	}()

	a := &App{log: log, shutdown: cfg.Shutdown, cleanup: cleanup}

	platform, err := wirePlatform(ctx, cfg, log, cleanup)
	if err != nil {
		return nil, fmt.Errorf("platform: %w", err)
	}

	services, err := wireServices(cfg, platform, log)
	if err != nil {
		return nil, fmt.Errorf("services: %w", err)
	}

	servers, err := wireServers(ctx, cfg, platform, services, a.ready.Load, log, cleanup)
	if err != nil {
		return nil, fmt.Errorf("servers: %w", err)
	}

	consumers, err := wireConsumers(ctx, cfg, log, cleanup)
	if err != nil {
		return nil, fmt.Errorf("consumers: %w", err)
	}

	a.components = append(servers.components, consumers.components...)
	a.onReady = servers.onReady
	return a, nil
}
