package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/example/go-skeleton/internal/domain"
	openapi "github.com/example/go-skeleton/internal/gen/openapi"
	"github.com/example/go-skeleton/internal/server/policy"
)

// stubAPI answers the operations the tests call. The embedded interface is
// nil, so any other operation panics, which the server turns into a 500.
type stubAPI struct {
	openapi.StrictServerInterface
}

func (stubAPI) CreateItem(context.Context, openapi.CreateItemRequestObject) (openapi.CreateItemResponseObject, error) {
	return openapi.CreateItem201JSONResponse{}, nil
}

func (stubAPI) UpdateItem(context.Context, openapi.UpdateItemRequestObject) (openapi.UpdateItemResponseObject, error) {
	return openapi.UpdateItem200JSONResponse{}, nil
}

func (stubAPI) SearchItems(context.Context, openapi.SearchItemsRequestObject) (openapi.SearchItemsResponseObject, error) {
	return nil, domain.NotFound("nothing here")
}

// newTestServer serves the full HTTP stack (routing, OpenAPI validation,
// strict handlers) with authentication disabled.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	enforcer, err := policy.NewEnforcer(nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.NewTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{}, stubAPI{}, func(context.Context) error { return nil }, enforcer, table, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// Requests that do not match the OpenAPI spec are rejected before they reach
// a handler.
func TestRequestsAreValidatedAgainstTheSpec(t *testing.T) {
	ts := newTestServer(t)
	url := ts.URL + "/v1/items/" + uuid.NewString()

	if resp := do(t, http.MethodPatch, url, `{"description":"new"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid request: status = %d, want 200", resp.StatusCode)
	}
	for name, body := range map[string]string{
		"empty body":    `{}`,
		"empty name":    `{"name":""}`,
		"unknown field": `{"colour":"red"}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPatch, url, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
	if resp := do(t, http.MethodGet, ts.URL+"/v1/items:search", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing required query parameter: status = %d, want 400", resp.StatusCode)
	}
}

// An error a handler returns is answered according to its domain kind.
func TestHandlerErrorsBecomeResponses(t *testing.T) {
	ts := newTestServer(t)

	resp := do(t, http.MethodGet, ts.URL+"/v1/items:search?q=x", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body openapi.Error
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "not_found" || body.Message != "nothing here" {
		t.Errorf("body = %+v, want the error's kind and public message", body)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	ts := newTestServer(t)
	big := `{"name":"x","description":"` + strings.Repeat("a", DefaultMaxBodyBytes) + `"}`

	t.Run("announced size", func(t *testing.T) {
		resp := do(t, http.MethodPost, ts.URL+"/v1/items", big)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", resp.StatusCode)
		}
	})
	t.Run("chunked", func(t *testing.T) {
		// A reader that is not a *strings.Reader hides the length, so the
		// client sends the body chunked.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/v1/items", io.MultiReader(strings.NewReader(big)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", resp.StatusCode)
		}
	})
	t.Run("under the limit", func(t *testing.T) {
		resp := do(t, http.MethodPost, ts.URL+"/v1/items", `{"name":"x"}`)
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("status = %d, want 201", resp.StatusCode)
		}
	})
}

func TestUnknownRoutesGetJSONErrors(t *testing.T) {
	ts := newTestServer(t)
	for name, tc := range map[string]struct {
		method, path string
		status       int
		code         string
	}{
		"unknown path":   {http.MethodGet, "/nope", http.StatusNotFound, "not_found"},
		"unknown method": {http.MethodPut, "/v1/items", http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, tc.method, ts.URL+tc.path, "")
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body openapi.Error
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Code, tc.code)
			}
			if tc.status == http.StatusMethodNotAllowed && resp.Header.Get("Allow") == "" {
				t.Error("a 405 must say which methods are allowed")
			}
		})
	}
}
