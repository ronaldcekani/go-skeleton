// Package telemetry configures OpenTelemetry tracing and metrics and exports
// them over OTLP/gRPC. When telemetry is disabled the global no-op providers
// stay in place, so instrumented code keeps working at near-zero cost.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Options struct {
	Enabled        bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	OTLPEndpoint   string
	OTLPInsecure   bool
	TraceSampling  float64
	MetricInterval time.Duration
}

// ShutdownFunc flushes buffered telemetry and releases exporter resources.
type ShutdownFunc func(ctx context.Context) error

// Setup installs the global tracer provider, meter provider and propagator.
// The returned function must be called on exit to flush pending data.
func Setup(ctx context.Context, opts Options, log *slog.Logger) (ShutdownFunc, error) {
	// Propagation is set even when exporting is disabled so trace context
	// still flows through this service to its downstreams.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !opts.Enabled {
		log.InfoContext(ctx, "telemetry disabled")
		return func(context.Context) error { return nil }, nil
	}

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("opentelemetry error", slog.Any("error", err))
	}))

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceVersion(opts.ServiceVersion),
		semconv.DeploymentEnvironment(opts.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	traceOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(opts.OTLPEndpoint)}
	metricOpts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(opts.OTLPEndpoint)}
	if opts.OTLPInsecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	}

	// The exporters connect lazily, so an unreachable collector does not
	// block or fail startup; export errors go to the error handler above.
	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.TraceSampling))),
	)
	otel.SetTracerProvider(tracerProvider)

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		_ = tracerProvider.Shutdown(ctx)
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			sdkmetric.WithInterval(opts.MetricInterval))),
	)
	otel.SetMeterProvider(meterProvider)

	// Go runtime metrics: goroutines, GC, memory.
	if err := runtime.Start(runtime.WithMeterProvider(meterProvider)); err != nil {
		log.WarnContext(ctx, "runtime metrics not started", slog.Any("error", err))
	}

	log.InfoContext(ctx, "telemetry enabled",
		slog.String("otlp_endpoint", opts.OTLPEndpoint),
		slog.Float64("trace_sampling", opts.TraceSampling))

	return func(ctx context.Context) error {
		return errors.Join(
			tracerProvider.Shutdown(ctx),
			meterProvider.Shutdown(ctx),
		)
	}, nil
}
