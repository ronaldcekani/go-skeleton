// Package httpserver is the HTTP transport. Routing, request decoding and
// response encoding come from code generated out of api/openapi/openapi.yaml;
// this package adapts the generated interface to the service layer.
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/gen/openapi"
	"github.com/example/go-skeleton/internal/server/policy"
)

type Options struct {
	Host              string
	Port              int
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// ReadinessCheck reports whether the app can serve traffic right now.
type ReadinessCheck func(ctx context.Context) error

// New builds the HTTP server. The caller owns its lifecycle (Serve/Shutdown).
func New(opts Options, items ItemService, ready ReadinessCheck, enforcer *policy.Enforcer, policies policy.Table, log *slog.Logger) (*http.Server, error) {
	spec, err := openapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}
	// The spec's server URLs are documentation; do not validate Host against them.
	spec.Servers = nil

	// Route pattern ("GET /v1/items/{id}") -> operationId, to find the
	// policy of the matched route.
	operations := map[string]string{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			operations[method+" "+path] = op.OperationID
		}
	}
	if err := policies.CheckHTTP(slices.Collect(maps.Values(operations))); err != nil {
		return nil, fmt.Errorf("endpoint rules: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			log.WarnContext(r.Context(), "readiness check failed", slog.Any("error", err))
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	strict := openapi.NewStrictHandlerWithOptions(&handler{items: items}, nil, openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  writeInvalidRequest,
		ResponseErrorHandlerFunc: writeError(log),
	})

	// Rejects requests that do not match the OpenAPI spec before they reach
	// a handler.
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		ErrorHandler: func(w http.ResponseWriter, message string, statusCode int) {
			writeErrorBody(w, statusCode, domain.ErrKindInvalidInput.String(), message)
		},
		// Tokens are verified by the authorize middleware, which runs first.
		Options: openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	})

	// Registers the generated routes on mux. The middlewares run per matched
	// operation, after routing; the last one listed runs first, so a request
	// is authenticated and rate limited before its body is read.
	authz := authorize(enforcer, policies, operations, writeError(log))
	openapi.HandlerWithOptions(strict, openapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []openapi.MiddlewareFunc{validator, authz, routeTag},
		ErrorHandlerFunc: writeInvalidRequest,
	})

	var h http.Handler = mux
	h = recoverer(log)(h)
	h = accessLog(log)(h)
	h = otelhttp.NewHandler(h, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return !isProbe(r) }),
	)

	return &http.Server{
		Addr:              net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		Handler:           h,
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		ReadTimeout:       opts.ReadTimeout,
		WriteTimeout:      opts.WriteTimeout,
		IdleTimeout:       opts.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}, nil
}

func isProbe(r *http.Request) bool {
	return r.URL.Path == "/healthz" || r.URL.Path == "/readyz"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
