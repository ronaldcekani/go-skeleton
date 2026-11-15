package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestDomainAttribute(t *testing.T) {
	logLine := func(domain string) string {
		t.Helper()
		var out bytes.Buffer
		log, err := New(&out, Options{Level: "info", Format: "json", Service: "svc", Domain: domain})
		if err != nil {
			t.Fatal(err)
		}
		log.Info("hello")
		return out.String()
	}
	if line := logLine("sales"); !strings.Contains(line, `"domain":"sales"`) {
		t.Errorf("log line has no domain: %s", line)
	}
	if line := logLine(""); strings.Contains(line, "domain") {
		t.Errorf("log line has a domain field without a configured domain: %s", line)
	}
}
