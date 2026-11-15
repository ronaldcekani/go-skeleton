# The target: what "on the skeleton" means

The skeleton checkout is the source of truth, not this file. Its `README.md`
explains the design, `internal/architecture/architecture_test.go` enforces the
dependency rules, and `internal/feature/item` and `internal/feature/order` are
the worked examples. Read those in full before designing anything. This file
adds what they do not say: how to tell whether a migrated project has really
arrived, and how to adapt the skeleton when the project differs from it.

## Layout

When the work is finished the project's Go code lives in exactly these places:

```
cmd/app/                      main: config, logger, signals, mode dispatch
api/openapi/, api/proto/      API definitions, the source of truth for both transports
migrations/                   SQL migrations, embedded
internal/
  feature/<name>/             one package per feature, everything about it
  domain/                     shared kernel: error kinds, Principal
  platform/<client>/          infrastructure clients, no business meaning
  server/httpserver|grpcserver|policy/   what every endpoint shares
  application/<mode>/         one package per operation mode; app/ holds the wiring
  config/                     config struct, defaults, loading, validation
  gen/                        generated code
  architecture/               the dependency test
  testkit/                    test helpers
tools/                        separate module pinning the code generators
```

Anything else that holds Go code (`pkg/`, `utils/`, `common/`, `models/`,
`handlers/`, `services/`, a second `cmd/`) is either gone or an exception the
user approved, written down in the plan.

## Dependency rules

| Area          | May import (inside the module)               |
| ------------- | -------------------------------------------- |
| `domain`      | nothing                                      |
| `config`      | nothing                                      |
| `platform`    | `domain`, `platform`                         |
| `feature/<x>` | `domain`, `platform`, `gen`; never another feature, never `server` or `config` |
| `server`      | `domain`, `platform`, `gen`, `server`        |
| `application` | everything                                   |
| `testkit`     | nothing; only test files import it           |

`TestDependencyRules` checks this for every non-test file under `internal/`,
and fails for a directory under `internal/` it has no rule for. Features get
their settings as constructor arguments from the wiring, which is why they do
not import `config`.

## Inside a feature

Use the same file names as `item` and `order`, and leave out the files a
feature has no use for.

| File | Holds | Rules |
| ---- | ----- | ----- |
| `<name>.go` | the entity, the inputs, limits and defaults as constants | no `db`, `json` or other struct tags; inputs have `Validate()` for rules that depend only on the input |
| `service.go` | the use cases, and the interfaces they need (`Store`, `Cache`, `EventPublisher`, `Transactor`, one per source of data) | no SQL, no cache or broker client, no HTTP or gRPC types; normalizes, validates, applies rules that need state, orders the side effects |
| extra files such as `pricing.go` | rules as functions without I/O | everything they need is passed in |
| `store.go` | the database | unexported row structs with the tags, mappers to the entity, no business rules; every query through `postgres.Conn(ctx, db)` so it joins a transaction carried by the context; "no rows" becomes `domain.NotFound` |
| `cache.go` | the cache | its own unexported stored format |
| `events.go` | publisher, consumer handler, event wire structs | wire structs are a contract, separate from the entity; a `Noop` publisher for when the broker is disabled |
| `http.go`, `grpc.go` | the handlers | request to input, call the service, result to response; no validation, no defaults, no status codes; errors returned untouched |
| `mapping_test.go` | `mapcheck.Check` for every mapper | a field left out on purpose is named with `IgnoreSource` / `IgnoreResult` |

The ideas behind the table, which decide the cases it does not list:

- **The feature owns its types.** Storage and wire formats are unexported
  structs next to the code that uses them, so a schema or API change stays in
  one file and the entity cannot leak a format.
- **The consumer declares the interface**, in its own types. A feature that
  needs something from another feature declares what it needs (`order.Customer`
  is an ID and group IDs, not the user feature's `User`), and
  `wire_features.go` passes in the other feature's service or a small adapter
  around it.
- **Handlers only translate**, which is what makes HTTP and gRPC behave the
  same and what makes a transport replaceable.
- **Errors are plain Go errors.** One that should reach the client is a
  `*domain.Error` with a kind; each transport turns kinds into statuses in
  exactly one place. Anything else is internal and only logged.
- **`internal/domain` stays tiny.** A type belongs there only if every feature
  would otherwise define it identically. "Used by two features" is not enough:
  each gets its own view.
- **Side effects that are not transactional happen after the commit**, and the
  service is where their order is visible.

## Outside the features

- `internal/platform/<client>`: one package per infrastructure client, with an
  `Options` struct, a constructor that connects and verifies, and
  instrumentation. Nothing in it knows a feature exists.
- `internal/server`: middleware, interceptors, error translation, request
  validation, the per-endpoint policy (auth and rate limit rules from the
  config, keyed by OpenAPI operationId and gRPC method). Features plug into
  the servers; the servers never import a feature.
- `internal/application/app`: `Build` constructs everything without starting
  it and pushes every resource on the cleanup stack; `wire_platform.go`,
  `wire_features.go`, `wire_servers.go` and `wire_consumers.go` each build one
  layer; anything long-running is a `component` with `start` and `stop`. This
  is the only place that sees everything, so it is where globals, `init()`
  side effects and DI containers end up as ordinary constructor calls.
- Operation modes: anything the binary can do besides serving (migrations, a
  worker, a one-off job, the old project's CLI subcommands) is a package under
  `internal/application` with `Run(ctx, cfg, log) error`, a constant in
  `internal/config` and an entry in the `modes` map in `cmd/app/main.go`.
- `internal/config`: one struct, `Default()`, validation at load. Every value
  a feature needs reaches it through its constructor.

## When the project differs from the skeleton's stack

The architecture is the layout and the rules above. The skeleton also picks
products and libraries, and those are a separate question for each row.

| Area | Skeleton | Default when the project uses something else |
| ---- | -------- | --------------------------------------------- |
| Database, cache or broker **product** (MySQL, MongoDB, RabbitMQ, NATS, ...) | Postgres, Redis, Kafka | Keep the product. Give it an `internal/platform/<product>` package shaped like the skeleton's (`Options`, a constructor, a context-carried transaction and `Conn` where the product has transactions). Changing where data lives is a data migration, not a refactoring. |
| Data access library (an ORM, `database/sql`, sqlc, ...) | `sqlx`, hand-written SQL | Keep it, inside `store.go`. The architecture needs the store to hide it, not to replace it. Rewriting queries is the easiest way to change behavior without noticing, so it is a swap the user asks for, done last. |
| HTTP framework and routing (gin, echo, chi, ...) | `net/http` and a strict server generated from `openapi.yaml` | Adopt, as a stack swap after the features are in place: write the spec to describe the existing API exactly, then reimplement the (by then thin) handlers on the generated interface. |
| gRPC | stubs generated with buf | Adopt the generation setup. The proto `package`, service and method names are the wire contract and do not change; only `go_package` and file locations do. |
| No gRPC, no cache, no broker in the project | present | Leave the part out completely, including its config section, wiring, platform package and Makefile targets. Do not add a transport or a dependency the project did not have. |
| Config loading | koanf, `APP_` variables | Adopt the loader; every existing key, variable and flag keeps working (see contracts.md). |
| Logging, telemetry | `slog`, OpenTelemetry | Adopt; keep the names and fields that something outside the service consumes. |
| Auth and rate limiting | Keycloak tokens, per-endpoint policy | Keep the existing scheme: implement it behind the interfaces `server/policy` declares, with a rule for every endpoint that reproduces today's requirement. Changing who can call what is never a side effect of a refactoring. |
| Migrations | goose, embedded | Adopt only with the continuity plan in contracts.md. |
| DI container, service locator, globals | explicit wiring | Replace with the wiring. This one is part of the architecture, not optional. |

"Adopt" rows are proposed in the plan and done one at a time in the stack
step. A row the user declines stays as it is, placed in the skeleton's
layout: the project then has the architecture with its own library in that
slot, which is a legitimate end state.

## Arrival checklist

Each line is checked by running something, not by reading:

- `go build ./... && go vet ./... && go test -race ./...` pass, with the
  database and cache tests running against real services, not skipped.
- `internal/architecture/architecture_test.go` differs from the skeleton's
  only in the `module` constant (`diff` the two files). A rule added to make a
  package pass is a failed migration, not a fix.
- No Go package exists outside the layout above (`go list ./...`).
- Every feature has a `mapping_test.go` that lists every mapper in the
  package; search the package for functions that return or take a row, cache,
  event or generated API type and compare.
- No struct tag in an entity or input: search `<name>.go` files for backquotes.
- No handler contains a branch on business state, and no service imports a
  transport or driver package: `go list -deps` or a search of the imports.
- The skeleton's example code is gone: no `item` or `order` feature, API
  definitions, migrations, config rules or `ItemsTopic` unless the project
  really has such features, and no `github.com/example/go-skeleton` anywhere.
- `make generate` leaves the working tree unchanged.
