// Package telemetry configures OpenTelemetry tracing, metrics and logs and
// exports each signal over OTLP, via gRPC or HTTP. A disabled signal keeps
// the global no-op provider, so instrumented code keeps working at near-zero
// cost.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Protocols an exporter can use.
const (
	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http"
)

// instanaKeyHeader authenticates OTLP exports sent straight to the Instana
// backend. gRPC metadata keys must be lower case.
const instanaKeyHeader = "x-instana-key"

type Options struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	// Domain is the business domain the service belongs to, e.g. "sales".
	// It is exported as the service.namespace resource attribute; empty
	// leaves the attribute out.
	Domain string

	Tracing Signal
	// TraceSampling is the parent-based ratio of traces to sample, 0..1.
	TraceSampling float64

	Metrics        Signal
	MetricInterval time.Duration

	// Logging exports the records written through the global
	// LoggerProvider, which the logger package bridges slog to.
	Logging Signal

	// InstanaKey, when set, is sent as the x-instana-key header on every
	// export.
	InstanaKey string
}

// Signal configures the export of one telemetry signal.
type Signal struct {
	Enabled bool
	// EndpointURL is the collector URL. Its scheme selects TLS: "http" sends
	// in plain text, "https" over TLS.
	EndpointURL string
	Protocol    string // ProtocolGRPC or ProtocolHTTP, case-insensitive
}

// ShutdownFunc flushes buffered telemetry and releases exporter resources.
type ShutdownFunc func(ctx context.Context) error

// Setup installs the global propagator and, for each enabled signal, the
// global provider. The returned function must be called on exit to flush
// pending data.
func Setup(ctx context.Context, opts Options, log *slog.Logger) (_ ShutdownFunc, err error) {
	// Propagation is set even when exporting is disabled so trace context
	// still flows through this service to its downstreams.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var shutdowns []func(context.Context) error
	shutdown := func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdowns {
			errs = append(errs, fn(ctx))
		}
		return errors.Join(errs...)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, shutdown(ctx))
		}
	}()

	if !opts.Tracing.Enabled && !opts.Metrics.Enabled && !opts.Logging.Enabled {
		log.InfoContext(ctx, "telemetry disabled")
		return shutdown, nil
	}

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("opentelemetry error", slog.Any("error", err))
	}))

	res, err := newResource(opts)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	var headers map[string]string
	if opts.InstanaKey != "" {
		headers = map[string]string{instanaKeyHeader: opts.InstanaKey}
	}

	// The exporters connect lazily, so an unreachable collector does not
	// block or fail startup; export errors go to the error handler above.
	if opts.Tracing.Enabled {
		exporter, err := newTraceExporter(ctx, opts.Tracing, headers)
		if err != nil {
			return nil, fmt.Errorf("create trace exporter: %w", err)
		}
		provider := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.TraceSampling))),
		)
		otel.SetTracerProvider(provider)
		shutdowns = append(shutdowns, provider.Shutdown)
	}

	if opts.Metrics.Enabled {
		exporter, err := newMetricExporter(ctx, opts.Metrics, headers)
		if err != nil {
			return nil, fmt.Errorf("create metric exporter: %w", err)
		}
		provider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
				sdkmetric.WithInterval(opts.MetricInterval))),
		)
		otel.SetMeterProvider(provider)
		shutdowns = append(shutdowns, provider.Shutdown)

		// Go runtime metrics: goroutines, GC, memory.
		if err := runtime.Start(runtime.WithMeterProvider(provider)); err != nil {
			log.WarnContext(ctx, "runtime metrics not started", slog.Any("error", err))
		}
	}

	if opts.Logging.Enabled {
		exporter, err := newLogExporter(ctx, opts.Logging, headers)
		if err != nil {
			return nil, fmt.Errorf("create log exporter: %w", err)
		}
		provider := sdklog.NewLoggerProvider(
			sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		)
		global.SetLoggerProvider(provider)
		shutdowns = append(shutdowns, provider.Shutdown)
	}

	log.InfoContext(ctx, "telemetry enabled",
		signalAttr("tracing", opts.Tracing),
		signalAttr("metrics", opts.Metrics),
		signalAttr("logging", opts.Logging),
		slog.Float64("trace_sampling", opts.TraceSampling),
		slog.Bool("instana_key", opts.InstanaKey != ""))

	return shutdown, nil
}

// newResource describes this service. Every exported span, metric and log
// record carries these attributes.
func newResource(opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceVersion(opts.ServiceVersion),
		semconv.DeploymentEnvironment(opts.Environment),
	}
	if opts.Domain != "" {
		// The standard attribute for grouping services, which backends
		// already know how to filter and group by.
		attrs = append(attrs, semconv.ServiceNamespace(opts.Domain))
	}
	return resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
}

func newTraceExporter(ctx context.Context, s Signal, headers map[string]string) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(s.Protocol) {
	case ProtocolGRPC:
		return otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(s.EndpointURL),
			otlptracegrpc.WithHeaders(headers))
	case ProtocolHTTP:
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(withSignalPath(s.EndpointURL, "/v1/traces")),
			otlptracehttp.WithHeaders(headers))
	default:
		return nil, unknownProtocol(s.Protocol)
	}
}

func newMetricExporter(ctx context.Context, s Signal, headers map[string]string) (sdkmetric.Exporter, error) {
	switch strings.ToLower(s.Protocol) {
	case ProtocolGRPC:
		return otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpointURL(s.EndpointURL),
			otlpmetricgrpc.WithHeaders(headers))
	case ProtocolHTTP:
		return otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(withSignalPath(s.EndpointURL, "/v1/metrics")),
			otlpmetrichttp.WithHeaders(headers))
	default:
		return nil, unknownProtocol(s.Protocol)
	}
}

func newLogExporter(ctx context.Context, s Signal, headers map[string]string) (sdklog.Exporter, error) {
	switch strings.ToLower(s.Protocol) {
	case ProtocolGRPC:
		return otlploggrpc.New(ctx,
			otlploggrpc.WithEndpointURL(s.EndpointURL),
			otlploggrpc.WithHeaders(headers))
	case ProtocolHTTP:
		return otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(withSignalPath(s.EndpointURL, "/v1/logs")),
			otlploghttp.WithHeaders(headers))
	default:
		return nil, unknownProtocol(s.Protocol)
	}
}

// withSignalPath appends the standard OTLP/HTTP signal path to a URL that
// has none. The HTTP exporters post to a path-less URL's root, which
// collectors and the Instana backend reject.
func withSignalPath(rawURL, path string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.Trim(u.Path, "/") != "" {
		// Leave it to the exporter to report a bad URL.
		return rawURL
	}
	u.Path = path
	return u.String()
}

func unknownProtocol(protocol string) error {
	return fmt.Errorf("protocol %q: want %s or %s", protocol, ProtocolGRPC, ProtocolHTTP)
}

func signalAttr(name string, s Signal) slog.Attr {
	if !s.Enabled {
		return slog.Bool(name, false)
	}
	return slog.Group(name,
		slog.String("endpoint_url", s.EndpointURL),
		slog.String("protocol", strings.ToLower(s.Protocol)))
}
