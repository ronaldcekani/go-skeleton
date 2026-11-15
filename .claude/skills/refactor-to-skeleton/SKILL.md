---
name: refactor-to-skeleton
description: Migrate an existing Go service onto the go-skeleton architecture (feature slices under internal/feature, a shared kernel in internal/domain, platform clients, generated HTTP and gRPC transports, explicit wiring and lifecycle) and prove that no business logic or observable behavior was lost. Use when asked to refactor, restructure, migrate or port a project to go-skeleton or "the skeleton architecture".
argument-hint: "[path or git URL of go-skeleton]"
---

# Refactor a project onto go-skeleton

You are moving an existing, working Go service onto the architecture of the
go-skeleton repository. Two things have to be true at the end, in this order
of priority:

1. The service behaves exactly as it did, for every client, consumer,
   database, deployment and dashboard that touches it.
2. The code has the skeleton's shape and passes the skeleton's own checks.

When the two conflict, behavior wins and the conflict goes to the user.

Refactorings lose logic quietly: a validation that lived in a handler, a
default applied by a framework tag, a `WHERE` clause, an error that used to
be a 409, a consumer group name. None of these fail to compile, and reading
your own work does not find them, because you read what you meant to write.
So this process does not rely on care. Every claim that nothing was lost is
backed by something that can fail: a ledger that accounts for every
declaration, literal, test and file of the old code; tests recorded from the
old code's real behavior; the old tests, still passing; the skeleton's
architecture and mapper tests; and a review by someone who did not do the
work. Follow the steps in order: each produces what the next one checks
against.

Skeleton location passed as argument (may be empty): $ARGUMENTS

## Before you start

**Find the skeleton.** You need a checkout of go-skeleton to read and copy
from. Use the first that exists: the path or URL in the argument above; the
`GO_SKELETON_DIR` environment variable; the repository this skill lives in
(resolve `${CLAUDE_SKILL_DIR}` with `realpath` and look three directories up
for `internal/architecture/architecture_test.go`). Clone a URL into a
directory outside the project. If none works, ask the user for it. Note the
skeleton's commit; the plan records it.

**Read the skeleton, in full**: its `README.md`,
`internal/architecture/architecture_test.go`, every file of
`internal/feature/item` and `internal/feature/order`, and
`internal/application/app`. Then read the three references next to this file:

- [reference/target.md](reference/target.md): what the finished project
  looks like and how to adapt the skeleton to a different stack
- [reference/moving.md](reference/moving.md): where each kind of old code
  goes and how to move it without changing it
- [reference/contracts.md](reference/contracts.md): the behavior that is
  frozen, and where the skeleton's defaults would change it by accident

**Check that this applies.** The project must be a Go module that builds. For
a project in another language this would be a rewrite, where none of the
proofs below hold: say so and stop. In a repository with several services, do
one service at a time, each with its own state directory.

**Resume if work is in flight.** If `.refactor/` exists in the project, read
`.refactor/progress.md`, `.refactor/plan.md` and `.refactor/inventory.md`
first and continue where they stop. This work outlasts a context window;
those files are what the next context knows. Update `progress.md` after every
slice: what is done, what is in flight, what you learned that the plan did
not expect.

**The tool.** `${CLAUDE_SKILL_DIR}/tools/refactorcheck.go` is a single-file
program run with `go run` from the project root. Running it without
arguments prints its usage and the ledger format.

## Rules that hold throughout

- **Move code, do not rewrite it.** Logic travels by cut and paste with
  mechanical edits only; `reference/moving.md` lists what counts as
  mechanical. A function you re-implement from your understanding of it is a
  new function with new bugs.
- **Structure and behavior never change in the same commit.** If the old
  code has a bug, move the bug and report it.
- **A red test means the new code is wrong.** Never delete, skip or loosen a
  test to get a green run, and never edit a recorded expectation except for a
  behavior change the user chose.
- **Nothing disappears without a ledger line.** Deleting old code and
  explaining where it went are one action.
- **Keep the service runnable at every commit.** There is one application
  the whole time; each commit changes how it is built inside, and the full
  set of checks runs before the next one.
- **Stay inside the working tree.** Use local, throwaway databases and
  brokers only. Do not run migrations, publish messages or call anything
  against shared or production systems, and do not deploy.
- **Decisions about behavior belong to the user.** You decide structure. You
  ask about anything a client, an operator or another system could notice.

## Step 1: Baseline

Start from a clean working tree on a branch for this work.

1. Build, vet and run every test the project has, the way its CI does, and
   save the output to `.refactor/baseline-tests.txt`. Bring up the
   project's local dependencies (its compose file, its Makefile) so that
   tests needing a database or broker run instead of skipping; a skipped test
   proves nothing. Tests that already fail are recorded, not fixed. If the
   project does not build, stop and tell the user.
2. Mark the starting point: `git tag refactor-baseline`. The old code stays
   readable for the rest of the work with `git show refactor-baseline:<path>`.
3. Take the snapshot and commit `.refactor/`:

   ```bash
   go run ${CLAUDE_SKILL_DIR}/tools/refactorcheck.go snapshot
   ```

The snapshot is taken once. It is the definition of "everything the old code
contained" that the final check is measured against.

## Step 2: Inventory

Write `.refactor/inventory.md`: every behavior the service has, each with an
ID and the `file:line` where it lives today. `reference/contracts.md` lists
what counts as behavior; go through it line by line.

Build the inventory from the outside in. Start at the places where entry
points are registered (router setup, gRPC registration, consumer
subscriptions, schedulers, CLI commands, `main`), because registration code
is the complete list of what the service does. Then follow each entry point
inward and record, in order: authentication and authorization, every
validation and where it is enforced, defaults, the business rules, each side
effect (database writes, cache, events, outside calls) with its position
relative to the transaction, and every outcome a caller can observe with its
status and body. Separately record config keys and defaults, startup and
shutdown behavior, and the metrics, spans and log lines something else
depends on.

Check the counts mechanically: the number of routes, RPC methods, consumers
and jobs in the inventory equals the number of registrations a search finds.

For a large service, list every entry point now and complete the detail for
each feature before that feature moves, never after.

## Step 3: Safety net

Pin the old behavior with tests before any code moves. These tests are the
yardstick for the whole refactoring, so they must not depend on anything that
will move: they talk to the service from outside.

- Prefer black-box tests against the running binary with real local
  dependencies: send the request, compare status, headers and body, then
  check what was written to the database, cache and broker. Otherwise test
  in-process at the transport boundary (the old router or gRPC server with
  `httptest` / `bufconn`). Put them in `test/characterization/`.
- Cover, for every entry point in the inventory: each success path, each
  validation rule from both sides of its limit, each error a caller can
  trigger, unauthenticated and unauthorized calls, defaults and limits of
  pagination and filtering, and the responses for unknown routes, wrong
  methods and oversized bodies. Consumers and jobs: feed the input, assert
  the effects.
- Record what the old code actually does, including what looks wrong, into
  golden files; replace only volatile values (generated IDs, timestamps) with
  placeholders. Record the exact bytes of every published event and cached
  value.
- Run the suite against the old code until it passes repeatedly, then measure
  what it and the existing tests execute (`go test -coverpkg=./... -coverprofile`,
  or a binary built with `go build -cover` for black-box runs). Business
  logic no test reaches gets a test now, or a line under "not verified by a
  test" in `progress.md` that ends up in the final report.

Commit the suite. From here on it runs after every slice, unchanged.

## Step 4: Plan and decisions

Write `.refactor/plan.md`:

- **Feature map**: every old package, type and entry point assigned to a
  feature, the shared kernel, or a platform, server or application package
  (`reference/moving.md` explains how to choose features).
- **Dependencies between features**, and the interface each consumer will
  declare.
- **Slices**: the order of work, each slice small enough to verify on its
  own. Start with the feature that has the fewest dependencies on the others.
- **Stack differences**: one row for every place the project's products and
  libraries differ from the skeleton's, with the default from the table in
  `reference/target.md` and your recommendation.
- **Behavior differences**: one row for every place a skeleton convention
  would change frozen behavior (`reference/contracts.md`), with both
  behaviors. The default is always to keep the old behavior.
- **Left out**: the skeleton parts this project does not need.
- **Findings**: bugs, dead code and risks you noticed. They are reported,
  not fixed.

Then stop and ask the user to decide the stack and behavior rows, showing
your recommendation for each, and to confirm the feature map. These are the
only decisions that are theirs rather than yours, and they are cheap now and
expensive after the code has moved. If nobody can answer, proceed with the
defaults (keep every behavior, make only the stack changes the table marks as
part of the architecture), record that in the plan, and repeat the open
decisions at the top of the final report.

## Step 5: Put the skeleton's frame around the old code

Goal: the application is started by the skeleton's wiring, and what it wires
is still the old code. No business code moves in this step.

1. Copy in the frame from the skeleton: `internal/domain`,
   `internal/architecture`, `internal/testkit`, the lifecycle of
   `internal/application/app` (`build.go`, `lifecycle.go` and their test),
   `cmd/app`, and the build files the plan calls for. Replace the module path
   `github.com/example/go-skeleton` everywhere, including the `module`
   constant in the architecture test. Do not copy the example features
   `item` and `order`, their API definitions, migrations, seed data or config
   rules, nor any part the plan leaves out.
2. Where an old package sits at a path the skeleton needs, move it aside
   first in a commit that does nothing else, and record it in the ledger.
3. Write the `wire_*.go` files for the old code, with the skeleton's as the
   pattern: move the old program's construction code (what `main` and its
   helpers build and start, in the order they do it) into them, each resource
   pushed on the cleanup stack, each server and background loop a
   `component`. Reproduce the old startup order and shutdown behavior;
   `reference/moving.md` covers globals, `init()` and DI containers. The
   frame needs `internal/config` and `internal/platform/logger` only for
   itself at this point (mode, shutdown, its own log lines): bring them in
   reduced to that, and let the old code keep loading its configuration and
   logging the way it does until step 7.
4. The old `main` becomes a call into this wiring. Its path, binary name and
   flags are frozen behavior: move it to `cmd/app` only together with every
   reference in the repository (Dockerfile, Makefile, CI), and keep the old
   flags working.
5. Old packages under `internal/` have no rule in the architecture test yet.
   Add a temporary entry for each to its `allowed` map, marked `// legacy`,
   and add them to the `application` entry so the wiring may import them.
   These entries are the to-do list: each disappears with its package, and
   none may remain at the end.

Run every check from the list in step 6. The characterization suite passing
here shows that the new lifecycle did not change behavior.

## Step 6: Move the features, one slice at a time

For each slice in the plan:

1. **Read first.** Re-read the slice's inventory lines and every old file
   involved, completely and from the files themselves, not from memory or
   from the plan. Search for logic outside the obvious layer (see "Logic in
   unexpected places" in `reference/moving.md`) and add what you find to the
   inventory.
2. **Move inside out**, compiling after each part: entity and inputs, store,
   cache, events, service, then the handlers reduced to translation, then the
   wiring. In this step the handlers keep the project's existing transport
   framework; only their logic leaves. Use the skeleton's `item` and `order`
   packages as the pattern for every file.
3. **Bring the tests along.** Port the old tests with the code, keeping their
   assertions. Add a `mapping_test.go` entry for every mapper, and a test
   that pins every stored or published format to bytes recorded from the old
   code.
4. **Delete the old code of the slice and account for it:**

   ```bash
   go run ${CLAUDE_SKILL_DIR}/tools/refactorcheck.go check -append
   ```

   This adds a `TODO` line to `.refactor/ledger.tsv` for everything from the
   snapshot that is no longer where it was. Fill in each line: `moved` with
   where the logic lives now, `replaced` when skeleton, generated or library
   code does the job instead, `dropped` when it is gone. `replaced` and
   `dropped` need a note with the evidence. Dropping something a caller could
   observe needs the user's approval, not a note. A missing literal is a
   question to answer, not a formality: a string or number that no longer
   exists anywhere is a message, a query, a key, a limit or a name that
   changed.
5. **Review what changed inside the moved code:**

   ```bash
   go run ${CLAUDE_SKILL_DIR}/tools/refactorcheck.go diff <part of the old package path>
   ```

   Read every changed line. Each must be one of the mechanical edits; put
   anything else back.
6. **Run every check**, and fix the new code until all pass:
   - `go build ./...`, `go vet ./...`, and the full test suite with the race
     detector and real local dependencies;
   - the characterization suite, unchanged;
   - `refactorcheck check` exits 0;
   - the legacy entry of every package that is now empty is removed from the
     architecture test.
7. **Commit the slice** and update `.refactor/progress.md`.

If a check still fails after a few focused attempts, throw the slice away
(`git restore`, `git clean`) and redo it in smaller pieces. Do not push a
half-understood failure forward into the next slice.

The last slice is the transport layer itself: router construction,
middleware and error handling move into `internal/server`. After it no legacy
package is left and the architecture test has no legacy entry.

## Step 7: Converge the stack, one swap at a time

The features now have the skeleton's shape but may still run on the old
project's libraries. For each stack row the user approved, in an order that
keeps each swap small, make the swap as its own commit series with the same
checks as a slice. The usual order:

1. **Transports.** Write `api/openapi/openapi.yaml` (and move the `.proto`
   files) to describe the existing API exactly as the characterization tests
   recorded it, generate, and reimplement the handlers on the generated
   interfaces. They only translate by now, which is what makes this swap
   mechanical. Bring in the skeleton's `internal/server` packages and adapt
   their error translation, validation and routing responses until the
   characterization suite passes unchanged.
2. **Per-endpoint policy.** A rule for every endpoint that reproduces today's
   authentication and limits.
3. **Config, logging, telemetry**, keeping every existing key, variable and
   consumed name working.
4. **Platform clients**, where the product matches the skeleton's.
5. **Migrations**, only with the continuity plan from
   `reference/contracts.md`.

Before each swap, read the row for it in the table "Where the skeleton's
defaults would change behavior". A swap the user declined stays as it is.

## Step 8: Prove it and report

1. Run the arrival checklist at the end of `reference/target.md`. Every line
   is checked by a command.
2. Run everything once more from a clean state: build, vet, all tests with
   real dependencies, the characterization suite, `refactorcheck check`.
   Compare the test run with `.refactor/baseline-tests.txt`: every test that
   passed at the baseline passes now under its new name from the ledger, and
   nothing that ran then is skipped now.
3. Get a review from a fresh context. If you can start a subagent, give one
   the baseline tag, the inventory, the ledger and this instruction: for
   every inventory item, find the code that implements it now and compare it
   with `git show refactor-baseline:<file>`; report anything missing,
   weakened, reordered or changed, and look specifically at the list of
   quiet changes in `reference/moving.md` and the traps in
   `reference/contracts.md`. It should try to find a loss, not confirm there
   is none. Without subagents, do the same pass yourself, working from the
   inventory rather than from your memory of the work. Fix what it finds and
   repeat until a pass finds nothing.
4. Write the report for the user, with the outcome first:
   - whether every check passes, with the commands and their results;
   - the decisions that were made, and any that are still open;
   - everything replaced or dropped (`refactorcheck report`);
   - every behavior difference that was introduced on purpose;
   - what no test verifies and has to be checked by hand, such as the
     migration path for existing databases or anything needing a real
     external system;
   - bugs and risks found in the old code and left as they were;
   - what has to change in deployment, if anything.

   State plainly what was not verified. A refactoring reported as clean with
   an untested corner is worse than one reported with the corner named.

`.refactor/` and the `refactor-baseline` tag are the audit trail. Leave them
in place for the user's review and say in the report that `.refactor/` can be
deleted before merging. Keep `test/characterization/`: it is now the
project's regression suite.
