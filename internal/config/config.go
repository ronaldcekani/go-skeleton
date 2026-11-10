// Package config loads application configuration from, in increasing order of
// precedence: built-in defaults, an optional YAML file, environment variables
// and command-line flags.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// Operation modes of the binary.
const (
	ModeApp       = "app"
	ModeMigration = "migration"
)

// EnvPrefix is the prefix of environment variables that override config keys.
// Nested keys are separated by a double underscore, e.g. APP_HTTP__PORT or
// APP_POSTGRES__MAX_OPEN_CONNS.
const EnvPrefix = "APP_"

type Config struct {
	Mode      string    `koanf:"mode"`
	Service   Service   `koanf:"service"`
	Log       Log       `koanf:"log"`
	HTTP      HTTP      `koanf:"http"`
	GRPC      GRPC      `koanf:"grpc"`
	Postgres  Postgres  `koanf:"postgres"`
	Redis     Redis     `koanf:"redis"`
	Kafka     Kafka     `koanf:"kafka"`
	Telemetry Telemetry `koanf:"telemetry"`
	Migration Migration `koanf:"migration"`
	Shutdown  Shutdown  `koanf:"shutdown"`
}

type Service struct {
	Name        string `koanf:"name"`
	Version     string `koanf:"version"`
	Environment string `koanf:"environment"`
}

type Log struct {
	Level  string `koanf:"level"`  // debug, info, warn, error
	Format string `koanf:"format"` // json, text
}

type HTTP struct {
	Host              string        `koanf:"host"`
	Port              int           `koanf:"port"`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	ReadTimeout       time.Duration `koanf:"read_timeout"`
	WriteTimeout      time.Duration `koanf:"write_timeout"`
	IdleTimeout       time.Duration `koanf:"idle_timeout"`
}

type GRPC struct {
	Host       string `koanf:"host"`
	Port       int    `koanf:"port"`
	Reflection bool   `koanf:"reflection"`
}

type Postgres struct {
	DSN             string        `koanf:"dsn"`
	MaxOpenConns    int           `koanf:"max_open_conns"`
	MaxIdleConns    int           `koanf:"max_idle_conns"`
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `koanf:"conn_max_idle_time"`
}

type Redis struct {
	Addr     string        `koanf:"addr"`
	Username string        `koanf:"username"`
	Password string        `koanf:"password"`
	DB       int           `koanf:"db"`
	CacheTTL time.Duration `koanf:"cache_ttl"`
}

type Kafka struct {
	Enabled       bool     `koanf:"enabled"`
	Brokers       []string `koanf:"brokers"`
	ClientID      string   `koanf:"client_id"`
	ItemsTopic    string   `koanf:"items_topic"`
	ConsumerGroup string   `koanf:"consumer_group"`
}

type Telemetry struct {
	Enabled        bool          `koanf:"enabled"`
	OTLPEndpoint   string        `koanf:"otlp_endpoint"` // host:port of an OTLP/gRPC collector
	OTLPInsecure   bool          `koanf:"otlp_insecure"`
	TraceSampling  float64       `koanf:"trace_sampling"` // 0..1, parent-based ratio
	MetricInterval time.Duration `koanf:"metric_interval"`
}

type Migration struct {
	Command string `koanf:"command"` // up, down, status
}

type Shutdown struct {
	Timeout time.Duration `koanf:"timeout"`
}

// Default returns the configuration used when nothing overrides it.
func Default() Config {
	return Config{
		Mode: ModeApp,
		Service: Service{
			Name:        "go-skeleton",
			Version:     "dev",
			Environment: "local",
		},
		Log: Log{Level: "info", Format: "json"},
		HTTP: HTTP{
			Host:              "0.0.0.0",
			Port:              8080,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		GRPC: GRPC{Host: "0.0.0.0", Port: 9090, Reflection: true},
		Postgres: Postgres{
			DSN:             "postgres://postgres:postgres@localhost:5432/skeleton?sslmode=disable",
			MaxOpenConns:    20,
			MaxIdleConns:    10,
			ConnMaxLifetime: 30 * time.Minute,
			ConnMaxIdleTime: 5 * time.Minute,
		},
		Redis: Redis{Addr: "localhost:6379", CacheTTL: 5 * time.Minute},
		Kafka: Kafka{
			Enabled:       true,
			Brokers:       []string{"localhost:9092"},
			ClientID:      "go-skeleton",
			ItemsTopic:    "items.events",
			ConsumerGroup: "go-skeleton",
		},
		Telemetry: Telemetry{
			Enabled:        true,
			OTLPEndpoint:   "localhost:4317",
			OTLPInsecure:   true,
			TraceSampling:  1,
			MetricInterval: 15 * time.Second,
		},
		Migration: Migration{Command: "up"},
		Shutdown:  Shutdown{Timeout: 20 * time.Second},
	}
}

// Load builds the configuration from defaults, the file named by --config (if
// any), APP_* environment variables and flags, then validates it.
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	configPath := fs.String("config", os.Getenv(EnvPrefix+"CONFIG"), "path to a YAML config file (optional)")
	fs.String("mode", ModeApp, "operation mode: app | migration")
	fs.String("migration-command", "up", "migration mode command: up | down | status")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	k := koanf.New(".")
	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}
	if *configPath != "" {
		if err := k.Load(file.Provider(*configPath), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config file %q: %w", *configPath, err)
		}
	}
	// APP_HTTP__READ_TIMEOUT -> http.read_timeout. A variable whose key holds
	// a list (e.g. APP_KAFKA__BROKERS) is split on commas.
	envProvider := env.ProviderWithValue(EnvPrefix, ".", func(name, value string) (string, any) {
		key := strings.ToLower(strings.TrimPrefix(name, EnvPrefix))
		key = strings.ReplaceAll(key, "__", ".")
		switch k.Get(key).(type) {
		case []string, []any: // from the defaults or from a YAML file
			parts := strings.Split(value, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			return key, parts
		}
		return key, value
	})
	if err := k.Load(envProvider, nil); err != nil {
		return Config{}, fmt.Errorf("load environment: %w", err)
	}
	// Only flags set explicitly on the command line override lower layers.
	flagKeys := map[string]string{"mode": "mode", "migration-command": "migration.command"}
	overrides := map[string]any{}
	fs.Visit(func(f *flag.Flag) {
		if key, ok := flagKeys[f.Name]; ok {
			overrides[key] = f.Value.String()
		}
	})
	if err := k.Load(confmap.Provider(overrides, "."), nil); err != nil {
		return Config{}, fmt.Errorf("load flags: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// Validate reports every problem found so they can be fixed in one pass.
func (c Config) Validate() error {
	var errs []error
	switch c.Mode {
	case ModeApp, ModeMigration:
	default:
		errs = append(errs, fmt.Errorf("mode: unknown mode %q (want %q or %q)", c.Mode, ModeApp, ModeMigration))
	}
	if c.Postgres.DSN == "" {
		errs = append(errs, errors.New("postgres.dsn: required"))
	}
	if c.Mode == ModeMigration {
		switch c.Migration.Command {
		case "up", "down", "status":
		default:
			errs = append(errs, fmt.Errorf("migration.command: unknown command %q (want up, down or status)", c.Migration.Command))
		}
	}
	if c.Mode == ModeApp {
		if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
			errs = append(errs, fmt.Errorf("http.port: %d out of range", c.HTTP.Port))
		}
		if c.GRPC.Port <= 0 || c.GRPC.Port > 65535 {
			errs = append(errs, fmt.Errorf("grpc.port: %d out of range", c.GRPC.Port))
		}
		if c.HTTP.Port == c.GRPC.Port {
			errs = append(errs, errors.New("http.port and grpc.port must differ"))
		}
		if c.Redis.Addr == "" {
			errs = append(errs, errors.New("redis.addr: required"))
		}
		if c.Kafka.Enabled {
			if len(c.Kafka.Brokers) == 0 {
				errs = append(errs, errors.New("kafka.brokers: required when kafka is enabled"))
			}
			if c.Kafka.ItemsTopic == "" || c.Kafka.ConsumerGroup == "" {
				errs = append(errs, errors.New("kafka.items_topic and kafka.consumer_group: required when kafka is enabled"))
			}
		}
		if c.Shutdown.Timeout <= 0 {
			errs = append(errs, errors.New("shutdown.timeout: must be positive"))
		}
	}
	if c.Telemetry.Enabled {
		if c.Telemetry.OTLPEndpoint == "" {
			errs = append(errs, errors.New("telemetry.otlp_endpoint: required when telemetry is enabled"))
		}
		if c.Telemetry.TraceSampling < 0 || c.Telemetry.TraceSampling > 1 {
			errs = append(errs, errors.New("telemetry.trace_sampling: must be between 0 and 1"))
		}
	}
	return errors.Join(errs...)
}
