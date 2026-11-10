// Package app wires the application together and runs one operation mode.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"

	"github.com/example/go-skeleton/internal/config"
	"github.com/example/go-skeleton/internal/messaging"
	"github.com/example/go-skeleton/internal/platform/kafka"
	"github.com/example/go-skeleton/internal/platform/postgres"
	"github.com/example/go-skeleton/internal/platform/redis"
	"github.com/example/go-skeleton/internal/platform/telemetry"
	"github.com/example/go-skeleton/internal/repository"
	"github.com/example/go-skeleton/internal/server/grpcserver"
	"github.com/example/go-skeleton/internal/server/httpserver"
	"github.com/example/go-skeleton/internal/service"
)

// Run executes the mode selected in cfg and returns when it is finished: for
// the app mode, that is after ctx is cancelled and shutdown has completed.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	switch cfg.Mode {
	case config.ModeApp:
		return runApp(ctx, cfg, log)
	case config.ModeMigration:
		return runMigration(ctx, cfg, log)
	default:
		return fmt.Errorf("unknown mode %q", cfg.Mode)
	}
}

func openPostgres(ctx context.Context, cfg config.Config) (*sqlx.DB, error) {
	return postgres.Open(ctx, postgres.Options{
		DSN:             cfg.Postgres.DSN,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Postgres.ConnMaxIdleTime,
	})
}

// runApp serves HTTP and gRPC until ctx is cancelled or a component fails.
//
// Resources are released in the reverse order of their creation: servers
// stop accepting work and drain first, then the Kafka consumer and producer,
// then Redis and Postgres, and telemetry is flushed last so that spans and
// metrics from the shutdown itself are exported.
func runApp(ctx context.Context, cfg config.Config, log *slog.Logger) (err error) {
	// cleanup runs deferred steps with a fresh deadline: ctx is already
	// cancelled by the time they execute.
	cleanup := func(name string, fn func(context.Context) error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Shutdown.Timeout)
		defer cancel()
		if cerr := fn(cctx); cerr != nil {
			log.Error("shutdown step failed", slog.String("step", name), slog.Any("error", cerr))
			return
		}
		log.Debug("shutdown step done", slog.String("step", name))
	}

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
		return fmt.Errorf("setup telemetry: %w", err)
	}
	defer cleanup("telemetry", shutdownTelemetry)

	db, err := openPostgres(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup("postgres", func(context.Context) error { return db.Close() })

	rdb, err := redis.Open(ctx, redis.Options{
		Addr:     cfg.Redis.Addr,
		Username: cfg.Redis.Username,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		return err
	}
	defer cleanup("redis", func(context.Context) error { return rdb.Close() })

	var (
		publisher service.EventPublisher = messaging.NoopPublisher{}
		consumer  *kafka.Consumer
	)
	if cfg.Kafka.Enabled {
		kopts := kafka.Options{Brokers: cfg.Kafka.Brokers, ClientID: cfg.Kafka.ClientID}

		producer, err := kafka.NewProducer(ctx, kopts)
		if err != nil {
			return err
		}
		defer cleanup("kafka producer", producer.Close)
		publisher = messaging.NewItemPublisher(producer, cfg.Kafka.ItemsTopic)

		handler := messaging.NewItemEventHandler(log)
		consumer, err = kafka.NewConsumer(ctx, kopts, cfg.Kafka.ConsumerGroup,
			[]string{cfg.Kafka.ItemsTopic}, handler.Handle, log)
		if err != nil {
			return err
		}
		// consumer.Run closes the client when it returns.
	} else {
		log.InfoContext(ctx, "kafka disabled, events are discarded")
	}

	items, err := service.NewItemService(
		repository.NewItemRepository(db),
		repository.NewItemCache(rdb, cfg.Redis.CacheTTL),
		publisher,
		log,
	)
	if err != nil {
		return err
	}

	var shuttingDown atomic.Bool
	ready := func(ctx context.Context) error {
		if shuttingDown.Load() {
			return errors.New("shutting down")
		}
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	}

	httpSrv, err := httpserver.New(httpserver.Options{
		Host:              cfg.HTTP.Host,
		Port:              cfg.HTTP.Port,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}, items, ready, log)
	if err != nil {
		return err
	}
	grpcSrv := grpcserver.New(grpcserver.Options{Reflection: cfg.GRPC.Reflection}, items, log)

	// Bind both ports before serving so a port conflict fails startup
	// instead of leaving the app half up.
	var lc net.ListenConfig
	httpLis, err := lc.Listen(ctx, "tcp", httpSrv.Addr)
	if err != nil {
		return fmt.Errorf("listen http: %w", err)
	}
	grpcLis, err := lc.Listen(ctx, "tcp", net.JoinHostPort(cfg.GRPC.Host, strconv.Itoa(cfg.GRPC.Port)))
	if err != nil {
		_ = httpLis.Close()
		return fmt.Errorf("listen grpc: %w", err)
	}

	// gctx is cancelled on a signal (via ctx) or when any goroutine below
	// returns an error; either way everything is shut down together.
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		log.InfoContext(ctx, "http server listening", slog.String("addr", httpLis.Addr().String()))
		if err := httpSrv.Serve(httpLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		log.InfoContext(ctx, "grpc server listening", slog.String("addr", grpcLis.Addr().String()))
		if err := grpcSrv.Serve(grpcLis); err != nil {
			return fmt.Errorf("grpc server: %w", err)
		}
		return nil
	})
	if consumer != nil {
		g.Go(func() error {
			log.InfoContext(ctx, "kafka consumer started",
				slog.String("topic", cfg.Kafka.ItemsTopic), slog.String("group", cfg.Kafka.ConsumerGroup))
			if err := consumer.Run(gctx); err != nil {
				return fmt.Errorf("kafka consumer: %w", err)
			}
			log.Debug("kafka consumer stopped")
			return nil
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutdown started", slog.Duration("timeout", cfg.Shutdown.Timeout))

		// Fail readiness first so load balancers stop routing here.
		shuttingDown.Store(true)
		grpcSrv.Health.Shutdown()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Shutdown.Timeout)
		defer cancel()

		// Drain both servers concurrently: stop accepting connections and
		// wait for in-flight requests, up to the timeout.
		var wg sync.WaitGroup
		wg.Go(func() {
			if err := httpSrv.Shutdown(sctx); err != nil {
				log.Error("http server did not drain in time, closing", slog.Any("error", err))
				_ = httpSrv.Close()
			}
		})
		wg.Go(func() {
			stopped := make(chan struct{})
			go func() {
				grpcSrv.GracefulStop()
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-sctx.Done():
				log.Error("grpc server did not drain in time, stopping")
				grpcSrv.Stop()
			}
		})
		wg.Wait()
		log.Debug("servers stopped")
		return nil
	})

	err = g.Wait()
	log.Info("shutdown complete, releasing resources")
	return err
}
