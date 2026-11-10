package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/go-skeleton/internal/domain"
	"github.com/example/go-skeleton/internal/gen/openapi"
)

func TestWriteError(t *testing.T) {
	handle := writeError(slog.New(slog.NewTextHandler(io.Discard, nil)))

	for name, tc := range map[string]struct {
		err     error
		status  int
		code    string
		message string
	}{
		"invalid input":     {domain.InvalidInput("name is required"), http.StatusBadRequest, "invalid_input", "name is required"},
		"not found":         {domain.NotFound("item not found"), http.StatusNotFound, "not_found", "item not found"},
		"wrapped not found": {fmt.Errorf("get: %w", domain.NotFound("item not found")), http.StatusNotFound, "not_found", "item not found"},
		"conflict":          {domain.Conflict("name already taken"), http.StatusConflict, "conflict", "name already taken"},
		"unauthenticated":   {domain.NewError(domain.ErrKindUnauthenticated, "sign in"), http.StatusUnauthorized, "unauthenticated", "sign in"},
		"permission denied": {domain.NewError(domain.ErrKindPermissionDenied, "not yours"), http.StatusForbidden, "permission_denied", "not yours"},
		"unknown error":     {errors.New("pq: password is hunter2"), http.StatusInternalServerError, "internal", "internal server error"},
		"deadline exceeded": {context.DeadlineExceeded, http.StatusGatewayTimeout, "deadline_exceeded", "request timed out"},
		"client went away":  {context.Canceled, statusClientClosedRequest, "canceled", "request canceled"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handle(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			var body openapi.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body.Code != tc.code || body.Message != tc.message {
				t.Errorf("body = %+v, want code %q message %q", body, tc.code, tc.message)
			}
		})
	}
}
