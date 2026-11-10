package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "http:\n  port: 8000\n  read_timeout: 3s\ngrpc:\n  port: 9000\nmode: app\nkafka:\n  brokers: [x:9092]\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_GRPC__PORT", "9100")
	t.Setenv("APP_KAFKA__BROKERS", "a:9092,b:9092")
	t.Setenv("APP_POSTGRES__MAX_OPEN_CONNS", "7")

	cfg, err := Load([]string{"--config", path, "--mode", "migration", "--migration-command", "status"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.Port != 8000 {
		t.Errorf("http.port = %d, want 8000 from file", cfg.HTTP.Port)
	}
	if cfg.HTTP.ReadTimeout != 3*time.Second {
		t.Errorf("http.read_timeout = %v, want 3s from file", cfg.HTTP.ReadTimeout)
	}
	if cfg.GRPC.Port != 9100 {
		t.Errorf("grpc.port = %d, want 9100 (env overrides file)", cfg.GRPC.Port)
	}
	if len(cfg.Kafka.Brokers) != 2 || cfg.Kafka.Brokers[1] != "b:9092" {
		t.Errorf("kafka.brokers = %v, want [a:9092 b:9092]", cfg.Kafka.Brokers)
	}
	if cfg.Postgres.MaxOpenConns != 7 {
		t.Errorf("postgres.max_open_conns = %d, want 7", cfg.Postgres.MaxOpenConns)
	}
	if cfg.Mode != ModeMigration {
		t.Errorf("mode = %q, want %q (flag overrides file)", cfg.Mode, ModeMigration)
	}
	if cfg.Migration.Command != "status" {
		t.Errorf("migration.command = %q, want status", cfg.Migration.Command)
	}
	if cfg.Redis.CacheTTL != Default().Redis.CacheTTL {
		t.Errorf("redis.cache_ttl = %v, want default", cfg.Redis.CacheTTL)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown mode":      {"--mode", "nope"},
		"unknown migration": {"--mode", "migration", "--migration-command", "sideways"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(args); err == nil {
				t.Error("Load succeeded, want error")
			}
		})
	}
	t.Run("same ports", func(t *testing.T) {
		t.Setenv("APP_GRPC__PORT", "8080")
		if _, err := Load(nil); err == nil {
			t.Error("Load succeeded, want error")
		}
	})
}

func TestLoadEndpointRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
endpoints:
  rules:
    - name: read
      http: [listItems]
      grpc: [item.v1.ItemService/ListItems]
      public: true
    - name: write
      http: [createItem]
      rate_limit: { requests: 20, period: 1m, burst: 5 }
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rules := cfg.Endpoints.Rules
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want 2", rules)
	}
	if !rules[0].Public || rules[0].RateLimit != nil || rules[0].GRPC[0] != "item.v1.ItemService/ListItems" {
		t.Errorf("read rule = %+v", rules[0])
	}
	if l := rules[1].RateLimit; l == nil || l.Requests != 20 || l.Period != time.Minute || l.Burst != 5 {
		t.Errorf("write rule limit = %+v", rules[1].RateLimit)
	}
}

func TestLoadRejectsBadEndpointRules(t *testing.T) {
	for name, rules := range map[string]string{
		"no name":             "- http: [listItems]",
		"duplicate name":      "- {name: a, http: [x]}\n    - {name: a, http: [y]}",
		"no endpoints":        "- name: a",
		"public with a limit": "- {name: a, http: [x], public: true, rate_limit: {requests: 1, period: 1s}}",
		"zero requests":       "- {name: a, http: [x], rate_limit: {requests: 0, period: 1s}}",
		"no period":           "- {name: a, http: [x], rate_limit: {requests: 5}}",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			yaml := "endpoints:\n  rules:\n    " + rules + "\n"
			if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load([]string{"--config", path}); err == nil {
				t.Error("Load succeeded, want error")
			}
		})
	}
}
