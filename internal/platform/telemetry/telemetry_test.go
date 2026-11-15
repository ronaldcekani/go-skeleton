package telemetry

import (
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestWithSignalPath(t *testing.T) {
	tests := []struct {
		url, want string
	}{
		{url: "http://localhost:4318", want: "http://localhost:4318/v1/traces"},
		{url: "https://otlp.example.com:4318/", want: "https://otlp.example.com:4318/v1/traces"},
		{url: "https://otlp.example.com/custom/traces", want: "https://otlp.example.com/custom/traces"},
		{url: "://bad", want: "://bad"},
	}
	for _, tt := range tests {
		if got := withSignalPath(tt.url, "/v1/traces"); got != tt.want {
			t.Errorf("withSignalPath(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestResourceCarriesTheDomain(t *testing.T) {
	namespace := func(domain string) (string, bool) {
		t.Helper()
		res, err := newResource(Options{ServiceName: "svc", ServiceVersion: "1", Environment: "test", Domain: domain})
		if err != nil {
			t.Fatal(err)
		}
		v, ok := res.Set().Value(semconv.ServiceNamespaceKey)
		return v.AsString(), ok
	}
	if got, ok := namespace("sales"); !ok || got != "sales" {
		t.Errorf("service.namespace = %q (set: %v), want sales", got, ok)
	}
	if got, ok := namespace(""); ok {
		t.Errorf("service.namespace = %q, want the attribute left out without a domain", got)
	}
}
