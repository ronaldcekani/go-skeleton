package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/gen/openapi"
)

// statusClientClosedRequest is the de facto status (from nginx) for a request
// the client abandoned. The client never sees it; it is for logs and metrics.
const statusClientClosedRequest = 499

// statusFromKind is the single place where domain error kinds become HTTP
// statuses.
func statusFromKind(kind domain.ErrKind) int {
	switch kind {
	case domain.ErrKindInvalidInput:
		return http.StatusBadRequest
	case domain.ErrKindNotFound:
		return http.StatusNotFound
	case domain.ErrKindConflict:
		return http.StatusConflict
	case domain.ErrKindUnauthenticated:
		return http.StatusUnauthorized
	case domain.ErrKindPermissionDenied:
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

// writeError returns the handler for every error a generated handler returns.
// It translates the error's domain kind into the HTTP response, so handlers
// only return errors and never pick status codes themselves. Internal errors
// are logged with their cause and answered with a generic message.
func writeError(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		switch {
		case errors.Is(err, context.Canceled):
			writeErrorBody(w, statusClientClosedRequest, "canceled", "request canceled")
			return
		case errors.Is(err, context.DeadlineExceeded):
			writeErrorBody(w, http.StatusGatewayTimeout, "deadline_exceeded", "request timed out")
			return
		}

		kind := domain.KindOf(err)
		if kind == domain.ErrKindInternal {
			log.ErrorContext(r.Context(), "request failed", slog.Any("error", err))
		}
		writeErrorBody(w, statusFromKind(kind), kind.String(), domain.PublicMessage(err))
	}
}

// writeInvalidRequest answers requests the generated code could not decode
// (malformed JSON, a path parameter of the wrong type).
func writeInvalidRequest(w http.ResponseWriter, _ *http.Request, err error) {
	writeErrorBody(w, http.StatusBadRequest, domain.ErrKindInvalidInput.String(), err.Error())
}

func writeErrorBody(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, openapi.Error{Code: code, Message: message})
}
