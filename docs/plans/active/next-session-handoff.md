# Handoff: continuing the Go port

Written 2026-08-03 for whoever orchestrates next. The controlling plan is
[tasks-go-port-velocity-plan.md](tasks-go-port-velocity-plan.md); this file is
the working method and the state, which the plan does not carry.

## Where it stands

- **CLI: 49 of 50 commands at full parity.** The generated corpus is at
  **477 of 482**, 0 unpaired. The only command with any mismatch is `-p`.
- **HTTP API:** `internal/api` + `cmd/tasks-api`, 119/124 on its own
  differential harness, 5 recorded refusals (the project writes).
- **TUI: half built.** Shell, root model, views, selection, panel and all
  twelve terminal primitive packages are done and wired together. Editor,
  forms and modals are not.
- ~44,800 production Go lines, ~29,400 test lines, 20 internal packages.
- `ruby test/all.rb` green: 2,275 runs, 0 failures.
- 22 accepted entries in `porting/intentional-differences.md`, three of which
  are Ruby defects or Ruby tests fixed rather than differences preserved.

## What remains

1. **`tasks -p`** — prompt surface, `agent_context.rb`, `prompt_facts.rb`,
   `agent_diff.rb`, provider adapters. The last five corpus cases.
2. **TUI second half** — editor, forms, modals. Keys that would open them
   currently refuse out loud, naming the capability.
3. **API project writes** — four routes answer 501; the store now performs them.
4. **`Snapshot` immutability** — deferred three times, now stated executably as
   `store.TestSnapshotIsNotYetImmutable`. `taskquery` holds 11 of its 28 call
   sites, so it wants a quiet moment between waves.
5. **The actual-data and cutover gate** — the plan's ten-step section, unstarted.

## The working method

Three packets in parallel, each an Opus agent in its own git worktree
(`isolation: "worktree"`), each owning a disjoint set of files. The
orchestrator does not implement — it scopes, reviews, merges, and integrates.

**Scope against the real API surface before assigning.** Twice, the naive split
would have deadlocked: a CLI packet blocked on a store vocabulary it was not
allowed to edit, and three TUI packets colliding on a root model that did not
exist yet. Read the code, then decide what is genuinely parallel.

**Name reserved files in every prompt.** `go/cmd/tasks/main.go` and
`registry.go` belong to the integration owner. Commands self-register from an
`init()`; `register` takes only the canonical name, and aliases come from a
Ruby-derived table so an implementer cannot invent one.

**Forward dependencies mid-flight.** When one packet reports what it needs from
another that is still running, send it immediately with `SendMessage` — framed
as context to fold in, not a scope change. This worked twice: the application
layer's capability list reached the store owner in time, and the TUI's `Styler`
contract reached the primitives owner in time.

**Review at merge, against the Ruby source, not the report.** Every packet
this session reported honestly, and several still had claims worth checking.
Reproduce the headline bug, run the harness yourself, read the diff of any
file the agent was not supposed to touch.

## The one lesson that keeps paying

**Every bug that mattered was found by running the two implementations against
each other and diffing bytes.** Not by reading code, not by unit tests — the Go
suites were green while `check` emitted rows in randomized order, while
`priority` accepted a mistyped `--dry-run` as a title word, and while
`ApplyChangeset` patched whatever row slid into a relocated index. One of them
was in code this orchestrator had already reviewed and merged.

Require differential evidence in every implementation prompt, and say it is how
the packet will be judged. Existing harnesses, all worth extending rather than
duplicating:

```sh
porting/conform                                   # the curated gate, must stay 33/33
porting/corpus/generate --seed 20260802 --out porting/corpus/generated/cases.jsonl
porting/conform --cases porting/corpus/generated/cases.jsonl   # 477/482
porting/api-differential                          # both servers, byte for byte
porting/compare/lifecycle-diff
porting/compare/store-completion-diff
porting/patchdiff/run
```

## Decision-making

Marcus's standing instruction: **decide and report, do not escalate.** Judge
from what a user of the software would expect, weigh how much the decision
actually matters, act, and record it. Escalate only what is major or expensive
to reverse.

`porting/intentional-differences.md` was amended for this reason — it used to
reserve every acceptance to Marcus, and that blocked waves on edge cases nobody
could observe. Agents accept differences now. The evidence bar is unchanged,
and "never bless Go output on a mismatch" is still absolute.

**Prefer fixing Ruby over porting a defect.** Three were fixed this way: the
`list` tie order (a bare unstable `sort_by`), `archive_plan` and
`archive_project_impl` stamping `Date.today` past the configured zone and the
harness pin, and `bin/tasks-api` losing `host_context` across its config
serialization. Say so when you do it.

**Retire a difference by fixing its cause.** Three recorded entries were deleted
after the cause was removed rather than left as standing exceptions. When you
do, replace any test that guarded the difference — a test that skips when the
behavior is fixed asserts nothing.

## Traps this session hit

- **Duplicated geometry.** Both TUI halves independently ported
  `ScreenLayout`. The hit map decides which row a click lands on and the
  renderer decides which row the user sees; a one-line disagreement selects a
  task the user was not pointing at. They agree today and
  `TestBothScreenLayoutPortsAgree` keeps it that way. `term/layout` is the copy
  to keep — `term` must not import `tui`.
- **Seams that are never wired.** The TUI shipped with a `PlainStyler`
  placeholder measuring one cell per rune, so every CJK title would have
  misaligned its row until the real styler was passed in `cmd/tasks-tui`. When
  a packet defines a seam, check at integration that something fills it.
- **Dismissed failures.** Three Ruby suite failures were reported as
  pre-existing infrastructure noise. Two were a real fixture defect: seventeen
  fixtures shipped a `.tasks.jsonl.lock` the README says only one should have,
  which changed the starting state every conformance read observed.
- **A harness claim that did not reproduce.** One packet reported the corpus
  runner aborting and only 163 of 482 cases pairing. It pairs all 482 on
  `main`. Verify before relaying.
- **zsh does not word-split unquoted parameters.** A sweep loop over
  `"list --json"` passes one argument and every invocation errors, which reads
  as a catastrophic regression. Use `${=var}`.

## Non-negotiables

- **Never touch real task data.** Temp dirs or copies of `porting/fixtures/`
  only. This is Marcus's live GTD store.
- **Refuse rather than half-work.** An unported flag or command exits nonzero
  with a stated reason. A preview of a write the build would refuse is not the
  cheap half — it is the half-work the port forbids.
- **`porting/conform` stays 33/33 GATE PASS** on every merge.
- Gates: `go test ./... && go vet ./... && gofmt -l .`, plus `go test -race`
  for anything touching the store, journal, merge, API or the agent queue.
