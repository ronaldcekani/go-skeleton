# Moving code: where things go and how to move them safely

## Moving without rewriting

Business logic travels by cut and paste. A function arrives in its new file
with the same statements in the same order, and only these edits:

- its package, receiver and file;
- names the plan renames (`CreateItemInput` becomes `item.CreateInput`);
- how it reaches a dependency (a global `db` becomes `s.store`, a parameter
  becomes a field);
- how it constructs an error, when the client sees the same result as before;
- a tracing span or log line added around it, never one removed or renamed.

Everything else is a behavior change, however harmless it looks. These are
the ones that slip through because they feel like tidying:

- reordering statements, merging or splitting branches, simplifying a condition;
- changing when something happens relative to a commit, a response, or
  another side effect;
- `nil` slice or map versus empty (`null` versus `[]` in JSON), pointer versus
  value (`omitempty`, zero versus absent);
- numeric types and rounding (`int` to `int64`, float to decimal, integer
  division, where rounding happens);
- time: zone, precision, `time.Now()` taken once versus twice, format strings;
- strings: trimming, case folding, bytes versus characters, collation;
- defaults and limits, including ones applied by a framework or a struct tag
  (`binding:"required"`, `default:"20"`, `validate:"max=100"`);
- map iteration, sort order and tie-breaking, pagination arithmetic;
- error wrapping that changes what `errors.Is` matches or what the client reads;
- which errors are swallowed, logged, retried or returned;
- replacing a library call with an "equivalent" one;
- concurrency: what ran in a goroutine, what was awaited, what was locked.

If the old code is wrong, move the bug and write it down for the report. A
fix is the user's decision and a separate commit after the refactoring, when
the tests can show it is the only thing that changed.

`refactorcheck diff` prints old against new for every declaration the ledger
marks as moved. Read every changed line of it for each slice; a line that is
not one of the mechanical edits above goes back to how it was.

## Where things go

| In the old project | Goes to |
| ------------------ | ------- |
| Controllers, handlers, resolvers | `feature/<x>/http.go`, `grpc.go`: the translation only. Everything else they did moves to the service. |
| Services, use cases, managers | `feature/<x>/service.go`; rules without I/O into a file of their own |
| Models, entities | `feature/<x>/<x>.go` without tags; the tags move to row, cache, event structs |
| Repositories, DAOs, query builders | `feature/<x>/store.go` |
| DTOs, request and response structs | generated from the API definitions once the transport is spec-first; until then unexported, in `http.go` |
| Validation helpers, binding tags | the input's `Validate()` when the rule needs only the input, the service when it needs state |
| Router setup, middleware, interceptors | `server/httpserver`, `server/grpcserver`; auth and rate limits into `server/policy` rules |
| Clients for databases, caches, brokers, other services | `platform/<client>`, with a feature-side interface and, where types need translating, a small adapter file in the feature |
| `main`, DI modules, `init()`, global variables | `application/app/wire_*.go` and `cmd/app/main.go` |
| Background jobs, schedulers, consumers | a `component` returned from a `wire_*.go` file; the handler lives in its feature |
| CLI subcommands, one-off scripts | operation modes under `application/` |
| Config structs, env parsing | `internal/config`; values reach features as constructor arguments |
| Custom error types, error code tables | `domain` kinds when they classify what the caller can do; feature-level sentinel errors otherwise |
| `utils`, `common`, `helpers` | see "Shared helpers" below |

## Choosing features

A feature is a business capability with its own use cases, not a table and
not a layer. Evidence to weigh, in this order: the groups of endpoints in the
API (a resource, a proto service), which tables are written together in one
transaction, and which code changes together in the history
(`git log --name-only`).

- Code that writes the same tables in the same transactions is one feature.
- A feature that only reads another's data is still its own feature, with its
  own query for its own view of that data, as `order` reads customers.
- Two features that call each other in both directions are usually one
  feature, or hide a third one that both depend on.
- Prefer too few features to too many: splitting a package later is
  mechanical, while a wrong boundary forces interfaces and adapters through
  the middle of a use case.

## Recipes for the hard cases

**Fat handlers.** Split the handler body into three parts in place first,
without moving anything out: decode the request into an input struct, do the
work, encode the result. Run the tests. Then move the middle part into a
service method, verbatim. Validation that came from binding tags or the
framework has to be reproduced explicitly, with the same messages if clients
see them; list each one in the inventory so none is forgotten.

**Logic in unexpected places.** Before moving a feature, search for rules
outside its service layer: ORM hooks (`BeforeSave`, `AfterFind`), model
methods, custom `MarshalJSON` / `UnmarshalJSON` / `Scan` / `Value`, middleware
that only applies to some routes, SQL (defaults, `COALESCE`, `ON CONFLICT`,
triggers, views), repository methods that filter or default, template
functions, and `init()`. Each becomes an inventory line with a destination.

**Tagged and ORM models.** The entity keeps the fields and loses the tags.
The row struct in `store.go` keeps the storage tags and, if an ORM stays, the
ORM's model behavior, hooks included. Write the two mappers and add them to
`mapping_test.go` in the same commit. When several formats shared one struct,
check that each new struct carries the same names and options the shared one
had: `refactorcheck` reports every `key:"value"` tag pair that no longer
exists.

**Errors.** List every place the old code decides what the client sees: a
status written in a handler, a framework error, an error-code table, a
type switch in middleware. For each, pick the `domain` kind whose translation
gives the same response, and move the message unchanged. Where the old API
uses statuses or body fields the kinds cannot express (422, a numeric error
code, field-level details), extend the kernel and both translators in the
smallest way that reproduces the old response; add a kind as the README
describes rather than bending an existing one. Old responses that expose
internal error text are frozen behavior too: keep them, and raise it with the
user as a finding.

**Transactions.** Keep every boundary exactly where it is: the same
statements inside, the same ones outside, the same isolation level. The
mechanism changes (a `Transactor` in the service, stores using
`Conn(ctx, db)`), the scope does not. Do not add a transaction around code
that had none, and do not move a cache write, an event or an outside call
across a commit, even though the skeleton's own examples publish after the
commit: if the old code published inside the transaction, that is today's
failure behavior, and changing it is a decision for the user.

**Dependencies between features.** For each call from feature A into B's
code: A declares an interface with only the methods it uses, in A's types;
the wiring passes B's service, or an adapter defined in
`application/app` when the types differ. For a cycle, pass the dependency
after construction or reconsider the boundary. Shared database access is not
a dependency between features: each has its own store.

**Shared helpers.** Sort every helper by what it knows. Knows nothing about
the business and wraps infrastructure: a `platform` package. Knows nothing
about the business and is a few lines: copy it, unexported, into each feature
that uses it. Carries a business rule used by one feature: that feature.
Carries a business rule used by several: decide which feature owns the rule
and let the others receive the result or ask through an interface. `domain`
is the answer only for something every feature would define identically. A
new `internal/util` is never the answer; the architecture test rejects it.

**Globals, singletons, `init()`.** Each becomes a value constructed in a
`wire_*.go` file and passed to whoever used the global. Do this per feature
as it moves, not all at once. Keep the initialization order the old program
had, and keep side effects of `init()` (registering drivers, metrics, codecs,
seeding randomness) as explicit calls in the wiring.

**DI containers.** Read the provider graph to recover the construction order
and every lifecycle hook, then write it out as plain constructor calls.
`OnStart` hooks become components or steps in `Build`; `OnStop` hooks become
cleanup steps, pushed in an order that reproduces the old shutdown order.

**Jobs, schedulers, consumers.** The schedule, the consumer group, the
acknowledgement and retry behavior and the concurrency are frozen. The loop
becomes a `component`; its `stop` reproduces what the old shutdown did for it
(finish the current item, flush, commit). If the skeleton's `platform/kafka`
consumer does not behave like the old consumer (it skips a record whose
handler fails, and starts from the beginning for a new group), extend the
platform package to match the old behavior instead of adopting the new one.

**Clients of other services.** The generic client (base URL, auth, retries,
instrumentation) is a `platform` package. The feature declares an interface
for what it needs in its own types. The translation between the two lives in
the feature, in a file named for the dependency. Timeouts, retries and
headers are frozen.

**Tests.** Old tests move with the code they test and keep their assertions;
only setup and names change. A test that no longer compiles because it tested
through a layer that is gone is rewritten to assert the same facts through
the new entry point, and the ledger line for it says so. Never delete a
failing test, weaken an assertion, add a skip, or replace exact comparisons
with loose ones to get a green run: a red test after a move means the move
changed something.
