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
| Auth          | Keycloak access tokens, verified locally (`golang-jwt` + `keyfunc`)     |
| Rate limiting | Per user (token `sub`), counted in Redis (`redis_rate`)                 |
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
make up        # Postgres, Redis, Kafka, Keycloak, Grafana/OTel stack
make migrate   # apply DB migrations
make run       # HTTP on :8080, gRPC on :9090
```

Get an access token for the demo user from the local Keycloak, then call
either API with it:

```bash
TOKEN=$(curl -s http://localhost:8081/realms/skeleton/protocol/openid-connect/token \
  -d grant_type=password -d client_id=skeleton-cli -d username=demo -d password=demo \
  | jq -r .access_token)
```

```bash
curl -X POST localhost:8080/v1/items -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' -d '{"name":"first"}'
```

```bash
grpcurl -plaintext -H "authorization: Bearer $TOKEN" -d '{"limit": 10}' \
  localhost:9090 item.v1.ItemService/ListItems
```

To skip Keycloak while developing, run with `APP_AUTH__ENABLED=false`.

Traces and metrics are in Grafana at http://localhost:3000. To run everything
in containers instead: `docker compose --profile app up -d --build`.

## Operation modes

The mode is chosen with `--mode` (or `mode:` in the config file, or `APP_MODE`).

| Mode        | What it does                                                         |
| ----------- | -------------------------------------------------------------------- |
| `app`       | Serves HTTP + gRPC and runs the Kafka consumer until SIGINT/SIGTERM. |
| `migration` | Runs `--migration-command` (`up` default, `down`, `status`) and exits. |

Each mode is a package under `internal/application/` exposing
`Run(ctx, cfg, log) error`. To add one (a worker, a cron job, ...), add a
constant in `internal/config`, a package next to `app` and `migration`, and
an entry in the `modes` map in `cmd/app/main.go`.

## Layout

```
api/openapi/openapi.yaml      HTTP API definition (source of truth)
api/proto/item/v1/item.proto  gRPC API definition (source of truth)
cmd/app/                      main: config, logger, signals, mode dispatch
config/config.yaml            local config
migrations/                   goose SQL migrations (embedded)
internal/
  application/                one package per operation mode
    app/                      build.go (wiring), lifecycle.go (run + shutdown),
                              wire_*.go (one file per layer)
    migration/                applies the embedded migrations
  config/                     config struct, loading, validation
  gen/                        generated code — do not edit
  server/httpserver/          server layer: HTTP handlers + middleware
  server/grpcserver/          server layer: gRPC handlers + interceptors
  server/policy/              per-endpoint auth + rate limit rules, shared by both
  service/                    service layer: business logic
  repository/                 data access layer: Postgres + Redis cache
  messaging/                  domain events <-> Kafka records
  domain/                     entities and errors shared by all layers
  platform/                   logger, telemetry, postgres, redis, kafka, auth,
                              ratelimit clients
deploy/keycloak/              realm imported by the local Keycloak
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

## Inputs and errors

Handlers build a domain input (`domain.CreateItemInput`) and pass it to the
service. Inputs carry their own `Validate()` for rules that depend only on
the input; rules that need other state stay in the service.

Every layer returns plain Go errors. An error that should reach the client
is a `*domain.Error` with a kind and a client-safe message:

```go
return domain.NotFound("item not found")
return domain.WrapError(domain.ErrKindConflict, err, "name already taken")
```

Handlers never choose status codes. Each transport translates kinds in one
place:

| Kind                      | HTTP | gRPC               |
| ------------------------- | ---- | ------------------ |
| `ErrKindInvalidInput`     | 400  | `InvalidArgument`  |
| `ErrKindNotFound`         | 404  | `NotFound`         |
| `ErrKindConflict`         | 409  | `AlreadyExists`    |
| `ErrKindUnauthenticated`  | 401  | `Unauthenticated`  |
| `ErrKindPermissionDenied` | 403  | `PermissionDenied` |
| `ErrKindRateLimited`      | 429  | `ResourceExhausted`|
| `ErrKindInternal`         | 500  | `Internal`         |

- HTTP: `writeError` in `internal/server/httpserver/errors.go`.
- gRPC: `errorInterceptor` in `internal/server/grpcserver/errors.go`.

Any error that is not a `*domain.Error` (a failed query, a bug) is internal:
it is logged with its cause and the client only sees "internal server error".
To add a kind, add the constant and its name in `internal/domain/errors.go`
and one line in each of the two mapping functions.

## Authentication and rate limiting

Callers send a Keycloak access token as `Authorization: Bearer <token>` (the
`authorization` metadata key on gRPC). The token's signature, issuer, expiry
and (optionally) audience are verified locally against the realm's signing
keys, which are fetched at startup and refreshed in the background; no call
to Keycloak is made per request. ID and refresh tokens are rejected.

What each endpoint requires is configured once, for both transports:

```yaml
auth:
  issuer: https://keycloak.example.com/realms/acme
endpoints:
  rules:
    - name: read-items
      http: [listItems, getItem]                # OpenAPI operationIds
      grpc: [item.v1.ItemService/ListItems, item.v1.ItemService/GetItem]
      public: true                              # no token needed
    - name: write-items
      http: [createItem, deleteItem]
      grpc: [item.v1.ItemService/CreateItem, item.v1.ItemService/DeleteItem]
      rate_limit: { requests: 20, period: 1m, burst: 5 }
```

- An endpoint no rule mentions **requires a token and has no rate limit**.
- Rate limits are counted per user (the token's `sub`) in Redis, so they hold
  across replicas. All endpoints of one rule share one bucket per user, over
  HTTP and gRPC combined.
- A limited caller gets `429` with a `Retry-After` header (HTTP) or
  `ResourceExhausted` with a `retry-after` trailer (gRPC).
- If Redis is unreachable the limiter **fails open**: requests are allowed,
  and the failure is logged and counted in `policy.ratelimit.errors`.
- Startup fails if a rule names an operation or method that does not exist.
- The gRPC health and reflection services are never authenticated.

The verified caller is available to handlers and services:

```go
if p, ok := domain.PrincipalFrom(ctx); ok {
    // p.Subject, p.Username, p.Email, p.Roles, p.Scopes
}
```

There are no per-endpoint role rules yet. Realm roles are in `p.Roles` as-is
and client roles as `<client>:<role>`; check them in the service and return
`domain.NewError(domain.ErrKindPermissionDenied, ...)`.

## Configuration

Every key in `config/config.yaml` can be overridden by an environment
variable: `APP_` prefix, upper case, `__` between nesting levels. Lists are
comma-separated.

```bash
APP_HTTP__PORT=8081
APP_POSTGRES__DSN=postgres://user:pass@db:5432/app?sslmode=require
APP_KAFKA__BROKERS=kafka-1:9092,kafka-2:9092
APP_KAFKA__ENABLED=false       # run without Kafka; events are discarded
APP_AUTH__ENABLED=false        # no tokens needed, nothing is rate limited
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

## Lifecycle

`app.Build` connects to every dependency, binds both ports and constructs
the components (HTTP server, gRPC server, Kafka consumers) without starting
them. Every resource it creates is pushed onto a cleanup stack, so a build
that fails halfway closes exactly what already exists.

`App.Run` starts the components and reports ready. On the first
SIGINT/SIGTERM, or if any component fails or exits on its own:

1. Readiness turns unhealthy (`/readyz` → 503, gRPC health → NOT_SERVING)
   and the app keeps serving for `shutdown.drain_delay`, so load balancers
   stop routing to it before connections close.
2. All components are stopped at once: the servers stop accepting
   connections and drain in-flight requests, the consumers finish and commit
   their current batch.
3. The cleanup stack runs in reverse creation order: Kafka, Redis, Postgres,
   and telemetry last so the shutdown itself is exported.

Everything shares one budget, `shutdown.timeout`; whatever is still running
when it ends is cut off. A second signal kills the process immediately. Keep
the orchestrator's grace period (`terminationGracePeriodSeconds`,
`stop_grace_period`) above `shutdown.timeout`, and set `drain_delay` to a few
seconds on Kubernetes.

To add a long-running part (another server, a consumer, a scheduler), return
it as a `component` from one of the `wire_*.go` files.

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
