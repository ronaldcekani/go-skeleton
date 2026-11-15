package telemetry

import "testing"

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
