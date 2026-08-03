# Tasks Ruby-to-Go port: velocity plan

- **Status:** accepted and controlling — Wave 0 complete, Wave 1 ready to start
- **Accepted:** 2026-08-03
- **Last progress:** 2026-08-03 (Wave 0)
- **Scope:** finish the macOS Go application, prove it against copied real data,
  and cut over safely
- **Supersedes:**
  [tasks-go-port-plan.md](../deprecated/tasks-go-port-plan.md) as the delivery
  plan and
  [tasks-go-port-fleet-ops.md](../deprecated/tasks-go-port-fleet-ops.md) as the
  active working method

This is the plan agents should follow when continuing the Ruby-to-Go port. The
old manifest, fleet loop, evidence bundles, and language-porting playbook remain
useful historical material, but they do not control the work.

## Decision

Finish the port as an ordinary software implementation project. Assign a small
number of substantial Ruby source-and-test groups to Sonnet or Terra agents.
Each agent writes production Go and the corresponding Go tests in the same
change. Opus or Sol independently reviews the completed packet once, followed
by fixes and a focused re-check of the findings.

Do not restart `porting/loop.sh` unchanged. Keep `porting/STOP` in place. Do not
expand the 144-slice manifest, generate per-slice evidence bundles, or spend
agent turns maintaining the loop, landing system, or claim lifecycle.

The port is for one current user with important personal task data. Correctness
at the persistence boundary matters. Enterprise load work, exhaustive platform
certification, and a general-purpose porting system do not.

## Why the process changed

The fleet was optimized for assurance per tiny behavior, not for completing the
application. The 2026-08-03 audit found:

- 191 agent ticks started and 182 completed over roughly 9.8 agent-hours;
- 7 of 144 manifest slices reached a terminal state;
- work through the last loop-landed slice added about 6,880 Go lines and 22,056
  evidence lines, alongside roughly 9,917 lines of control-plane code;
- a focused post-loop burst added about 11,000 Go lines in roughly two hours;
- the current tree contains 14,978 production Go lines and 3,021 test lines;
- `go test`, `go vet`, and the live 33/33 Phase 1 conformance run pass; and
- only 11 of 50 canonical commands currently dispatch, while the Go CLI adapter
  has no direct Go tests and several core packages have no direct coverage.

The manifest is therefore no longer a useful progress measure. Progress is
measured by product source ported, product tests covered, working commands and
surfaces, and independent review.

## Scope and priorities

The first cutover target is the macOS product Marcus uses:

1. data model, JSONL store, journal, and merge behavior;
2. complete CLI and structured JSON output;
3. HTTP API and agent surface;
4. standalone Bubble Tea TUI; and
5. copied-real-data verification and reversible cutover.

The CLI may enter a canary before the TUI and API are complete. Ruby is not
retired until every owned surface that remains part of the product has a Go
implementation or an explicit product decision removes it.

Keep the schema-v2 JSONL format for cutover. The Go canonical writer already
exists, byte compatibility keeps rollback cheap, and a format change would add
migration work without helping the port finish. A later format change is a
separate project with its own migration and rollback path.

Defer Windows certification, mobile bindings, sync, and sidecar embedding until
after the macOS application has cut over. Do not shape port packets around those
future projects.

## Agent working contract

Use up to three implementation worktrees in parallel. Every packet assignment
must name:

- the Ruby source files to port;
- the Ruby test files to mirror;
- the Go packages or files the agent owns;
- the commands or user behavior that must work; and
- any shared files reserved for the integration owner.

The implementation agent must:

1. read the assigned Ruby source and tests;
2. implement the complete assigned behavior in production Go;
3. translate every distinct Ruby test invariant into Go, preserving recognizable
   test or table-case names;
4. add tests for user-expected behavior that Ruby does not cover;
5. run the focused Ruby tests and focused Go tests;
6. run `go test ./...` and `go vet ./...`; and
7. commit one coherent packet with a short list of any genuine remaining gaps.

Implementation agents do not update `porting/manifest.jsonl`, add oracle JSON,
write evidence reports, manage old porting-slice td issues, or touch real task
data. A packet should spend most of its time and changed lines in `.go` and
`_test.go` files.

One integration owner controls collision-heavy files such as
`go/cmd/tasks/main.go`, the command registry, and shared store interfaces.
Implementers add command or package files without independently rewiring the
dispatcher.

Every packet receives independent review by Opus or Sol. The reviewer compares
the Ruby source and tests with the Go diff, runs the relevant tests, and checks
for omitted behavior, unsafe data assumptions, self-fulfilling tests, and poor
Go boundaries. The implementer fixes the findings; the reviewer re-checks those
findings. Do not create separate oracle-capture, source-fidelity, Go-idiom, and
approval reviews for the same packet.

## Wave 0: stabilize the base — COMPLETE 2026-08-03

Do not merge stale branches wholesale. They were built against older versions
of main and contain large evidence bundles mixed with useful Go work.

What was done:

- **Reviewed and published the delta.** 131 commits were unpublished. The
  review checked for real store data, secrets, and out-of-tree paths and found
  none — every `tasks.jsonl` in the delta is a synthetic porting fixture.
  `main` is now level with `origin/main`.
- **`port/check-task-fields`: superseded, invariants salvaged.** Its `task.go`
  was overtaken by main's `validate.go`, which covers every rule it ported plus
  lead, temporal, and delegation, and validates recur through the shared
  `recur` package instead of a private copy. What main lacked was the
  vocabularies characterized as ranges rather than as the few values the
  fixture oracles hold, so four property tests came across against main's API
  (`go/internal/check/validate_test.go`).
- **`port/config-resolution`: retested, not merged.** Its tests targeted a
  `Resolve(Options)` signature this package no longer has. The precedence
  ladder is now tested against the current API
  (`go/internal/config/config_test.go`), which took `config` from zero tests.
- **`port/store-snapshot-items`: declined.** Its `isodate.go` reimplements
  `Date.iso8601`'s full grammar including the Julian calendar before 1582 —
  roughly 450 lines for dates no store can contain, and exactly the work this
  plan says not to do. Main's strict `YYYY-MM-DD` is the pragmatic behavior.
  Its read-model findings are carried to Wave 2 below.
- **Retired the stale branches and worktrees.** All six branches were tagged
  `retired/<name>` before deletion, so nothing is unrecoverable. The two
  `tasks-port-slots*` worktrees and the merged `tasks-corpus` worktree are
  gone; `/Users/marcus/code/tasks` on `main` is the only checkout.
- **Removed the dispatcher collision point.** `go/cmd/tasks/registry.go`
  replaces the switch in `main.go`: a command file registers its own canonical
  name, aliases, and handler from an `init`, so adding a command no longer
  edits an integration file. `canonicalCommands` is the full Ruby command set,
  so registering a command removes it from the unported-refusal list by
  construction rather than by care.

Gates at the end of Wave 0: `go build`, `go vet`, `gofmt -l`, and
`go test ./...` clean; `porting/conform` at 33/33 GATE PASS.

### Findings carried forward

Two things surfaced during salvage that later waves own.

**A defect fixed in passing.** `config.parseFile` read
`determinism.OSEnv()` instead of the caller's `env`, so a `~` or relative path
in the config file expanded against the process environment and ignored any
harness pin. Ruby expands these against `ENV`, so the two disagreed exactly
when a sandbox set `HOME` — which is also why `config` could not be tested in
isolation. Fixed by threading `env` through.

**A decision for Marcus, not yet made.** Ruby's `Time.iso8601` accepts an
`updated` stamp naming no real instant — `2026-02-30`, `24:00:00`, a leap
second — where Go's `time.Parse` refuses all three. Neither implementation can
write one, since both format from a real clock, so the difference is reachable
only by hand edit. It is pinned in a named Go test
(`TestUpdateStampRejectsInstantsRubyAccepts`) rather than accepted into
`porting/intentional-differences.md`, because that document reserves accepting
a difference to Marcus. **Decide before cutover:** the stamp also drives
last-write-wins ordering, so if a divergent stamp ever did exist, the two
implementations would order it differently.

**Deferred to the Wave 2 store owner.** `store.Snapshot` documents itself as an
immutable view but exposes `Items` and `ArchiveItems` as public slices, and the
`Item` values it hands out share tag backing arrays with the snapshot — so a
caller can corrupt a snapshot it only meant to read. `port/store-snapshot-items`
solved this with unexported fields and copying accessors, which is the right
shape. Fixing it touches 14 non-test call sites across `internal/store` and
`cmd/tasks`, both reserved files, so it belongs to the store owner rather than
to Wave 0.

## Wave 1: finish independent core seams

Run these three packets in parallel.

### Read model and queries

Ruby sources include `task_queries.rb`, `task_view.rb`, `tree.rb`, `links.rb`,
`quadrants.rb`, and `opener.rb`. Complete `next`, `inbox`, `quadrants`,
`projects`, `show`, `links`, `open`, and their JSON and human output. Port the
associated query, view, project, link, and CLI tests.

### Temporal and recurrence

Ruby sources include `dates.rb`, `temporal_*`, `timezones.rb`, `lead.rb`, and
`recur.rb`. Finish friendly date input, timed values, time zones, recurrence,
lead windows, availability, and their CLI behavior. The currently untested Go
temporal, lead, and time-zone packages need direct tests.

### Record, config, and check closeout

Ruby sources include `format.rb`, `check.rb`, `config.rb`, `determinism.rb`,
`atomic.rb`, update stamps, and delegation record rules. Much of the Go code
already exists; port missing tests, remove probe-only gaps, and make these
packages dependable foundations for the write wave.

Wave 0 left this packet a head start: `check` has its vocabulary properties and
`config` has its path-precedence ladder. What `config` still needs from
`test/test_config.rb` is urgent days, max depth, timezone resolution and the
fallback warning, date order, theme and colors, mouse, time format, and host
contexts. `determinism` and `updatestamp` remain untested.

Integrate the three packets, run the full Go suite, and complete one independent
review of the integrated wave.

## Wave 2: complete writes and application behavior

`lib/tasks/store.rb` is the main collision point. Give all `go/internal/store`
changes to one owner during this wave.

### Store owner

Complete capture, patching, placement, lifecycle, archive, repair, revisions,
snapshots, post-write validation, and rollback. Port the store, store-patch,
repair, changeset, placement, project-lifecycle, and archive tests.

### Application owner

Port `application.rb` and the create, delete, proposal, delegation, placement,
and operation objects. Add application-level tests without duplicating store
rules in the adapter.

### CLI mutation owner

Build commands in separate files for metadata edits, dates and state, hierarchy,
lifecycle, proposals, and delegation. Translate `test_cli_mutations.rb` into
black-box or table-driven Go CLI tests. The integration owner wires dispatch.

This wave must pass focused fault and rollback tests and `go test -race` for the
persistence packages before independent review.

## Wave 3: finish non-TUI surfaces

Use substantial packets for:

- journal interoperability, real undo and redo, archive, and JSONL merge;
- all 50 canonical CLI commands, aliases, help, structured output, dry-run
  behavior, errors, and exit codes;
- the HTTP API, using `docs/api/openapi.yaml` and the 108 Ruby API tests; and
- `tasks -p`, provider adapters, and agent queue behavior that remain part of
  the product.

Run the generated 482-case corpus once as a wave-level CLI gate. It is a batch
verification tool, not 482 work items. Review each substantial packet once and
then review the integrated surface for parity.

## Wave 4: rebuild the TUI

The TUI is a separate implementation wave because it is the largest remaining
surface and uses a new framework. Split ownership into:

- shell, navigation, views, selection, and the root model;
- editor, forms, and modals; and
- rendering, themes, shortcuts, input, mouse, clipboard, and agent activity.

Only one owner edits the root model. Preserve the interaction contracts Marcus
uses: stable selection across refreshes, save on blur, editor focus and escape
behavior, task details, project and archive actions, FIFO agent requests, mouse
hit testing, wide characters, responsive widths, themes, and `NO_COLOR`.

Use deterministic model tests and fixed-size rendered fixtures. Do not port the
Ruby terminal event loop or demand pixel identity where Bubble Tea provides a
different but correct rendering.

## Test parity and quality gates

The goal is behavioral coverage, not a gamed global percentage. Every product
Ruby test gets one of four dispositions:

- translated directly;
- covered by a named Go table, property, integration, or black-box test;
- obsolete because it tests Ruby internals or discarded porting machinery; or
- intentionally changed, with a Go test naming the new behavior.

Keep this mapping in one short Markdown checklist organized by Ruby test file.
Do not create a JSON record per test. The 108 tests for temporary porting
infrastructure are not part of Go product parity.

The standard packet gate is:

```sh
ruby test/test_relevant_area.rb
cd go && go test ./...
cd go && go vet ./...
```

Add `go test -race` for persistence, concurrency, journal, API cross-process,
and other shared-state work. Retain proportionate fault injection for atomic
replacement, invalid-store refusal, rollback, concurrent writers, and
cross-language undo/redo. Do not require enterprise load tests or exhaustive
fault matrices for every command.

Keep the existing fixtures and a small curated `porting/conform` smoke gate.
Run broader differential conformance at wave boundaries and before cutover,
not after every source file.

## Actual-data and cutover gate

No implementation agent touches the live store. After the code packets and
independent reviews are complete:

1. back up and checksum `tasks.jsonl`, `archive.jsonl`, and the journal;
2. run the Ruby structural checks on the real data;
3. copy the real data into two isolated directories;
4. compare Ruby and Go reads for Inbox, Next, agenda, filters, projects, show,
   recurrence, delegation, and other daily views;
5. independently replay capture, title/note/tag/context edits, dates, priority,
   state, move/subtask, recurrence/lead, proposals/delegation, done/archive, and
   undo/redo against the two copies;
6. confirm Ruby reads Go-written data, Go reads Ruby-written data, and both
   implementations validate both stores;
7. exercise the Go CLI, API, and TUI against another fresh copy and inspect the
   output manually;
8. run the full Go, Ruby, race, conformance, and API suites;
9. have Sol or Opus perform the final data-safety and cutover review; and
10. rehearse rollback against data last written by Go.

At cutover, take a fresh backup, switch only the installed executable or
wrapper, perform one real mutation, and verify it from both binaries. Keep the
Ruby executables available during the compatibility window. Never dual-write a
live store. Delete Ruby only after normal use has exercised Go-written data and
rollback is no longer needed.

## What counts as complete

The port is ready for default use when:

- all retained CLI commands and aliases work with human and structured output;
- Go tests cover every retained Ruby product-test invariant;
- store, journal, merge, API, and TUI behavior pass their focused suites;
- every substantive Go packet has independent approval;
- copied real data survives the complete workflow and cross-language checks;
- the installed Go application passes a bounded canary; and
- rollback from Go-written data has been demonstrated.

The old manifest does not need to reach 144/144. The porting control plane does
not need to become a reusable product. Working Go code, executable tests,
independent review, and a safe cutover are the deliverables.
