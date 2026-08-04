# Tasks Ruby-to-Go port: velocity plan

- **Status:** implementation complete and independently reviewed — synthetic
  cutover gates pass; copied-real-data verification and the bounded canary remain
- **Accepted:** 2026-08-03
- **Last progress:** 2026-08-04 (all CLI/API/TUI code and Snapshot immutability)
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

### Wave 1 outcome — COMPLETE 2026-08-03

All three packets landed, were independently reviewed against the Ruby sources,
and are merged. Packages that had **zero** direct tests — `temporal`, `lead`,
`timezones`, `determinism`, `updatestamp` — now have 119 between them; `config`
went 13 → 75, `check` 20 → 71, `recur` 13 → 74, `record` 71 → 112, and
`links` plus a new black-box CLI harness added 103.

Seven read commands work with human and `--json` output: `next`, `inbox`,
`quadrants`, `projects`, `show`, `links`, `open`.

**The bugs that mattered were found by differential comparison against the Ruby
binary, not by reading code and not by unit tests.** Wave 2 should assume the
same and run comparisons early rather than at the wave boundary:

- `takeFlags` accepted an unknown `--flag` as a positional where
  `bin/tasks:1072` aborts — live in `check`, `priority` and `delegate`, so a
  mistyped `--dry-run` became a title word and performed the mutation it was
  meant to preview;
- the unsupported-schema gate reached no read command, so `list` answered from
  a v1 store as though it could read it;
- `crossFileDuplicates` walked a Go map, making `check`'s output order
  **randomized between runs**;
- `writeStore` built a fresh id mint per store, so a second store in one
  invocation would re-mint an id the first had used; and
- `list` disagreed with Ruby on 6 fixtures — see the oracle fix below.

Four intentional differences stand: `update-stamp-real-instants`,
`year-less-feb-29-rolls-to-the-next-real-one`,
`date-order-is-a-parameter-not-a-process-global`, `calendar-interval-beyond-int64`,
and `custom-link-system-order`. Two were recorded and then **retired** by fixing
the cause instead — the config timezone warning, and the list tie order.

**One Ruby oracle fix.** `cmd_list` sorted priority groups with a bare
unstable `sort_by`, so ties landed wherever introsort left them — deterministic
for one input, permuted by an unrelated row elsewhere in the file. Reproducing
that in Go meant porting `ruby_qsort` and pinning it to an MRI version to
preserve an ordering carrying no information, and it is a latent bug for Ruby
users regardless of the port. Fixed in `bin/tasks` with the stable idiom
`TaskQueries#stable_sort` already used. Verified across 864 comparisons — 4
clock pins x 18 fixtures x 12 invocations — now byte-identical.

**Known pre-existing failure, not caused by this wave:**
`test_cli_mutations.rb#test_cli_recur_preview_honors_count_and_json` fails
against unmodified `bin/tasks`. It expects a projection relative to the real
current date, so it is wall-clock dependent and fails on some days. Wave 2's
recurrence or CLI owner should pin its clock.

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

### Corpus baseline — 2026-08-03, start of Wave 2

Regenerate and run it with:

```sh
porting/corpus/generate --seed 20260802 --out porting/corpus/generated/cases.jsonl
porting/conform --cases porting/corpus/generated/cases.jsonl
```

At the start of Wave 2 this stood at **219 of 482 matching**. The number is a
progress metric, not a defect count: the mismatches are dominated by commands
and flags the Go build has not ported, and it refuses them honestly rather than
answering wrongly. Spot-checked to confirm that — `done --dry-run` prints
Ruby's preview and exits 0, while Go refuses and exits 1.

**Fully matching (12):** `agenda`, `check`, `config`, `inbox`, `links`, `list`,
`next`, `open`, `projects`, `quadrants`, `show`, plus the fixture probe.

**Partially matching (7)** — these are the ones worth attention, because the
command IS implemented and still diverges: `capture` 8/12, `done` 10/12,
`priority` 8/10, `propose` 6/8, `delegate` 5/7, `claim` 4/6, `undo` 2/5. Every
gap inspected so far is an unported *flag* within a ported command (`--dry-run`
is the most common), which the build refuses.

**Zero matching (31):** every remaining write command, plus `help`, `-p`,
`redo`, `id`, `repair`, `merge-driver` and the five `project` subcommands.

Re-run this at each wave boundary and record the new number here. A wave that
does not move it has not moved the product.

### Corpus after Wave 2 — 221 of 482

**Wave 2 moved the number by two.** That is not a failure of the wave; it is the
metric working. Wave 2 built the write path's *capability* — `PatchField` from 2
fields to 14, the recurrence roll, the changeset, targeted repair, the whole
application layer, JSONL merge — and almost none of it is reachable from the
command line yet, because the commands that would call it are not written.
`merge-driver` is the only new fully-matching command.

The corpus measures the **surface**, not the capability behind it. Both numbers
matter and they are not the same number, so record both:

- capability: `internal/store` patches 14 fields, `internal/application` and
  `internal/merge` exist, ~3,500 Go tests' worth of behavior;
- surface: 13 of 50 commands reach parity.

**This is what Wave 3 is for, and it should now be mostly cheap.** The 31
zero-matching commands are dominated by field patches the store already
performs — `due`, `schedule`, `undate`, `state`, `cancel`, `retitle`, `tag`,
`note`, `defer`, `someday`, `activate`, `lead`, `recur` are all `PatchField`
calls behind a command file that registers itself. The genuinely unbuilt ones
are `move`/`location` (needs placement), `delete`, `archive`, `repair`, `redo`,
the proposal verbs, the five `project` subcommands, `help`, and `-p`.

The seven partially-matching commands are unchanged from the Wave 2 baseline:
`capture` 8/12, `done` 10/12, `priority` 8/10, `propose` 6/8, `delegate` 5/7,
`claim` 4/6, `undo` 2/5 — still mostly `--dry-run`, which no ported command
implements.

### Corpus after Wave 3 — 393 of 482

Wave 3 moved the number by **172**, from 221 to 393, and took fully-matching
commands from 13 to **36 of 50**. This is the wave where the port became a
usable CLI rather than a read-only one.

The prediction in the Wave 2 section held exactly: the field-patch commands
were cheap and worth doing first. The CLI mutation packet alone accounted for
131 of the gain; history and lifecycle supplied the rest by making `undo`,
`redo`, `delete`, `archive`, `repair`, `approve` and `reject` reachable.

**Fully matching (36):** `activate`, `agenda`, `approve`, `archive`, `cancel`,
`check`, `config`, `defer`, `delete`, `done`, `due`, `help`, `inbox`, `lead`,
`links`, `list`, `merge-driver`, `next`, `note`, `open`, `priority`,
`projects`, `quadrants`, `recur`, `redo`, `reject`, `repair`, `retitle`,
`schedule`, `show`, `someday`, `state`, `tag`, `undate`, `undo`.

**Partial (4):** `capture` 8/12, `propose` 6/8 — every remaining case passes
`--due`/`--scheduled`/`--recur`/`--lead`/`--under`, which `store.CreateCommand`
cannot persist, so both the write and its preview refuse rather than half-work.
`delegate` 5/7 and `claim` 4/6 — delegation output, not previews: neither
command has a `--dry-run` in `bin/tasks` at all.

**Zero (11), and what each needs:**

- `move` — `location`/`TaskPlacement`, the largest remaining store gap;
- the five `project` subcommands — the project lifecycle store calls;
- `undelegate`, `release`, `workref` — store verbs the application layer
  already declares optional interfaces for;
- `id` — trivial, unclaimed;
- `-p` — the prompt surface and its provider adapters, deliberately deferred.

**Two corrections to the Wave 3 framing**, both found by reading `bin/tasks`
rather than assuming: `delegate` and `claim` accept no `--dry-run`, and
previewing a dated create is not "the cheap half" of a command whose write
refuses — it is the half-work this port forbids.

### Corpus after the store-completion packet — 477 of 482

The packet moved the number by **84**, from 393 to 477, and took fully-matching
commands from 36 to **49 of 50**. The CLI is closed.

**What landed:** `TaskPlacement` and the `location` field — the largest
remaining store gap — with the cycle, depth, proposed-parent and anchor-parent
rules and the UNNEST spelling; the project lifecycle (`create_section`,
`create_project`, `rename_section`, `complete_project`, `archive_project`,
`section_named`, `ensure_id`) behind `with_history`; the dated create
(`scheduled`, `deadline`, `recur`, `lead`, `--under`), which retired the
application layer's refusal; and the commands `move`, the five `project`
sub-verbs, `undelegate`, `release`, `workref`, `id`. `delegate --to` and
`claim --json` were finished in passing, since the composed-write pattern was
already there.

**Zero (1):** `-p` — the prompt surface and its provider adapters, still
deliberately deferred. Every one of the five remaining mismatches is a `-p`
case.

**Three defects the differential harness found that unit tests did not**, all
in code that had already been written and passed its own tests:

- `ApplyChangeset` cached the record index across the field loop. A `location`
  change sorts before `state` in FIELD_ORDER and physically relocates the row,
  so the next field patched whatever slid into the old slot.
- `Undelegate` and `SetWorkRef` honoured a coalesce key Ruby's store does not
  take, so two consecutive work-ref writes collapsed into one undo step and the
  first could not be restored.
- `delegate` and `claim` printed the task headline where Ruby prints the
  delegation line.

**One Ruby defect fixed rather than ported:** `archive_project_impl` stamped
`archived` from `Date.today`, the one date this store wrote that a harness pin
could not reach. See `porting/intentional-differences.md`.

**The Snapshot immutability defect is deferred a third time, and pinned.** It
is now stated executably as `store.TestSnapshotIsNotYetImmutable` rather than
only in prose. The reason is no longer Wave 2's: `taskquery` holds 11 of its 28
call sites and is the package the TUI worktrees read from, and the change
carries no differential evidence because none of it is visible from the CLI. It
should land as its own packet between waves, not inside one.

**A harness claim that did not reproduce.** One Wave 3 packet reported the
corpus runner aborting on `journal_key` and only 163 of 482 cases pairing. On
`main` the corpus pairs all 482 with zero unpaired, before and after that
packet merged. The note it added to this plan has been removed; no harness fix
is needed.

### Corpus after store completion — 477 of 482

**49 of 50 commands reach full parity.** The only command with any mismatch is
`-p`, whose five cases wait on the prompt surface and its provider adapters —
deliberately deferred.

Landed to get here: `TaskPlacement` and the `location` field with its cycle,
depth, proposed-parent and anchor-conflict rules; the project lifecycle and its
five subcommands; dated creates; and the commands `move`, `project`,
`undelegate`, `release`, `workref`, `id`.

The API differential improved in step, 115/124 to 119/124, as the store grew
the operations its 501s stood in for. The remaining refusals are the project
writes, which the API can now be taught since the store performs them.

### Implementation completion checkpoint — 2026-08-04

All retained product surfaces are implemented and independently reviewed:

- the CLI is 50/50 commands and the generated corpus is 482/482;
- `tasks -p`, prompt facts, transcript diffing, and provider adapters are 18/18;
- the HTTP API, including all four project writes, is 223/223; its five
  delegation-route 501s remain the accepted precondition-safety difference;
- the complete TUI is 77/77 on its interaction differential and 39/39 on the
  opener Shellwords differential; and
- `store.Snapshot` collections are private and deeply detached through copying
  accessors and tree results. The old corruption proof, focused positive tests,
  and a shallow-copy negative control all demonstrated the boundary.

The final synthetic rehearsal used only repository fixtures and temporary
stores. `lifecycle-diff` passed 39 cross-language write/undo scenarios,
`store-completion-diff` passed 54 scenarios diff-clean, `porting/conform`
remained 33/33, and the full Go, Ruby, race, API, and formatting gates passed.

The final data-safety review initially blocked on one corpus blind spot:
configured `host_context` was omitted by CLI `capture`/`propose`, while every
generated create case happened to pass `--no-host-context`. Commit `890b05c`
fixed the shared preparation path for mutation and dry-run, added black-box Go
coverage, and expanded the store differential with five configured-host
scenarios. The old tree fails those scenarios with six byte/state differences;
the final tree is 54/54 and received independent approval.

The sole remaining gate is the copied-real-data and bounded-canary procedure
below. It was not run because this session explicitly prohibited accessing,
copying, or mutating the real task store. That is a deliberate safety boundary,
not an implementation gap.

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
