// Package logger builds the application's slog.Logger. Every record logged
// with a context that carries an active span gets trace_id and span_id
// attributes, so logs can be correlated with traces.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type Options struct {
	Level   string // debug, info, warn, error
	Format  string // json, text
	Service string
	Version string
	Env     string
}

// New returns a logger writing to w. It does not install it as the default;
// callers decide that.
func New(w io.Writer, opts Options) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(opts.Level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", opts.Level, err)
	}
	handlerOpts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch strings.ToLower(opts.Format) {
	case "json", "":
		h = slog.NewJSONHandler(w, handlerOpts)
	case "text":
		h = slog.NewTextHandler(w, handlerOpts)
	default:
		return nil, fmt.Errorf("log format %q: want json or text", opts.Format)
	}

	l := slog.New(&traceHandler{Handler: h}).With(
		slog.String("service", opts.Service),
		slog.String("version", opts.Version),
		slog.String("env", opts.Env),
	)
	return l, nil
}

// traceHandler decorates records with the IDs of the span found in the context.
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
