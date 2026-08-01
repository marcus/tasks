# PORTING.md — the Go-port agent loop

> This is the prompt every porting tick reads. `porting/loop.sh` points each
> agent here; everything below the rule is read verbatim every iteration, so
> keep it lean — it is paid for on every tick. The runbook for the human is
> [OPERATING.md](OPERATING.md); the plan and method live in
> [`docs/plans/active/`](../docs/plans/active/).

---

You are one of many agents porting `tasks` from Ruby to Go. You start with no
memory of previous iterations. td and this repository *are* the memory: read
them, do one useful unit of work, record it, exit.

## Mission

A behavior-preserving port. Ruby is the oracle until cutover. The unit of
progress is a **proved behavior**, not a translated file. The full plan is in
`docs/plans/active/tasks-go-port-plan.md` and the method in
`docs/plans/active/language-porting-playbook.md` — consult the section your step
needs; do not re-read them front to back each iteration.

## Non-negotiables

- **Behavior-preserving only.** No format changes, no CLI redesign, no new
  features, no refactoring-while-translating. An improvement you notice is an
  intentional-difference record for Marcus to decide, never something you land.
- **Byte-compatible JSONL.** The Go writer emits byte-identical canonical
  output for every Ruby fixture. `encoding/json` cannot do this; the canonical
  emitter is the only writer.
- **Fixtures only.** You operate on copies under `porting/fixtures/`. Never
  touch a live store. Never dual-write. Ruby remains production throughout.
- **Never bless Go output.** On a conformance mismatch, classify it — Go
  defect / legacy Ruby rule we preserve / nondeterminism to inject or
  normalize / intentional difference (Marcus decides) / missing oracle
  coverage — and act on the classification. Updating an expected result
  because Go produced it is the one unforgivable move.
- **Green means green.** A slice lands compiling, gofmt/vet/lint clean,
  race-clean, with its risk tier's full evidence recorded.

## Where state lives

- **td (this repo)** — work state. One issue per slice; campaigns are epics.
  Claims, progress logs, handoffs, and reviews all happen here.
- **git** — proof. `porting/manifest.jsonl` defines slices (id, deps, risk,
  source_sha, fixtures, evidence locator); `porting/evidence/<slice-id>/`
  holds oracle captures, conformance reports, and review findings. td log
  entries *point at* evidence paths; evidence never exists only inside td.

## Your iteration

1. **Orient.** `td usage --new-session -q`. Prefer, in order: a slice in
   review you did not write; in-progress work handed off and unclaimed; the
   next ready slice whose manifest dependencies are green.
2. **Claim.** `td start <id>` is the atomic claim. If it fails, someone else
   has it — pick another. Assume sibling agents are running: never share a
   fixture copy, port, build dir, or store with them.
3. **Work one step** of the slice loop below — not necessarily the whole
   slice. Low-risk slices fit in one iteration; high-risk slices should span
   several agents on purpose, so the translator and the oracle-capturer and
   the reviewer are different contexts.
4. **Record.** `td log` what you proved and the evidence path. Update the
   manifest entry if its status changed. Commit small, reviewable changes.
5. **Hand off or conclude.** `td handoff <id>` stating what is proven, what
   is next, and the next step's risk tier — or move it to review. Then exit.

## The slice loop, scaled by risk

| Tier | Applies to | Required steps |
|---|---|---|
| **Low** | pure reads, formatting, help text | oracle capture → translate → compile → differential conformance; one combined review; self-approval allowed (`td approve --self-review --reason`) |
| **Medium** | semantics without persistence: temporal parsing, tree queries, config | low tier + property tests + split source-fidelity and Go-idiom reviews; independent approval |
| **High** | anything that writes: store, journal, locking, archive, merge | full loop: + fault injection at every write boundary, real competing processes, stress and budget checks; two independent reviews; independent approval only |

Three things are never skipped at any tier: the manifest entry, the Ruby
oracle capture, and differential conformance. Skipping anything else is
recorded in the manifest entry, not left silent.

Role boundaries: on medium/high slices, oracle capture and translation are
different agents; reviewers read and name exact divergences (file, function,
evidence, correction) and never edit; a disputed high-risk claim gets a third
review or a new executable test, not a vote.

## Review and approval

Reviews happen on a **later iteration by a different agent** claiming the
in-review issue — never a subagent of the writer, which inherits the writer's
framing. Approve from the reviewing session: `td approve <id> --reason "..."`.
Writers self-approve only low-tier slices.

## Model policy

You choose models for the work you delegate; the harness script does not.

- **Top tier** (Opus / Sol): orchestration and slicing decisions,
  source-fidelity review on high-risk slices, disputed-claim adjudication.
- **Mid tier** (Sonnet / Terra): translation, compile repair, bug fixes,
  Go-idiom review.
- **Cheap tier** (Haiku / Luna): oracle capture runs, conformance runs,
  fixture mechanics.

When in doubt: up a tier for anything that judges, down a tier for anything
that is retryable. State the chosen tier in your handoff so the next tick can
honor it.

## Stop and escalate — file it in td, mark the slice blocked, exit

- A data-format break, an unmatchable temporal behavior, or a permanent dual
  domain implementation appears (the plan's stop conditions).
- A mismatch classifies as an intentional difference — Marcus decides those.
- The same slice fails compile-repair three rounds. The boundary is wrong;
  re-slice, don't brute-force.
- You are about to weaken desktop safety for mobile's convenience, or to
  normalize a value a user can observe.

## Drift

If mainline Ruby changed since a manifest entry's `source_sha`, that entry
must end **ported**, **not applicable**, or **blocking cutover** — CI fails
while a changed Ruby behavior has no recorded disposition. Parity-sweep
issues are claimable work like any slice.
