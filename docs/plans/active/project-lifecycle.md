# Project lifecycle: open, closed, and where a project shows

Status: planned; ready to implement.

A project is a section under the top-level `Projects` root, and today a section
has no lifecycle. `project complete` closes a project's descendant *tasks* and
leaves the section itself untouched, so a finished project is indistinguishable
from a new empty one: both are a heading with no open work. The Outline renders
every live section unconditionally, and the Projects tab lists projects "even
when empty" by design, so finished commitments accumulate in both views until
someone sweeps them to `archive.jsonl` — the only exit that exists.

This plan gives a section an open/closed lifecycle, makes closing one an
explicit act with a keystroke and a CLI/API verb, and fixes the task detail rail
so it names the project a task belongs to rather than whatever section happens
to contain it.

## Contract

**Lifecycle.** A section is *open* unless it carries a `state`. A closed section
carries `state` (`DONE` for finished, `CANCELLED` for dropped) and a `closed`
date, exactly as a closed task does. Closing writes no records anywhere else:
the section stays in the live file with its subtree, and `project archive`
remains the separate, later act that sweeps it to `archive.jsonl`. The lifecycle
is open → closed → archived, and each step is explicit.

**Completing cascades.** `project complete` keeps its existing cascade — every
open descendant task goes DONE with today's `closed` date, the defer tag
dropped, a recurring descendant retired — and *additionally* stamps the section
`DONE`. Both happen in one write and one undo step. `project drop` is the
CANCELLED twin: it cancels open descendants and stamps the section `CANCELLED`,
because open tasks stranded under a dropped project would keep surfacing in
Agenda and Next forever, which is the problem this plan exists to solve.
`project reopen` clears `state` and `closed` from the section only; it does not
reopen tasks, which own their own lifecycle.

**Visibility.** Closed projects are hidden by default in the Outline and in the
Projects tab, and the existing session-only `C` closed-work toggle reveals them
in both — the same key, the same vocabulary, the same muted row idioms that
v1.16.0 shipped for closed tasks. `tasks projects` and `GET /api/v1/projects`
exclude them by default and gain a flag to include or select them. Resolution is
not filtered: `project show <ref>`, `project reopen <ref>`, and
`GET /api/v1/projects/{id}` still reach a closed project, exactly as they
already reach a reserved GTD list the listing omits.

**Hiding a section never hides open work.** A hidden closed section is
*transparent*, not pruned — its children render at the same depth, the way the
Outline already hoists open tasks out of a closed parent. An open task under a
closed project stays visible in the Outline, in Agenda, and in Next. Task
visibility stays driven by the task's own state; an ancestor section's state
never suppresses a task.

**Detail rail.** Every task detail rail carries a row naming the project the
task belongs to — the ancestor section that is a child of the `Projects` root —
in every view that can open the rail, Agenda included.

## Data model

`internal/check/validate.go` currently lists `state` and `closed` in
`sectionForbidden`. Both come off that list, replaced by narrower rules:

- A section's `state`, when present, must be `DONE` or `CANCELLED`. A section is
  not actionable, so `INBOX`/`TODO`/`NEXT`/`WAITING`/`PROPOSED` on one is an
  error, not a warning.
- A section's `closed`, when present, must be a `YYYY-MM-DD` date (`checkDate`,
  already called there for `archived`).
- The two travel together: `state` without `closed` and `closed` without `state`
  are both errors, mirroring the task rule at `validate.go:256`.
- `rejected` stays forbidden — proposals are tasks.

`record.KeyOrder` already holds `state` and `closed`, so serialization order and
`knownKeys` need no change. `check.Version` stays `2`; see the open questions.

```jsonl
{"type":"section","id":"a1b2c3d4","parent":"9f8e7d6c","state":"DONE","title":"Website redesign","closed":"2026-09-01"}
```

## Work sequence

Each slice ends green (`go test ./...`, `go vet ./...`, `gofmt`, and a build of
`./cmd/tasks`, `./cmd/tasks-api`, `./cmd/tasks-tui`) and is independently
reviewable.

### 1. Schema and store

- `internal/check`: the section rules above, plus fixtures for a valid closed
  section, a section with an open state, and each half-stamped section.
- `internal/store/project.go`: one mutation
  `CompleteProject(id, state, today string) (closed int, found bool)` that does
  the descendant cascade and the section stamp inside a single `withHistory`
  body, and `ReopenProject(id string) (bool, bool)` that clears both fields.
  Keep the existing rollback discipline: a clean no-op (already stamped, nothing
  to cascade) and a rolled-back write both report zero, and only the second
  records a rollback.
- Prove: resulting record fields, structural integrity, refusal on an invalid
  file, and undo/redo returning both the tasks and the stamp in one step.

### 2. Read model

- `taskquery.ProjectView` gains `State` and `Closed` (plus `HasClosed`), and a
  `Closed()`/`Open()` helper for callers that only need the boolean.
- `taskquery.Node` gains the section's `State` and `Closed` so row builders can
  ask a node directly; `BuildTree` reads them off section records.
- `Queries.Projects()` excludes closed sections — every existing caller (CLI,
  API, TUI) inherits the new default from the one function. Add
  `Queries.ProjectsIncluding(closed bool)` for the callers that opt in.
- `ProjectView(id)` and `SectionView(id)` keep resolving closed sections.
- A closed project is never `Stuck`: no open NEXT is the point of a commitment
  that is over.

### 3. Application and CLI

- `Application.CompleteProject` keeps its name and gains the stamp;
  `DropProject` and `ReopenProject` join it, all three on the existing
  gate-on-checked-read-first shape. `ProjectSummary` carries the new state.
- `cmd/tasks/project.go`: `project drop` (alias `cancel`) and `project reopen`
  in `projectSubcommands`; `project complete` gains a line naming the project
  closed alongside its touched-task list.
- `cmd/tasks/projects.go`: `--closed` (only closed) and `--all`; `state` and
  `closed` added to `writeProjectJSON`, omitted when absent like every other
  optional member.
- `docs/cli-spec.md`: the project verb table, the `projects` row in the
  selections table, the JSON support table, and the Outline/Projects narrative.
- `.agents/skills/tasks-cli/SKILL.md`: the new verbs and flag.

### 4. TUI

- **Outline** (`appendOutlineNode`): a closed section is hidden unless
  `ShowClosed`, and hiding it is transparent — children carry on at the same
  depth. `outlineSectionBadge` and `outlineHiddenNote` count hidden closed
  sections as well as tasks, so the reveal stays discoverable from the screen.
- **Projects** (`buildProjects`): closed projects arrive only when `C` is on,
  render as foldable headings in the muted closed vocabulary, sort after the
  open ones, and never join the dormant tail — the tail keeps meaning "an open
  project with nothing live under it".
- **Keys**: `c` already opens the complete-project confirmation whenever a row
  carries a `ProjectView`, which covers both the Projects tab and Outline
  section rows, so completing gains the section stamp for free. Two new
  availability predicates join `project_selected?` in `actions.go`:
  `project_open_selected?` and `project_closed_selected?`.
  - **Reopen is `r`**, gated on `project_closed_selected?`. Both existing `r`
    bindings (reject proposal, recur) require `CurrentItem()`, which is nil on
    a project row, so `r` is free there. Reopening is non-destructive, so a
    mis-key costs nothing.
  - **Drop has no sequence.** It is palette-only —
    `Sequences: nil, DisplayKey: "palette"`, gated on
    `project_open_selected?` — following the precedent at `shortcuts.go:236`.
    The keys that reinterpret themselves on a project row (`c`, `x`, `e`) all
    keep their verb and change their scope; drop has no task-side verb to scope
    up from, because the TUI has no cancel-a-task key, so any letter would
    attach a brand-new meaning to a key that means something else one row up.
    It is a rare, destructive, weekly-review act, and
    `tasks project drop <ref>` is the non-interactive path an agent uses, so
    nothing is gated behind a keystroke. `X` stays free if a binding is wanted
    later.
  - Both use the confirmation-modal shape `ConfirmCompleteProject` uses.
- **Project detail rail** (`BuildProjectDetails`): a `state` row and a `closed`
  row when the project is closed, and an `ACTIONS` block — the rail has none
  today, while the task rail's `detailActions` is the pattern to follow — whose
  pairs follow the selected project's own state: `c complete · x archive · drop`
  on an open project, `r reopen · x archive` on a closed one. Drop is spelled
  without a key because it has none; the palette is where it lives.

### 5. Task detail rail names the project

- `taskquery.Node` gains `ProjectSection()`: climb ancestors to the section that
  is a child of the `Projects` root, nil when there is none.
- `BuildTaskDetails` takes the resolved project rather than a bare name and
  emits one row in the `extra` block beside `deadline` and `repeats`: labelled
  `project` and naming the project when the task is under one; labelled `list`
  and naming the containing top-level section otherwise; absent entirely for
  Inbox and the reserved GTD lists, where the task's own state already says it.
  A closed project shows its state beside the title.
- The `Labels:` line keeps contexts and tags only.
- `Model.projectNameOf` and both `BuildTaskDetails` call sites in `model.go`
  move to the new resolver. The rail is view-agnostic already, so Agenda, Next,
  Quadrants, Inbox, Outline and Projects all pick this up from one change.

### 6. API

- `internal/api/representation.go`: `state` and `closed` on the project
  resource.
- `GET /api/v1/projects` gains `?closed=exclude|include|only`, default
  `exclude`.
- `POST /api/v1/projects/{id}/drop` and `…/reopen` beside the existing
  `/complete`; `projectPath`, `routeLabel`, `completeRoute` and friends in
  `server.go` extend to match.
- `docs/api/openapi.yaml` and the semantic parity tests.

## Acceptance evidence

- A store whose only project is stamped `DONE` shows no project row in the
  Outline or the Projects tab, and shows both — muted, unstuck, foldable — after
  `C`.
- An open task filed under a closed project is still visible in the Outline
  without `C`, and still appears in Agenda and Next.
- `tasks project complete <ref>` closes the tasks and stamps the section in one
  undo step; `tasks undo` restores both.
- `tasks projects` omits it, `tasks projects --all` includes it with
  `"state":"DONE"`, and `tasks project show <ref>` still resolves it.
- `tasks check` passes on the resulting file, and rejects a section stamped
  `NEXT` or stamped `DONE` with no `closed` date.
- TUI visual proof under `docs/plans/active/evidence/`: Outline and Projects
  before and after `C`, and a task detail rail in Agenda showing its `project`
  row.

## Settled decisions

These were weighed and closed during planning. Reopen one only with a stated
reason.

1. **`check.Version` stays 2.** The version is matched exactly, so bumping it to
   3 would force a migration of every existing store for a purely additive
   field. The accepted cost is that an *older* `tasks` binary reading a file
   with a stamped section reports `section must not carry "state"` and refuses
   to mutate — a hard break rather than a warning. Acceptable because the CLI,
   API and TUI binaries move together. Worth remembering when reading
   `docs/multi-device-sync.md`.
2. **The archive sweep stays task-only.** `x` keeps selecting DONE/CANCELLED
   *tasks* as roots, so closing a project never silently archives it on the next
   sweep and `project archive` stays the explicit sweep. Letting a closed
   section become a sweep root would be a larger change to `ArchivePreview` and
   its blocking rules, and it would collapse two lifecycle steps this plan
   deliberately separates.
3. **The area rule is untouched.** An area is a top-level section that currently
   holds open work, so one whose work is finished has already left the listing
   and `C` deliberately does not bring it back. Stamping an area closed stays
   permitted by the schema and by ref resolution, but the Projects tab gains no
   closed-area reveal here.
4. **Drop gets no keystroke; reopen is `r`.** Reasoning in the TUI slice above.
