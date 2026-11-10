# go-skeleton

A reusable Go service skeleton: one binary that serves **HTTP and gRPC on
separate ports**, with both servers generated from API definitions, a layered
architecture, Postgres, Redis, Kafka, structured logging, OpenTelemetry and
graceful shutdown.

| Concern       | Choice                                                                 |
| ------------- | ---------------------------------------------------------------------- |
| HTTP          | `net/http` `ServeMux` + [oapi-codegen] strict server from `openapi.yaml` |
| gRPC          | `grpc-go`, stubs generated with [buf] from `.proto` files               |
| Database      | Postgres via `sqlx` (pgx driver), hand-written SQL                      |
| Migrations    | [goose], SQL files embedded in the binary                               |
| Cache         | Redis via `go-redis`                                                    |
| Messaging     | Kafka via [franz-go]                                                    |
| Config        | [koanf]: defaults → YAML file → environment → flags                     |
| Logging       | `log/slog`, JSON or text, with `trace_id` / `span_id` on every record   |
| Observability | OpenTelemetry traces + metrics over OTLP/gRPC                           |

[oapi-codegen]: https://github.com/oapi-codegen/oapi-codegen
[buf]: https://buf.build
[goose]: https://github.com/pressly/goose
[franz-go]: https://github.com/twmb/franz-go
[koanf]: https://github.com/knadh/koanf

## Quick start

Requires Go 1.26+ and Docker.

```bash
make up        # Postgres, Redis, Kafka, Grafana/OTel stack
make migrate   # apply DB migrations
make run       # HTTP on :8080, gRPC on :9090
```

```bash
curl -X POST localhost:8080/v1/items -H 'content-type: application/json' -d '{"name":"first"}'
curl localhost:8080/v1/items
grpcurl -plaintext -d '{"limit": 10}' localhost:9090 item.v1.ItemService/ListItems
```

Traces and metrics are in Grafana at http://localhost:3000. To run everything
in containers instead: `docker compose --profile app up -d --build`.

## Operation modes

The mode is chosen with `--mode` (or `mode:` in the config file, or `APP_MODE`).

| Mode        | What it does                                                         |
| ----------- | -------------------------------------------------------------------- |
| `app`       | Serves HTTP + gRPC and runs the Kafka consumer until SIGINT/SIGTERM. |
| `migration` | Runs `--migration-command` (`up` default, `down`, `status`) and exits. |

To add a mode (a worker, a cron job, ...), add a constant in
`internal/config`, a `run<Mode>` function in `internal/app`, and a case in
`app.Run`.

## Layout

```
api/openapi/openapi.yaml      HTTP API definition (source of truth)
api/proto/item/v1/item.proto  gRPC API definition (source of truth)
cmd/app/                      main: config, logger, signals, mode dispatch
config/config.yaml            local config
migrations/                   goose SQL migrations (embedded)
internal/
  app/                        wiring, startup and shutdown per mode
  config/                     config struct, loading, validation
  gen/                        generated code — do not edit
  server/httpserver/          server layer: HTTP handlers + middleware
  server/grpcserver/          server layer: gRPC handlers + interceptors
  service/                    service layer: business logic
  repository/                 data access layer: Postgres + Redis cache
  messaging/                  domain events <-> Kafka records
  domain/                     entities and errors shared by all layers
  platform/                   logger, telemetry, postgres, redis, kafka clients
tools/                        separate Go module pinning the code generators
```

Dependencies point inwards: `server → service → repository`. The service
layer declares the interfaces it needs (`ItemStore`, `ItemCache`,
`EventPublisher`), so it has no knowledge of HTTP, gRPC, SQL, Redis or Kafka
and is unit-tested with fakes.

## Changing the API

Edit `api/openapi/openapi.yaml` or the `.proto` files, then:

```bash
make generate
```

The build then fails until the handlers in `internal/server/...` implement
the new generated interface. Incoming HTTP requests are validated against the
OpenAPI spec before they reach a handler.

## Configuration

Every key in `config/config.yaml` can be overridden by an environment
variable: `APP_` prefix, upper case, `__` between nesting levels. Lists are
comma-separated.

```bash
APP_HTTP__PORT=8081
APP_POSTGRES__DSN=postgres://user:pass@db:5432/app?sslmode=require
APP_KAFKA__BROKERS=kafka-1:9092,kafka-2:9092
APP_KAFKA__ENABLED=false       # run without Kafka; events are discarded
APP_TELEMETRY__ENABLED=false   # run without an OTLP collector
```

The config file itself is optional (`--config` or `APP_CONFIG`); the defaults
live in `internal/config/config.go`.

## Observability

- **Traces**: a server span per HTTP request / gRPC call, a span per service
  method, SQL query, Redis command, and Kafka produce / consume. Trace context
  travels in Kafka headers, so a request and the consumption of the event it
  produced appear in one trace.
- **Metrics**: HTTP and gRPC server metrics, DB pool stats, Redis and Kafka
  client metrics, Go runtime metrics, plus two example business counters in
  `internal/service`.
- **Logs**: pass the request context (`log.InfoContext(ctx, ...)`) and the
  record carries `trace_id` and `span_id`.
- **Probes**: `GET /healthz` (liveness), `GET /readyz` (checks Postgres and
  Redis, fails during shutdown), and the standard gRPC health service.

## Graceful shutdown

On the first SIGINT/SIGTERM (or if any component fails):

1. Readiness turns unhealthy (`/readyz` → 503, gRPC health → NOT_SERVING).
2. HTTP and gRPC servers stop accepting connections and drain in-flight
   requests, bounded by `shutdown.timeout`; after that they are force-closed.
3. The Kafka consumer finishes and commits its current batch, then leaves the
   group. The producer is flushed.
4. Redis and Postgres are closed.
5. Telemetry is flushed last.

A second signal kills the process immediately. Keep the orchestrator's grace
period (`terminationGracePeriodSeconds`, `stop_grace_period`) above
`shutdown.timeout`.

## Using this as a template

1. Rename the module: replace `github.com/example/go-skeleton` everywhere
   (`go.mod`, imports, `go_package` in the proto file), then `make generate`.
2. Replace the example `Item` resource: API definitions, `domain`, `service`,
   `repository`, `messaging`, the migration, and the two server handlers.

## Things to know

- Events are published after the DB write commits, so a crash in between
  loses the event. If that is not acceptable, add a transactional outbox.
- The Kafka consumer is at-least-once: handlers must be idempotent. A handler
  error is logged and the record is skipped; add retries or a dead-letter
  topic in `internal/platform/kafka` if you need them.
- Topics are auto-created for local development. Create them explicitly in
  production.
