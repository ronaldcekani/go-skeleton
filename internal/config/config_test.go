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
