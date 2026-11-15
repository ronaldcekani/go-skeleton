// Package logger builds the application's slog.Logger. Every record logged
// with a context that carries an active span gets trace_id and span_id
// attributes, so logs can be correlated with traces. Records can also be
// exported over OpenTelemetry, where they carry the span context natively.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"
)

type Options struct {
	Level   string // debug, info, warn, error
	Format  string // json, text
	Service string
	Version string
	Env     string
	Domain  string // business domain of the service; left out of the output when empty
	// Export also sends every record to the global OpenTelemetry
	// LoggerProvider. Until telemetry installs one, records sent there are
	// dropped.
	Export bool
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

	// The service attributes are only added to the local output: exported
	// records carry them as OpenTelemetry resource attributes instead.
	attrs := []slog.Attr{
		slog.String("service", opts.Service),
		slog.String("version", opts.Version),
		slog.String("env", opts.Env),
	}
	if opts.Domain != "" {
		attrs = append(attrs, slog.String("domain", opts.Domain))
	}
	var out slog.Handler = &traceHandler{Handler: h.WithAttrs(attrs)}
	if opts.Export {
		// The bridge does not filter by level itself.
		exported := &levelHandler{Handler: otelslog.NewHandler(opts.Service), level: level}
		out = slog.NewMultiHandler(out, exported)
	}
	return slog.New(out), nil
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

// levelHandler drops records below level.
type levelHandler struct {
	slog.Handler
	level slog.Level
}

func (h *levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level && h.Handler.Enabled(ctx, l)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}
