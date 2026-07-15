# Defer-until availability

Status: accepted implementation contract

Tracking epic: `td-0b5d63`

Plan gate: `td-ac8efd`

## Goal

Make deferral behave like OmniFocus: a task may have an **available-from** date
that keeps it out of active work views until that local calendar date. The due
date remains a separate commitment. In task records, the existing `scheduled`
field is the available-from date and `deadline` remains the due date.

The feature also preserves the existing timeless someday/maybe behavior. The
semantic `defer` tag remains the storage marker for an indefinite **On Hold**
task. Timed deferral and indefinite hold are related availability blockers, but
they are not the same field.

The motivating case must work exactly:

```sh
tasks defer "Cancel Fox after the last World Cup game" +4
```

This sets `scheduled` four days from today, preserves any `deadline`, clears an
indefinite `defer` marker, and removes the task from active views until the
scheduled date. It does not move the deadline four days.

## Current code and constraints

The implementation must extend the existing application boundary rather than
create an adapter-specific rule:

- `lib/tasks/format.rb` owns the JSONL shape and key order. It already stores
  `scheduled` before `deadline` at schema version 1.
- `lib/tasks/store.rb` parses both fields as dates and owns checked, undoable,
  conflict-safe mutations. `DEFER_TAG = "defer"` is currently the indefinite
  hiding marker.
- `lib/tasks/task_queries.rb` owns canonical named-view selection, while
  `lib/tui/views.rb` additionally owns tree visibility. These paths currently
  hide the tag but do not hide a future `scheduled` date.
- `Tasks::TaskView` is the persistence-neutral read resource used by the CLI,
  TUI, and planned HTTP API.
- `bin/tasks` currently implements `defer` as a Boolean tag toggle and
  `schedule` as a `scheduled` patch.
- the TUI currently labels the two controls `Deferred` and `Scheduled`, and
  `z` toggles the Boolean marker.
- recurrence currently advances `deadline` when both dates exist; otherwise it
  advances `scheduled`.

All writes continue through `Tasks::Application` and `Tasks::Store`. No CLI,
TUI, test, or migration may hand-edit the JSONL store.

## Product decisions

### One timed field, one timeless marker

There is no new `defer_until` storage field. `scheduled` is the single start /
available-from / defer-until date. Introducing a second date would create two
answers to when work becomes available, require a migration, and diverge from
the OmniFocus model this feature is intended to match.

The existing `defer` tag remains supported, but its user-facing name becomes
**On Hold** or **Someday/Maybe**. It means there is no automatic availability
date. This is backward compatible with every existing deferred task and keeps
the useful timeless backlog.

| Concept | Stored representation | Meaning |
|---|---|---|
| Available from / Defer until | `scheduled: YYYY-MM-DD` | Unavailable before this date; available on and after it. |
| Due | `deadline: YYYY-MM-DD` | Commitment date; never controls availability. |
| On Hold / Someday | `defer` in `tags` | Indefinitely unavailable until explicitly activated. |
| Available now | No indefinite marker, and no future effective `scheduled` blocker | Eligible for active views if its state/view otherwise qualifies. |

The words “scheduled,” “start date,” “available from,” and “defer until” refer
to the same stored field. Human-facing copy should prefer “Available from” for
the field and “Defer until” for the action.

### Availability is derived

Availability is not persisted. It is evaluated with an explicit `today`
argument. At the outer edge of each CLI command, TUI action/save, or future HTTP
request, the application captures Ruby's process-local `Date.today` exactly
once. That one immutable value is passed through the entire operation; there is
no separate tasks timezone setting and no UTC-date conversion.

For an open task considered by itself:

| Own `defer` tag | Own `scheduled` | Available today? | Own reason |
|---|---|---|---|
| absent | absent | yes | `available` |
| absent | before today | yes | `available` |
| absent | today | yes | `available` |
| absent | after today | no | `scheduled` |
| present | any value | no | `on_hold` |

Closed (`DONE` or `CANCELLED`) and archived tasks are never actionable, so their
derived availability is false with reason `closed`. Availability does not alter
their visibility under explicit done/archive/all scopes.

The boundary is inclusive: a task scheduled for July 18 is hidden through July
17 and becomes available at the first read on July 18. No midnight writer or
record mutation is required.

The one-operation date snapshot is a cross-layer invariant. The same `today`
must reach fuzzy parsing (`Tasks::Dates.parse_when`), availability reads,
`activate`'s future-date comparison, Store mutations, recurrence advancement
(`Tasks::Recur.next_date`), and the recurring completion note. No downstream
method may call `Date.today` again after the operation has captured it. A TUI
picker may stay open across midnight, but Return begins the save operation and
captures the date used for both parsing and mutation at that point. Tests inject
the snapshot rather than changing the process clock.

### Task ancestors block a subtree

An open task is effectively available only when neither it nor any task ancestor
owns an availability blocker. A future-scheduled or On Hold parent therefore
hides its whole task subtree. This inheritance is computed from parent pointers;
descendants are never rewritten. A descendant may retain its own later
available-from date or On Hold marker. Activating the parent exposes only
descendants whose remaining own/ancestor blockers have also cleared.

Ancestor lifecycle and ancestor availability are deliberately separate. A
closed task ancestor is transparent for tree hoisting: it does not by itself
hide an open descendant, become a rendered contextual row, or prevent that
descendant from anchoring a view. Its own `defer` marker or future `scheduled`
date still participates in the descendant's blocker chain, however. Traversal
must therefore continue through closed task ancestors for both purposes: skip
them when choosing visible rows/anchors, but inspect and propagate their timed or
On Hold blockers. Sections never carry availability fields and do not block.

The primary diagnostic blocker is deterministic. Build candidates from the
task and every task ancestor (including closed ancestors), then apply this exact
precedence:

1. If the subject task is closed or archived, return `closed` with no blocker id.
2. If one or more candidates carry On Hold, On Hold wins over every timed
   blocker. Choose the subject itself when it is On Hold; otherwise choose the
   nearest On Hold ancestor. Return `on_hold` for self or `ancestor_on_hold` for
   an ancestor, with that candidate's id.
3. Otherwise, if one or more candidates have `scheduled > today`, choose the
   candidate with the latest scheduled date because that date is the effective
   gate. On equal dates choose the subject itself, then the nearest ancestor.
   Return `scheduled` for self or `ancestor_scheduled` for an ancestor, with
   that candidate's id.
4. Otherwise return `available` with no blocker id.

Thus an indefinite hold always prevents a misleading dated availability claim,
while multiple timed blockers identify the date that actually controls release.
The same result object drives filtering, JSON diagnostics, TUI badges, and
activation output.

The canonical availability calculation must live above the JSONL shape, beside
the tree-aware query/read model, so flat CLI queries, TUI tree queries, and a
future HTTP adapter cannot disagree.

## CLI contract

### `defer <ref> [date]`

`defer` and its existing `snooze` alias accept zero or one date expression plus
the standard `--dry-run`, `--json`, and `--include-done` flags.

With a date:

```sh
tasks defer "file taxes" +4
tasks defer "file taxes" fri
tasks defer "file taxes" 2026-07-31
```

- Parse with `Tasks::Dates.parse_when`, using the same accepted forms as every
  other date command: ISO, month-day, weekday, `today`, `tomorrow`, and `+N`.
- Set `scheduled` to the parsed date and remove the task’s own `defer` tag in
  one changeset and one undo entry.
- Preserve `deadline`, `recur`, state (apart from existing INBOX promotion),
  priority, notes, descendants, and all other tags.
- Promote `INBOX` to `TODO`, matching every other dated mutation.
- The mutation changes the task's own timed blocker: a future own date blocks
  until that date, while today or a past own date does not block. Effective
  availability is then recomputed across the full ancestor chain; the command
  must not claim the task is available or released on its own date when an
  ancestor still controls it.
- Human output first states the own mutation, for example `Deferred "title"
  until YYYY-MM-DD`, then states the effective post-write result from the
  canonical availability object: `available now`, `unavailable until DATE via
  <ancestor ref>`, or `still on hold via <ancestor ref>`. A later timed ancestor
  therefore reports the ancestor's later effective release date; an On Hold
  ancestor reports no automatic release date.
- `--dry-run` computes the same hypothetical two-field change (`scheduled` plus
  clearing the own marker) against the current ancestor snapshot and prints the
  same own-mutation/effective-result pair prefixed with `would`; it does not
  simplify the answer to the supplied date. JSON returns the post-write task
  resource, including its effective blocker fields, and touched ids through the
  existing mutation-reporting convention.
- An unrecognized date exits 1 without writing. Missing/ambiguous refs retain
  existing exit-2 behavior.

Without a date, `defer <ref>` retains backward compatibility: it adds the
indefinite `defer` marker without changing `scheduled` or `deadline`. Its output
uses On Hold/Someday language rather than implying a date.

Add `someday <ref>` as the clear canonical spelling for the no-date operation;
`defer <ref>` remains accepted indefinitely. Agent documentation should prefer
`someday` when the request says “someday,” “maybe,” “indefinitely,” or “on hold.”

### `activate <ref>`

`activate` (and `undefer`/`resume`) means “make this task available now”:

- remove the own `defer` marker, if present;
- clear own `scheduled` only when it is later than today;
- retain a scheduled date that is today or in the past because it is not a
  blocker and remains useful history;
- preserve `deadline` and every unrelated field;
- perform both removals atomically as one undo step.

The task may remain effectively unavailable when an ancestor or its own other
condition still blocks it. Output must say so instead of claiming activation
succeeded globally.

### Existing date commands

- `schedule <ref> <date>` continues to set only `scheduled`, now documented as
  Available from. It does not silently remove an indefinite On Hold marker;
  callers that mean timed deferral use `defer <ref> <date>`.
- `undate <ref> --kind scheduled` removes the timed availability date and leaves
  an On Hold marker unchanged.
- bare `undate <ref>` keeps its current responsibility for both dates and the
  coupled recurrence cleanup. It does not alter On Hold.
- `capture --scheduled <date>` creates a task with that available-from date;
  the task is hidden while the date is future. `--due` remains deadline.
- Directly setting a past/today date is allowed; the model does not reject or
  normalize it away.

Natural-language agent guidance must translate “defer four days” to `+4` and
“defer until Friday” to `fri`. It must not add four days to `deadline` and must
not use `schedule` as a substitute for `defer` when an existing indefinite hold
also needs to be removed.

### Review filters

- Default `list` shows effectively available open tasks only.
- In the default/open scope, `list --deferred` remains the familiar review entry
  point but broadens to all effectively unavailable open tasks: own timed
  deferral, own On Hold, or an inherited blocker. Add `--unavailable` as the
  unambiguous canonical spelling for this new open-lifecycle review.
- Add `list --someday` (alias `--on-hold`) for tasks carrying their own legacy
  indefinite marker only. It does not include a descendant merely blocked by a
  parent and composes with every lifecycle scope.
- Explicit `--done`, `--archived`, and `--all` scopes continue to show their
  selected lifecycle records without applying active availability filtering.
  A closed resource still reports `available: false` / `closed`, but that fact
  never makes it a deferred-review match.
- For backward compatibility, `list --deferred` combined with `--done`,
  `--archived`, or `--all` retains its pre-feature meaning: filter that explicit
  lifecycle scope by the task's own `defer` marker. It does not broaden to timed
  or inherited blockers outside open scope. This keeps a closed task marked via
  `defer --include-done` discoverable.
- `--deferred` and `--someday` are mutually exclusive to avoid an unclear
  intersection.
- The explicitly effective `--unavailable` filter combined with `--done`,
  `--archived`, or `--all` is invalid (exit 1) rather than silently treating
  every closed row as unavailable.

The planned HTTP collection follows the same order: lifecycle scope is selected
first; `available=false` is an open-live unavailable review and is rejected with
`scope=done|archived|all`. `deferred=true` remains the task's own On Hold marker
filter and continues to compose with open, done, archived, and all scopes exactly
as the accepted OpenAPI contract already specifies. Direct `GET` resources still
expose the derived `closed` result for done/archived tasks.

## View behavior

Every active view uses effective availability, including inherited blockers,
but tree views must distinguish **anchor eligibility** from **contextual subtree
riders**. The view predicate decides which task causes a branch to appear. Once
that branch is anchored, its other open, effectively visible task rows may ride
for hierarchy/context even when they do not independently match the view
predicate. A rider never creates a duplicate anchor.

Lifecycle traversal and availability traversal stay separate: closed task rows
are never anchors or riders, but open descendants are hoisted through them. Any
timed/On Hold blocker owned by a skipped closed ancestor still hides the hoisted
descendant unless reveal mode is on.

| View | Anchor eligibility | Contextual riders |
|---|---|---|
| Agenda | A visible open branch qualifies when it contains at least one visible open task with `deadline` or `scheduled`; branch ordering uses the earliest qualifying date under existing deadline-first row semantics. | Render the branch's visible open hierarchy, including undated ancestors and descendants around the dated task. An undated row may ride but cannot make an Agenda branch appear by itself. |
| Next | A visible `NEXT` task seeds a branch; nested matching tasks collapse under the topmost matching visible anchor as today. | Visible open descendants ride even when they are TODO/WAITING/etc.; closed ancestors are skipped and matching open descendants hoist. |
| Quadrants | Existing visible open roots seed/classify branches; urgency continues to use only deadline/tag. | Visible open descendants ride under the classified root; future scheduled dates never create urgency. |
| Inbox | A visible `INBOX` task seeds a branch, with existing duplicate-anchor suppression. | Visible open descendants ride regardless of their own state; closed ancestors are skipped and matching open descendants hoist. |
| Projects | A project/section branch appears only when its rendered subtree contains at least one visible open task; header counts come from that same rendered set. | Every visible open task in the branch rides in DFS order; unavailable-only projects disappear when reveal is off and return coherently when reveal is on. |
| Default list | Flat per-row selection: available open tasks only. | None; `--deferred` is the explicit flat unavailable review. |

Reveal mode changes only the availability gate for open task nodes. It does not
make closed tasks anchors/riders, change the per-view anchor predicate, or let an
undated unavailable task create an Agenda branch alone. Once an unavailable
anchor or unavailable branch member is revealed, its open subtree rides under
the same hierarchy/collapse rules. Counts, project headers, flat filtering, and
tree bodies must all use the same revealed row set.

A future `scheduled` date never makes a task urgent. A deadline may become
overdue while a later available-from date still hides the task; the explicit
deferred/reveal surfaces must show both dates so the contradiction is visible.
No implicit date ordering validation is added in this feature.

## Read and JSON/API contract

Keep the existing stored and wire names `scheduled` and `deadline`; do not emit
a duplicate `defer_until` field that could drift. The canonical task resource
adds derived availability while retaining backward compatibility:

```json
{
  "deferred": false,
  "scheduled": "2026-07-18",
  "deadline": "2026-07-25",
  "available": false,
  "availability_reason": "scheduled",
  "availability_blocker_id": "c2e2b843"
}
```

- `deferred` remains the task’s own indefinite-marker Boolean. It is not a
  synonym for `available == false`.
- `scheduled` is the task’s own available-from date or null.
- `deadline` is the independent due date or null.
- `available` is the effective, ancestor-aware Boolean as of the operation’s
  injected/current local date.
- `availability_reason` is one of `available`, `scheduled`, `on_hold`,
  `ancestor_scheduled`, `ancestor_on_hold`, or `closed`.
- `availability_blocker_id` is the stable id of the task that owns the blocker,
  or null for `available`/`closed`.

These fields belong in `TaskView#to_h`, CLI JSON read/mutation output, examples,
and the planned OpenAPI `TaskResource`. Derived fields are read-only and are not
accepted by create/patch requests. Create and patch keep `scheduled`,
`deadline`, and `deferred` as inputs. The planned HTTP collection adds
`available=true|false` for effective availability. Its existing
`deferred=true` query remains the own indefinite-marker filter for wire
compatibility; an inherited/timed review uses `available=false`.

Application/query entry points that return derived fields accept the operation's
injected `today` for deterministic tests. CLI/TUI/API adapters capture it once
and pass it through query construction and every returned task resource, so one
response cannot cross midnight with internally inconsistent rows. Mutation
responses reuse the same snapshot used for parsing and writing rather than
capturing a second date for the post-write read.

## Recurrence contract

The recurrence cookie still describes one occurrence cadence. Completion
behaves as follows:

| Dates on recurring task | Completion behavior |
|---|---|
| `scheduled` only | Advance `scheduled` by the cookie. The new occurrence stays hidden until that date. |
| `deadline` only | Advance `deadline` by the cookie. This adds no own timed blocker; effective availability still depends on own On Hold and ancestor blockers. |
| both | Advance `deadline` by the cookie, compute the day delta between old and new deadline, and shift `scheduled` by that same delta. |

Shifting both dates preserves the occurrence’s availability-to-due window. For
example, available August 3 and due August 10 advanced by a seven-day result
become available August 10 and due August 17. The rule also applies to `.+` and
`++`: first compute the new deadline with existing cookie semantics, then apply
that exact delta to scheduled. Existing unusual orderings are shifted as-is;
this feature does not introduce `scheduled <= deadline` validation.

Completion continues to clear the task’s own indefinite marker, append the
completion note, leave the recurring task open, and avoid cascading from a
recurring parent. Completing a non-recurring parent still closes open recurring
descendants outright without advancing either date. Cancel still closes the
task and stops recurrence.

All multi-date recurrence writes are one checked transaction and one undo entry.
The recurrence base calculation, `.+`/`++` catch-up, next availability result,
and `- Did [YYYY-MM-DD]` note all consume the operation's single `today` value.

## TUI contract

### Language and rendering

- Rename the editor’s `Scheduled` field to `Available from`.
- Rename its Boolean `Deferred` field to `On hold`.
- Task details and Markdown export use `available from`; compatibility JSON
  continues to use `scheduled`.
- Timed-unavailable rows use a distinct marker such as `⏳ 7/18`; indefinite
  rows retain `⏸`; an inherited blocker indicates that it comes from a parent.
- The header count reports effectively available open tasks. Reveal mode copy
  says `unavailable shown`, not only `deferred shown`.

### Keys and actions

- `Z` continues to show/hide unavailable rows across views.
- `z` opens a `Defer until` interaction rather than blindly toggling a Boolean.
  It accepts the shared fuzzy dates plus explicit `someday` and `now` choices.
- A date performs the same atomic operation as CLI `defer <ref> <date>`;
  `someday` adds the indefinite marker; `now` performs `activate`.
- Escape cancels without writing. Invalid input keeps the prompt open with an
  error. The action palette invokes the same registered action.
- Existing `d` behavior remains a direct date editor; labels must make clear
  whether it is editing Deadline or Available from.

Deferring a selected task while unavailable rows are hidden reselects a stable
neighbor and keeps the detail/editor lifecycle safe. Reveal/hide, external file
reloads, suspended editors, flat filtering, tree counts, collapsed subtrees, and
project headers all use the same effective-availability predicate.

The save-on-blur editor keeps field ownership narrow: editing `Available from`
changes only `scheduled`; editing `On hold` changes only the marker. The `z`
action may intentionally change both through one `TaskChangeset`.

## Backward compatibility and migration

There is no file migration and no `Format::VERSION` bump:

- Every existing `scheduled` date gains the intended start-date behavior and
  may therefore disappear from active views until its date. This is the desired
  semantic correction, not a data rewrite.
- Every existing `defer` tag remains an indefinite hidden task.
- Existing callers of `defer <ref>`, `snooze <ref>`, `schedule`, `activate`, and
  `list --deferred` remain accepted. `list --deferred` broadens to effective
  unavailability only in default/open scope; under done/archive/all it preserves
  legacy own-marker filtering. `--someday` and HTTP `deferred=true` also keep the
  own marker reachable in every lifecycle scope.
- Unknown JSON keys still round-trip under the existing forward-compatible
  formatter behavior; no new stored key is introduced.
- Archived records load unchanged. Derived availability never changes archive
  bytes.
- Undo/redo restores byte-identical pre-feature-compatible records.

The release notes and agent prompts must call out all intentional visible
changes: future scheduled tasks no longer compete in active views; `defer` gains
an optional timed date and ancestor-aware output; open-scope `--deferred`
broadens while explicit non-open scopes preserve marker filtering; the TUI uses
Available from/On hold language; and two-date recurrence preserves its window.

## Implementation slices and touched files

### 1. Plan gate — `td-ac8efd`

Create this contract only, run `git diff --check`, commit, and obtain independent
approval before production work starts. `docs/plans/` has no index file, so
there is no plans index to update.

### 2. Canonical model and recurrence — `td-00db77`

Expected files:

- `lib/tasks/store.rb`
- `lib/tasks/task_queries.rb`
- `lib/tasks/task_view.rb`
- `lib/tasks/application.rb`
- `lib/tasks/edit_snapshot.rb`
- `lib/tasks/task_changeset.rb`
- `lib/tasks/task_patch.rb`
- `lib/tasks/recur.rb`
- compatibility assertions around `lib/tasks/format.rb` and
  `lib/tasks/check.rb`
- focused Store/query/resource tests

Implement one tree-aware availability result object or equivalent internal
contract, carry one `today` through each query, and implement atomic two-date
recurrence advancement.

### 3. CLI — `td-2e1c74`

Expected files:

- `bin/tasks`
- `test/test_cli_mutations.rb`
- `test/test_task_queries.rb`

Implement optional-date deferral, `someday`, activate semantics, unavailable and
indefinite filters, output/help, JSON, dry-run, undo, and ref failures.

### 4. TUI — `td-6995e5`

Expected files:

- `lib/tui/app.rb`
- `lib/tui/views.rb`
- `lib/tui/task_edit_form.rb`
- `lib/tui/task_editor_session.rb`
- `lib/tui/task_details.rb`
- `lib/tui/export.rb`
- `lib/tui/shortcuts.rb`
- `lib/tui/ui_state.rb`
- corresponding app/view/editor/modal/shortcut tests

### 5. Compatibility matrix — `td-8a3d86`

Add hermetic cross-surface tests for date boundaries, old version-1 fixtures,
ancestor inheritance, lifecycle scopes, recurrence, flat/tree parity, undo, and
conflict behavior. The hierarchy matrix must include an open child beneath a
closed ancestor with no blocker (hoisted), a timed closed ancestor (hidden until
date), and an On Hold closed ancestor (hidden until activation). It must also
cover own-plus-ancestor and multi-ancestor blocker precedence, contextual
undated riders in Agenda, nonmatching riders in Next/Inbox, project header/count
parity, reveal behavior, and invalid closed/archive/unavailable filter
combinations. CLI output/dry-run coverage must include a timed defer under an On
Hold ancestor and under a later-timed ancestor, asserting both the own mutation
date and the inherited effective blocker/release result. Lifecycle-filter tests
must prove `--unavailable`/HTTP `available=false` reject non-open scopes while
legacy `--deferred`, `--someday`, and HTTP `deferred=true` can still retrieve own
markers under done/archive/all. Tests must never point at the real task files.

### 6. Documentation and prompts — `td-ba19e5`

Expected files:

- `docs/cli-spec.md`
- `docs/conventions.md`
- `README.md`
- `docs/ideas.md` where wording is affected
- `docs/api/openapi.yaml`
- `AGENTS.md`
- `.agents/skills/tasks-cli/SKILL.md`
- `.claude/skills/tasks-cli/SKILL.md`
- the usage block in `bin/tasks` if not already complete

Both skill copies must teach the same behavior. `AGENTS.md` must explicitly
prevent the original failure: “defer four days” is timed availability, not a
deadline shift and not merely an indefinite tag.

### 7. Adversarial review — `td-c4d951`

An independent reviewer attempts to falsify the contract across all surfaces,
logs findings in td, and sends confirmed defects through fix/re-review cycles
until no P0-P2 finding remains.

### 8. Proof — `td-4fad0c`

Record concrete output/artifacts in the proof task:

```sh
ruby test/all.rb
bin/tasks check
git diff --check
```

Also record a sandbox transcript proving `defer TASK +4` preserves deadline,
writes scheduled, hides the task from default list/agenda/next, and exposes it
under the unavailable review. Include named boundary/subtree/recurrence tests
and a reproducible TUI screenshot or Betamax artifact showing distinct timed
and indefinite rows. Finally verify a clean `main` and equality of local HEAD
and `origin/main` after push.

## Quality gates and execution order

Each implementation task follows plan → implement → independent review → test →
commit, with its td id in the commit subject. The plan task is the first hard
gate. After core is approved, CLI and TUI may run concurrently because their
production files do not overlap. The compatibility matrix waits for both; docs
wait for the tested behavior; adversarial review waits for all docs and code;
proof and push verification are last.

Required repository gates after implementation are:

```sh
ruby test/all.rb
bin/tasks check
git diff --check
```

No feature task is complete merely because its focused tests pass. The epic is
complete only after the adversarial review cycle is resolved, proof is recorded,
the working tree is clean, and `main` is pushed.

## Out of scope

- A second `defer_until` persistence field or schema migration.
- Time-of-day availability; dates remain local calendar dates.
- Automatically rewriting descendants when a parent is deferred.
- Enforcing available-from before deadline.
- Replacing the existing recurrence-cookie grammar.
- Implementing the HTTP server; only the accepted OpenAPI contract and shared
  application/resource boundary are updated.
