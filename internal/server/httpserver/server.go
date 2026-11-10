// Package httpserver is the HTTP transport. Routing, request decoding and
// response encoding come from code generated out of api/openapi/openapi.yaml;
// this package adapts the generated interface to the service layer.
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/example/go-skeleton/internal/gen/openapi"
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
func New(opts Options, items ItemService, ready ReadinessCheck, log *slog.Logger) (*http.Server, error) {
	spec, err := openapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}
	// The spec's server URLs are documentation; do not validate Host against them.
	spec.Servers = nil

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
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
		// Errors returned by handlers that were not mapped to a response in
		// the spec end up here.
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			log.ErrorContext(r.Context(), "request failed", slog.Any("error", err))
			writeError(w, http.StatusInternalServerError, "internal", "internal server error")
		},
	})

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		ErrorHandler: func(w http.ResponseWriter, message string, statusCode int) {
			writeError(w, statusCode, "bad_request", message)
		},
	})

	// Registers the generated routes on mux. The middlewares run per matched
	// operation, after routing; the last one listed runs first.
	openapi.HandlerWithOptions(strict, openapi.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []openapi.MiddlewareFunc{validator, routeTag},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
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

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, openapi.Error{Code: code, Message: message})
}
