# Recurring lead time

Status: proposed implementation contract

Tracking epic: _create at plan gate_

Plan gate: _create at plan gate_

## Goal

Let a dated task — recurring ones above all — stay hidden until a **set span
before its occurrence date**, and keep that window forever as the recurrence
rolls. The span is stored as intent, not as a computed date, so it survives
completion, activation, deadline edits, and cookie changes.

The motivating cases must work exactly:

```sh
tasks capture "water succulents" --recur 2w:sun --lead 1d
tasks capture "clean gutters"    --recur y:06-01 --lead 17d
```

The succulents task is invisible until the Saturday before each occurrence
Sunday. The gutters task is invisible until May 15 each year. Neither needs any
per-cycle maintenance, and neither acquires a due-date commitment it did not
already have.

## Current code and constraints

- [`defer-until-availability.md`](defer-until-availability.md) is the accepted
  availability contract and stays authoritative. `scheduled` is available-from,
  `deadline` is the due date, the `defer` tag is indefinite On Hold, and
  availability is derived, ancestor-aware, and computed against one injected
  `today`/`TemporalContext` per operation.
- `Tasks::TaskQueries#build_availability` (`lib/tasks/task_queries.rb:486`) is
  the single canonical gate. It builds a candidate list of the task plus every
  task ancestor, resolves On Hold first, then picks the latest future
  `scheduled` release instant among candidates.
- `Tasks::Store#advance_recurrence_records` (`lib/tasks/store.rb:2654`) advances
  `deadline` by the cookie and shifts an existing `scheduled` by the same day
  delta; with no deadline it advances `scheduled`.
- `Store#patch_activate` (`lib/tasks/store.rb:2243`) deletes a future
  `scheduled`, which today permanently destroys a recurring task's window.
- `Tasks::Recur` owns cookie parsing, `step(date, n, unit)` calendar stepping,
  and humanizing. `Format::KEY_ORDER` owns the stored key order at
  `Format::VERSION = 2`, with unknown keys round-tripping forward-compatibly.
- `bin/tasks recur <ref> <schedule|off>` is the shape a lead command mirrors.

All writes continue through `Tasks::Application` and `Tasks::Store`. No CLI,
TUI, test, or migration may hand-edit the JSONL store.

## Product decisions

### One stored span, one derived gate

A new optional stored field `lead` holds a canonical positive span:
`Nd`, `Nw`, `Nm`, `Ny` (same units and calendar-clamping semantics as a
recurrence cookie interval, via `Recur.step` with a negative count).

Availability is still derived and still has exactly one timed answer per task:

| Concept | Stored | Meaning |
|---|---|---|
| Occurrence anchor | `deadline` if present, else `scheduled` | When the work is actually due or happens. |
| Lead | `lead: "17d"` | Become available this long before the anchor. |
| Own timed gate | derived | `anchor − lead` when `lead` is set; otherwise `scheduled`, exactly as today. |

`lead` deliberately does **not** introduce a new `availability_reason`. A
lead-gated task reports `scheduled` / `ancestor_scheduled` with the derived date
in `available_at`, so every existing filter, badge, review view, and API client
keeps working unchanged. The stored `lead` value is exposed as its own read-only
resource field for clients that want to explain the gate.

Because the gate is derived from the anchor, **recurrence advancement needs no
lead-specific logic**: the cookie moves the anchor and the window follows.

### Where the anchor may live

`lead` applies to whichever field holds the occurrence date. This is what makes
both motivating cases work without inventing a due date for a soft chore:
`2w:sun` may be anchored on `scheduled`, `y:06-01` on either.

Validation rules, enforced in `Store` so every adapter inherits them:

1. `lead` requires an anchor. Setting it on a task with neither date is
   rejected (`lead requires a scheduled date or deadline`).
2. `lead` and a task carrying **both** dates is rejected. The explicit
   `scheduled`/`deadline` window already expresses the same intent, and two
   spellings of one gate is how this drifts. The error names the alternative.
3. While `lead` is set, `scheduled` may not be used as a manual defer gate:
   `defer <ref> <date>` and `schedule <ref> <date>` against a deadline-anchored
   lead task are rejected, pointing at `tasks lead` and `tasks activate`. On a
   scheduled-anchored lead task, `schedule` continues to mean "move the
   occurrence" (a legitimate anchor edit) while `defer <ref> <date>` is
   rejected.
4. A lead longer than the recurrence period is allowed without a warning. It
   simply means the task is always visible; no validation invents a policy the
   user did not ask for.
5. Clearing the anchor (`undate`, or `patch_date` to nil) clears `lead` in the
   same changeset, alongside the existing `recur` cleanup.

Sections never carry `lead`; add it to `Check::SECTION_FORBIDDEN`.

### Granularity and release instant

This slice ships **calendar units only** (`d`, `w`, `m`, `y`). Clock units are a
planned follow-up (slice 5b), so the gate is designed instant-shaped from day
one and adding `h` stays additive rather than a refactor.

The gate resolves to an **instant**, not a date. `build_availability` already
compares `value.release_instant(context) > context.now`, and `Availability`
already carries `available_at` as an instant beside its display date. The new
`effective_gate(candidate)` helper must therefore return an instant (plus the
display value the reason object needs), never a bare `Date`, even while every
supported unit happens to produce a midnight-aligned one.

For a calendar unit the arithmetic stays on the *date*: the gate is a date-only
`TemporalValue` for `anchor.date − lead`, inheriting the anchor's timezone
metadata but never its local time, so it releases at local start of day in the
anchor's effective zone. A 9am deadline with `lead: "1d"` becomes available at
00:00 local the previous day, not 9am. Date arithmetic is what makes "17 days
before" land on the same wall date whether or not a DST transition falls in
between; a duration would drift by an hour.

Calendar clamping follows `Recur.step`: `1m` before March 31 is February 28 (or
29), matching how the recurrence engine already steps months and years.

### Planned clock units (slice 5b)

Recorded here so the model slice does not foreclose it:

- Grammar adds `h` only. `m` already means months in the recurrence grammar and
  must not be overloaded; minutes, if ever wanted, need an explicit `min`.
- A clock lead is **duration arithmetic on the anchor's release instant**, and
  the gate stays that raw instant rather than being rebuilt into a
  `TemporalValue`. Fold ambiguity only arises going wall-clock → instant, so
  computing `anchor_instant − 5h` never produces an ambiguous DST fall-back
  gate. Rebuilding a `TemporalValue` from it would reintroduce that problem;
  don't.
- **All-day anchors accept clock leads.** A date-only value releases at local
  midnight (`Timezones.earliest_on`), so `5h` before June 1 is 19:00 on May 31.
  This follows the same release rule as the rest of the model rather than
  inventing a special case, and must be stated explicitly in the CLI spec and
  agent prompts because it will otherwise surprise.
- Display surfaces must render a time on the gate, reusing the existing
  timed-`scheduled` rendering.
- Before starting 5b, verify that an idle open TUI notices a gate instant
  passing and makes the row appear without a manual reload. This is already
  true of any timed `scheduled` date, so it is a verification, not assumed new
  work — but clock leads make mid-day flips common enough to matter.

### Ancestors and precedence

The gate is computed per candidate inside `build_availability`, so an ancestor's
`lead` participates exactly like an ancestor `scheduled` does. The precedence
table from the defer contract is unchanged: closed/archived first, then On Hold
(self before ancestor), then the **latest** future timed gate among candidates
(self before ancestor on ties), then available.

### Activation is per-occurrence

`activate` on a lead task cannot delete the anchor — for a scheduled-anchored
task, that is the recurrence anchor. Instead, activation stamps an optional
`lead_skip` field holding the ISO anchor date the suppression applies to:

- `build_availability` ignores `lead` for a task whose `lead_skip` equals its
  current anchor date.
- Any write that changes the anchor — recurrence advancement, `due`, `schedule`,
  `undate` — deletes a `lead_skip` that no longer matches, so the suppression
  expires with its occurrence and never leaks into the next one.
- `activate` on a lead task therefore reads as "I will do this one early"
  without un-deferring the series. This also fixes the existing defect where
  `activate` on a recurring task with a `scheduled`/`deadline` window deletes
  `scheduled` permanently: for a lead task the standing intent survives.

The pre-existing behavior for non-lead tasks (delete a future `scheduled`) is
unchanged.

## CLI contract

### `lead <ref> <span|off>`

Mirrors `tasks recur` in shape, flags (`--dry-run`, `--json`), and error style.

```sh
tasks lead "clean gutters" 17d
tasks lead "clean gutters" 2w
tasks lead "clean gutters" off
```

- Parse to the canonical span; reject anything else with exit 1 and no write.
- Apply the validation rules above; each refusal names the fix.
- Human output states the mutation and the effective result from the canonical
  availability object, following the `defer` precedent: `Lead time for "clean
  gutters" — available 2026-05-15 (17d before 2026-06-01)`, then `unavailable
  until DATE`, `unavailable until DATE via <ancestor ref>`, or `still on hold
  via <ancestor ref>` when an ancestor controls the outcome.
- `off` removes the field and restores plain `scheduled` gating; output states
  the resulting availability rather than assuming the task became visible.
- `--dry-run` prints the same pair prefixed with `would`, computed against the
  current ancestor snapshot. `--json` returns the post-write task resource and
  touched ids through the existing mutation-reporting convention.

### `capture --lead <span>`

Accepted alongside `--recur`, `--due`, and `--scheduled`, subject to the same
validation. `--lead` with neither date and no `--recur` is rejected; with
`--recur` it uses the anchor the recurrence establishes. `propose` accepts it
on the same terms it accepts other dated fields.

### Existing commands

- `activate` gains the `lead_skip` behavior described above.
- `undate` and anchor-clearing `due`/`schedule` clear `lead` per rule 5.
- `defer <ref> <date>` and `schedule` refuse per rule 3.
- `show` and the unavailable review render the derived date plus its span, e.g.
  `⏳ 5/15 (17d before 6/1)`, so the contradiction between "hidden" and "due
  soon" stays visible.
- `list --deferred` / `--unavailable` include lead-gated tasks with no filter
  changes, because the reason code is unchanged.

## View and TUI contract

- Every view already consumes effective availability, so anchor eligibility,
  riders, reveal mode, counts, and project headers need no per-view change.
  They must be proven against lead tasks, not re-implemented.
- The task editor gains a `Lead time` field beside `Recurrence`, showing the
  humanized span and the resolved date (`17d before 6/1 → 5/15`). Its validation
  mirrors the recurrence field's inline errors.
- Task details and Markdown export render the span and derived date.
- The `z` `Defer until` picker gains no new mode; on a lead task the timed
  choices refuse per rule 3 and the prompt points at the editor field, while
  `now` performs the `lead_skip` activation and `someday` still applies On Hold.
- Row markers reuse the existing timed-unavailable marker with the derived date.

## Read and JSON/API contract

The canonical task resource adds one read-only field and keeps every existing
one stable:

```json
{
  "scheduled": null,
  "deadline": "2026-06-01",
  "recur": "y:06-01",
  "lead": "17d",
  "available": false,
  "availability_reason": "scheduled",
  "availability_blocker_id": "c2e2b843",
  "available_at": "2026-05-15T07:00:00Z"
}
```

- `lead` is the stored span or null, and is accepted on create and patch.
- `lead_skip` is internal bookkeeping and is **not** part of the wire resource.
- `availability_reason` keeps its existing enum. `available_at` and the
  availability object's date carry the derived gate.
- No new collection query parameter: `available=false` already selects these
  tasks.
- OpenAPI gains the field on `TaskResource`, the create body, and the patch
  body, plus one worked example.

## Recurrence contract

Unchanged. `advance_recurrence_records` keeps advancing `deadline` (shifting a
paired `scheduled` by the same delta) or advancing `scheduled` when it is the
only date. Because `lead` is relative to the anchor, the window follows for
free. The only additions are clearing a stale `lead_skip` and preserving `lead`
across the roll. The `- Did [YYYY-MM-DD]` note, `.+`/`++` catch-up, and the
single-`today` invariant are untouched.

## Backward compatibility and migration

- No `Format::VERSION` bump. `lead` and `lead_skip` are optional keys added to
  `KEY_ORDER` after `recur`; absent keys mean today's behavior exactly, so every
  existing record keeps its current visibility.
- Known degradation to document: an older binary round-trips the keys
  (forward-compat rule) but does not apply the gate, so it shows a lead task as
  available. No data loss, and the newer binary resumes hiding it.
- `Check` validates the span grammar, the anchor requirement, the both-dates
  refusal, and `lead_skip` as a date; it reports violations rather than
  repairing them.
- Undo/redo restores byte-identical pre-feature records.

## Implementation slices and touched files

Create td ids for each slice at the plan gate and put the id in each commit
subject.

### 1. Plan gate

This contract only. Run `git diff --check`, commit, and obtain independent
approval before production work starts.

### 2. Canonical model

- `lib/tasks/format.rb` (key order), `lib/tasks/check.rb` (validation,
  `SECTION_FORBIDDEN`)
- `lib/tasks/recur.rb` or a small `lib/tasks/lead.rb` for span parse/humanize/
  step, reusing `Recur.step`; the parser rejects `h` for now but its shape must
  leave room for a unit whose arithmetic is a duration rather than a date step
- `lib/tasks/task_queries.rb` — extract the per-candidate gate in
  `build_availability` into one `effective_gate(candidate)` helper that returns
  an instant (see Granularity), and apply `lead`/`lead_skip` there
- `lib/tasks/store.rb` — create normalization, `patch_lead`, `patch_activate`
  stamping, anchor-edit and advancement `lead_skip` cleanup, rules 1–5
- `lib/tasks/task_view.rb`, `lib/tasks/task_patch.rb`,
  `lib/tasks/task_changeset.rb`, `lib/tasks/edit_snapshot.rb`
- focused Store/query/resource tests

### 3. CLI

`bin/tasks` (`lead`, `capture --lead`, `show`/review rendering, usage block),
`test/test_cli_mutations.rb`, `test/test_task_queries.rb`.

### 4. API

`lib/tasks/api/representation.rb`, `lib/tasks/api/app.rb` (create/patch input),
`docs/api/openapi.yaml`, `test/api/`.

### 5. TUI

`lib/tui/task_edit_form.rb`, `lib/tui/task_editor_session.rb`,
`lib/tui/task_details.rb`, `lib/tui/export.rb`, `lib/tui/views.rb`,
`lib/tui/shortcuts.rb`, plus the corresponding app/view/editor/modal tests.

### 5b. Clock units (optional follow-up)

Additive on top of the shipped slices: accept `h` in the span grammar, add the
duration branch to `effective_gate`, render a time on the gate across CLI/TUI/
JSON, and document the all-day-anchor definition. Gated on the TUI mid-day
refresh verification above. Its test matrix is the expensive part — hour leads
across a DST transition in both directions, on floating vs zoned vs all-day
anchors, and immediately either side of local midnight.

### 6. Compatibility matrix

Hermetic cross-surface tests: both motivating cases end to end; deadline-anchored
and scheduled-anchored leads; boundary days on either side of the derived date;
month/year clamping; DST transitions falling between the gate and the anchor,
proving a calendar lead holds its wall date; timezone-carrying anchors
releasing at local midnight; `h` rejected by the span parser;
ancestor leads and mixed own/ancestor precedence; `lead_skip` expiring on roll
and on anchor edit; every refusal in rules 1–5; reveal mode, flat/tree parity,
project header counts; undo; conflict behavior; and an old fixture with no
`lead` key. Tests must never point at the real task files.

### 7. Documentation and prompts

`docs/cli-spec.md`, `docs/conventions.md`, `README.md`, `docs/api/openapi.yaml`,
`TASK_AGENT.md`, and both `tasks-cli` skill copies
(`.agents/skills/`, `.claude/skills/`). Agent guidance must teach "hide it until
N before it's due" as `lead`, distinct from timed `defer` (a one-off date) and
`someday` (indefinite hold).

### 8. Adversarial review

An independent reviewer attempts to falsify the contract across all surfaces,
logs findings in td, and sends confirmed defects through fix/re-review cycles
until no P0–P2 finding remains.

### 9. Proof

```sh
ruby test/all.rb
bundle exec ruby test/api/all.rb
bin/tasks check
git diff --check
```

Record a sandbox transcript proving both motivating cases: hidden before the
derived date, visible on it, still hidden the rest of the cycle after `done`
rolls the anchor, and `activate` releasing exactly one occurrence. Include a
reproducible TUI screenshot or Betamax artifact showing the lead field and a
lead-gated row. Verify a clean `main` and equality of local HEAD and
`origin/main` after push.

## Quality gates and execution order

Each slice follows plan → implement → independent review → test → commit. The
plan task is the first hard gate. After the model slice is approved, CLI, API,
and TUI may run concurrently because their production files do not overlap. The
compatibility matrix waits for all three; docs wait for tested behavior;
adversarial review waits for docs and code; proof and push verification are
last.

## Out of scope

- Clock-unit leads in the first release; they are planned as slice 5b, and the
  model slice must not foreclose them.
- Sub-hour lead granularity, and any overloading of `m` to mean minutes.
- A lead that shifts the occurrence itself rather than its visibility.
- Per-occurrence lead overrides beyond the binary `lead_skip` activation.
- Relaxing rule 2 to let `lead` coexist with an explicit two-date window.
- Any change to the recurrence cookie grammar or to `availability_reason`.
