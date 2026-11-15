# Frozen behavior: what must not change, and where it changes by accident

A refactoring is invisible from outside. "Outside" is wider than the API:
it is every client, every other service, the database, the message broker,
the cache, the deployment, the dashboards and the people on call. Everything
below is frozen unless the user decides otherwise in the plan.

## The frozen surface

Go through this list when writing the inventory; each line that applies to
the project becomes inventory items.

**API.** For every HTTP endpoint: method, path, path and query parameter
names, which parameters are optional and their defaults, request body shape,
what happens with unknown or missing fields, accepted content types, status
code for every outcome, response headers, and the response body down to
field names, field order where clients might depend on it, `null` versus
absent versus empty, number and time formats. The same for error responses:
status, body shape, code strings, and the message text when clients show or
match it. For gRPC: proto package, service, method and field names and
numbers, status code and message per outcome, metadata and trailers. Also the
routes nobody wrote on purpose: unknown path, wrong method, oversized body,
trailing slash, `OPTIONS` / CORS, panics.

**Who may call what.** Per endpoint: public or authenticated, by which
scheme, which roles, scopes or ownership checks, and any rate limit.

**Data.** Table and column names, types, constraints and defaults; what is
written, in which transaction, at which isolation level; generated IDs
(type, version, who generates them); cache key formats, value formats and
TTLs; anything written to object storage or files.

**Messages.** For everything published: topic or exchange, key, headers,
payload bytes, when it is sent relative to the database commit, and whether a
failed publish fails the request. For everything consumed: topic, consumer
group or queue name, where a new group starts, acknowledgement timing,
retry, dead-letter and ordering behavior, concurrency.

**Calls to other systems.** URL, method, headers, body, timeout, retry
policy, and what happens to the request when the call fails.

**Process.** Binary name and path, command-line flags and subcommands, exit
codes, every environment variable and config key with its default, ports,
health and readiness paths and what they check, signal handling, shutdown
order and deadlines, files read or written, what happens at startup when a
dependency is down.

**Operations.** Metric names, labels and units; trace span names and
attributes that dashboards or alerts use; log messages and field names that
alerts or log queries match; the log format if something parses it.

**Build and delivery.** Dockerfile entrypoint and exposed ports, CI
commands, Makefile targets other people or pipelines call, the Go packages
other modules import (their import paths are an API).

## Where the skeleton's defaults would change behavior

Adopting a skeleton part as-is can change behavior with no line of business
logic touched. Check each of these against the inventory before a part goes
in, and configure or adapt the part until the old behavior is reproduced.

| Skeleton behavior | What to compare with the old project |
| ----------------- | ------------------------------------ |
| Errors are `{"code","message"}` with the status from the kind table; internal errors answer "internal server error" | every old error body and status, including ones that exposed the underlying error text |
| Requests are validated against the OpenAPI spec before the handler runs | what the old code accepted: extra fields, loose types, missing content type, empty bodies. The spec has to describe what was accepted, not what would be clean. |
| Unknown path, wrong method and oversized body get JSON errors (404, 405, 413) | the old router's responses for the same |
| `http.max_body_bytes` defaults to 1 MiB; read, write and idle timeouts are set | upload sizes, long downloads, streaming, server-sent events, WebSockets |
| With auth enabled, an endpoint no rule mentions requires a token; a public endpoint cannot be rate limited | every endpoint needs a rule that states today's requirement |
| Tokens are Keycloak access tokens verified against a JWKS, with a required audience | the old scheme and its claims; `Principal` fields the old code read |
| Rate limits are per token subject, in Redis, failing open | old limits, their key (IP, API key) and their failure behavior |
| `/healthz` and `/readyz`; readiness checks Postgres only and fails during shutdown | old probe paths and what they checked; the deployment's probe config |
| Shutdown: not-ready, drain delay, stop components together, close resources in reverse order, one total budget | old order and timeouts; anything the old shutdown flushed or deregistered |
| Kafka publishing is asynchronous and best effort, after the commit | whether a failed publish used to fail the request; ordering guarantees |
| The Kafka consumer commits after the batch, skips a record whose handler failed, and a new group starts at the oldest offset | old acknowledgement, retry and dead-letter behavior; keep the group name, or the service reprocesses the topic |
| Cache-aside with tombstones; cache failures degrade to store reads | old cache semantics; whether new code can read entries the old code wrote |
| IDs are UUIDv7 generated in the service | old ID type and generator |
| The example service trims names and defaults page sizes | nothing: these are the examples' rules, not the project's. Carry over only what the old code did. |
| Config keys are overridden by `APP_`-prefixed variables with `__` between levels | every variable and flag the deployment sets today |
| gRPC reflection is on by default; health and reflection are never authenticated | whether the old server exposed them |
| Pool sizes, connection lifetimes and client timeouts have skeleton defaults | the old values. `refactorcheck` reports number literals that disappeared, which is how a pool of 25 silently becoming 20 gets noticed. |
| Logs are `slog` JSON with `trace_id`, `span_id` and service fields | fields and messages that alerts match |
| Spans are named `<feature>.Service.<Method>`; metrics follow the examples | existing span and metric names in use |

## Continuity traps

These do not show up in any test that starts from an empty database.

- **Migrations.** A database that already exists has no goose version table.
  If the old project used another tool, or none, switching to goose needs an
  explicit plan: translate the existing migrations without changing the
  schema they produce, and decide with the user how existing databases are
  marked as already migrated (a baseline version, or a first migration that
  is a no-op where the schema exists). Compare the schema both paths produce
  with a schema dump. Never run a migration against a database that is not a
  local throwaway one.
- **Consumer groups and offsets.** A renamed group reprocesses or skips
  history. Keep group, topic and client IDs.
- **Cached data.** After deployment the new code reads entries the old code
  wrote, and during a rolling deployment both run at once. Either the format
  is unchanged (pin it with a test recorded from the old code) or the key
  prefix changes so the two never read each other's entries.
- **In-flight messages.** Events published by the old version are consumed
  by the new one. The consumer has to accept the old payloads, byte for byte.
- **Environment.** Deployment manifests set the old variable names. Either
  the old names keep working (map them in `internal/config`, with a test per
  name) or the report hands the user an exact old-to-new table. The default
  is to keep them working.
- **Importers.** If other modules import this module's packages, moving them
  breaks those builds. Find out before moving (`pkg/` directories, a
  non-`internal` layout, a published module path) and ask.

## Handling a difference

When a skeleton convention and the old behavior disagree:

1. Record it in the plan's table of differences with both behaviors.
2. Default to the old behavior, and adapt the skeleton part: its options,
   its config, or its code in `internal/server` and `internal/platform`,
   which belong to the project once copied.
3. Offer the skeleton's behavior as the alternative when it is a real
   improvement, with what it would break and for whom.
4. Change behavior only for the rows the user chose, each in its own commit
   after the structural work, with the characterization test updated in that
   same commit so the change is visible in the diff of the test.
