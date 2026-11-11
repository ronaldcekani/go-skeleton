package app

import (
	"context"
	"log/slog"

	"github.com/jmoiron/sqlx"
	goredis "github.com/redis/go-redis/v9"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/platform/auth"
	"github.com/example/go-skeleton/internal/platform/kafka"
	"github.com/example/go-skeleton/internal/platform/postgres"
	"github.com/example/go-skeleton/internal/platform/ratelimit"
	"github.com/example/go-skeleton/internal/platform/redis"
	"github.com/example/go-skeleton/internal/platform/telemetry"
)

// platform holds the infrastructure clients. Optional ones are nil when
// disabled in the config.
type platform struct {
	db            *sqlx.DB
	tx            *postgres.Transactor // give it to services that write through several repositories
	redis         *goredis.Client
	producer      *kafka.Producer     // nil: Kafka disabled
	authenticator *auth.Authenticator // nil: auth disabled
	limiter       *ratelimit.Limiter  // nil: rate limiting disabled
}

// wirePlatform connects to the infrastructure. Telemetry comes first so it is
// closed last and exports what the rest of the shutdown produces.
func wirePlatform(ctx context.Context, cfg config.Config, log *slog.Logger, cleanup *cleanupStack) (*platform, error) {
	shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Options{
		Enabled:        cfg.Telemetry.Enabled,
		ServiceName:    cfg.Service.Name,
		ServiceVersion: cfg.Service.Version,
		Environment:    cfg.Service.Environment,
		OTLPEndpoint:   cfg.Telemetry.OTLPEndpoint,
		OTLPInsecure:   cfg.Telemetry.OTLPInsecure,
		TraceSampling:  cfg.Telemetry.TraceSampling,
		MetricInterval: cfg.Telemetry.MetricInterval,
	}, log)
	if err != nil {
		return nil, err
	}
	cleanup.push("telemetry", shutdownTelemetry)

	p := &platform{}

	p.db, err = postgres.Open(ctx, postgres.Options{
		DSN:             cfg.Postgres.DSN,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,
	})
	if err != nil {
		return nil, err
	}
	cleanup.push("postgres", closeWithErr(p.db.Close))
	p.tx = postgres.NewTransactor(p.db)

	p.redis, err = redis.Open(ctx, redis.Options{
		Addr:     cfg.Redis.Addr,
		Username: cfg.Redis.Username,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		return nil, err
	}
	cleanup.push("redis", closeWithErr(p.redis.Close))

	if cfg.Kafka.Enabled {
		p.producer, err = kafka.NewProducer(ctx, kafka.Options{
			Brokers:  cfg.Kafka.Brokers,
			ClientID: cfg.Kafka.ClientID,
		})
		if err != nil {
			return nil, err
		}
		cleanup.push("kafka producer", p.producer.Close)
	} else {
		log.WarnContext(ctx, "kafka disabled: events are discarded")
	}

	if cfg.Auth.Enabled {
		p.authenticator, err = auth.New(auth.Options{
			Issuer:   cfg.Auth.Issuer,
			JWKSURL:  cfg.Auth.JWKSURL,
			Audience: cfg.Auth.Audience,
			Leeway:   cfg.Auth.Leeway,
		}, log)
		if err != nil {
			return nil, err
		}
		cleanup.push("authenticator", closeNoErr(p.authenticator.Close))

		if cfg.RateLimit.Enabled {
			p.limiter = ratelimit.New(p.redis)
		} else {
			log.WarnContext(ctx, "rate limiting disabled")
		}
	} else {
		log.WarnContext(ctx, "auth disabled: every endpoint is public and nothing is rate limited")
	}

	return p, nil
}
