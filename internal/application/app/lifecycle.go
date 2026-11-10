package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// component is anything long-running: a server or a consumer loop.
type component struct {
	name string
	// start blocks until the component stops. It returns nil after a
	// requested stop and an error if the component fails.
	start func() error
	// stop asks the component to finish in-flight work and return from
	// start. It gives up when ctx is done.
	stop func(context.Context) error
}

// Run starts every component, waits for a shutdown signal (ctx) or the first
// component failure, then shuts down in this order:
//
//  1. report not-ready (HTTP /readyz and gRPC health) and wait DrainDelay,
//     so load balancers stop routing here before connections close
//  2. stop all components at once (graceful HTTP/gRPC shutdown, consumers
//     finish their current batch)
//  3. wait for every start goroutine to return
//  4. close resources in reverse creation order (Kafka, Redis, Postgres),
//     telemetry flush last
//
// All of it shares one budget, shutdown.timeout.
func (a *App) Run(ctx context.Context) error {
	failed := make(chan error, len(a.components))
	var wg sync.WaitGroup

	for _, c := range a.components {
		wg.Go(func() {
			err := c.start()
			if err == nil && !a.stopping.Load() {
				err = errors.New("exited unexpectedly")
			}
			if err != nil {
				failed <- fmt.Errorf("%s: %w", c.name, err)
			}
		})
		a.log.InfoContext(ctx, "component started", slog.String("component", c.name))
	}

	a.setReady(true)
	a.log.InfoContext(ctx, "ready")

	var runErr error
	select {
	case <-ctx.Done():
		a.log.Info("shutdown requested")
	case runErr = <-failed:
		a.log.Error("component failed, shutting down", slog.Any("error", runErr))
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.shutdown.Timeout)
	defer cancel()

	errs := []error{runErr, a.stop(shutdownCtx), waitFor(shutdownCtx, &wg)}
	errs = append(errs, a.cleanup.run(shutdownCtx))

	// Collect failures reported while shutting down.
drain:
	for {
		select {
		case err := <-failed:
			errs = append(errs, err)
		default:
			break drain
		}
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}
	a.log.Info("stopped cleanly")
	return nil
}

func (a *App) stop(ctx context.Context) error {
	a.stopping.Store(true)
	a.setReady(false)

	if a.shutdown.DrainDelay > 0 {
		a.log.Info("draining", slog.Duration("delay", a.shutdown.DrainDelay))
		timer := time.NewTimer(a.shutdown.DrainDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}

	// Components are independent of each other, so they stop concurrently:
	// one slow component does not use up the time the others have to drain.
	errs := make([]error, len(a.components))
	var wg sync.WaitGroup
	for i, c := range a.components {
		wg.Go(func() {
			if err := c.stop(ctx); err != nil {
				errs[i] = fmt.Errorf("stop %s: %w", c.name, err)
				return
			}
			a.log.Debug("component stopped", slog.String("component", c.name))
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (a *App) setReady(ready bool) {
	a.ready.Store(ready)
	for _, hook := range a.onReady {
		hook(ready)
	}
}

func waitFor(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("components still running at shutdown deadline: %w", ctx.Err())
	}
}

// cleanupStack closes resources in reverse creation order. The wire functions
// push onto it as each resource is created, so a failure halfway through
// Build and a normal shutdown both close exactly what exists.
type cleanupStack struct {
	log   *slog.Logger
	steps []cleanupStep
}

type cleanupStep struct {
	name string
	fn   func(context.Context) error
}

func (s *cleanupStack) push(name string, fn func(context.Context) error) {
	s.steps = append(s.steps, cleanupStep{name: name, fn: fn})
}

func (s *cleanupStack) run(ctx context.Context) error {
	var errs []error
	for i := len(s.steps) - 1; i >= 0; i-- {
		step := s.steps[i]
		if err := step.fn(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", step.name, err))
			continue
		}
		s.log.Debug("closed", slog.String("resource", step.name))
	}
	s.steps = nil
	return errors.Join(errs...)
}

// Adapters from common Close signatures to cleanup steps.

func closeWithErr(fn func() error) func(context.Context) error {
	return func(context.Context) error { return fn() }
}

func closeNoErr(fn func()) func(context.Context) error {
	return func(context.Context) error { fn(); return nil }
}
