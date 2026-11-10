package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/go-skeleton/internal/config"
)

// recorder collects lifecycle events in order.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recorder) index(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Index(r.events, event)
}

// blockingComponent runs until stopped.
func blockingComponent(name string, rec *recorder) component {
	done := make(chan struct{})
	return component{
		name:  name,
		start: func() error { <-done; rec.add(name + " returned"); return nil },
		stop:  func(context.Context) error { rec.add(name + " stop"); close(done); return nil },
	}
}

func newTestApp(rec *recorder, shutdown config.Shutdown, components ...component) *App {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &App{log: log, shutdown: shutdown, components: components, cleanup: &cleanupStack{log: log}}
	a.onReady = []func(bool){func(ready bool) {
		if ready {
			rec.add("ready")
		} else {
			rec.add("not ready")
		}
	}}
	a.cleanup.push("first resource", func(context.Context) error { rec.add("close first resource"); return nil })
	a.cleanup.push("second resource", func(context.Context) error { rec.add("close second resource"); return nil })
	return a
}

func TestRunShutsDownInOrder(t *testing.T) {
	rec := &recorder{}
	a := newTestApp(rec, config.Shutdown{Timeout: 5 * time.Second, DrainDelay: 20 * time.Millisecond},
		blockingComponent("http", rec), blockingComponent("consumer", rec))

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- a.Run(ctx) }()

	for rec.index("ready") < 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run = %v, want a clean stop", err)
	}

	order := []string{"ready", "not ready", "http stop", "http returned", "close second resource", "close first resource"}
	last := -1
	for _, event := range order {
		i := rec.index(event)
		if i <= last {
			t.Fatalf("event %q out of order in %v", event, rec.events)
		}
		last = i
	}
	if i := rec.index("consumer returned"); i < 0 || i > rec.index("close second resource") {
		t.Errorf("resources were closed before every component returned: %v", rec.events)
	}
}

func TestRunStopsEverythingWhenAComponentFails(t *testing.T) {
	rec := &recorder{}
	boom := errors.New("listener died")
	failing := component{
		name:  "grpc",
		start: func() error { return boom },
		stop:  func(context.Context) error { return nil },
	}
	a := newTestApp(rec, config.Shutdown{Timeout: 5 * time.Second}, blockingComponent("http", rec), failing)

	err := a.Run(t.Context())
	if !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want the component's error", err)
	}
	if rec.index("http stop") < 0 || rec.index("close first resource") < 0 {
		t.Errorf("the rest of the app was not shut down: %v", rec.events)
	}
}

func TestRunReportsAComponentThatExitsOnItsOwn(t *testing.T) {
	rec := &recorder{}
	quitter := component{
		name:  "consumer",
		start: func() error { return nil },
		stop:  func(context.Context) error { return nil },
	}
	a := newTestApp(rec, config.Shutdown{Timeout: 5 * time.Second}, quitter)

	err := a.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "exited unexpectedly") {
		t.Fatalf("Run = %v, want an unexpected-exit error", err)
	}
}

func TestRunGivesUpAtTheShutdownDeadline(t *testing.T) {
	rec := &recorder{}
	stuck := component{
		name:  "stuck",
		start: func() error { select {} },
		stop:  func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	}
	a := newTestApp(rec, config.Shutdown{Timeout: 50 * time.Millisecond}, stuck)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	err := a.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("shutdown took %v, want it bounded by shutdown.timeout", elapsed)
	}
	if rec.index("close first resource") < 0 {
		t.Errorf("resources must still be closed after the deadline: %v", rec.events)
	}
}

func TestCleanupStackRunsEveryStepInReverse(t *testing.T) {
	rec := &recorder{}
	stack := &cleanupStack{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	boom := errors.New("close failed")
	stack.push("a", func(context.Context) error { rec.add("a"); return nil })
	stack.push("b", func(context.Context) error { rec.add("b"); return boom })
	stack.push("c", func(context.Context) error { rec.add("c"); return nil })

	if err := stack.run(t.Context()); !errors.Is(err, boom) {
		t.Errorf("run = %v, want the failing step's error", err)
	}
	if got := strings.Join(rec.events, ""); got != "cba" {
		t.Errorf("order = %q, want cba (a failing step must not stop the rest)", got)
	}
	if err := stack.run(t.Context()); err != nil || len(rec.events) != 3 {
		t.Error("a second run must do nothing")
	}
}
