# Plan: editable task panel and reusable terminal forms

Status: implemented; independent adversarial review pending

Date: 2026-07-13

Implementation status: Phases 0–5 and the Phase 6A extraction, hardening, and
documentation pass are complete. Phase 6B remains an independent adversarial
review and remediation gate; no gem publication or API-stability claim is part
of the delivered feature.

Related decisions:

- [ADR-0001: Build the form system as an embedded, extractable library](../adr/0001-embedded-terminal-form-library.md)
- [ADR-0002: Commit validated task fields on blur through semantic Store patches](../adr/0002-save-task-fields-on-blur.md)
- [ADR-0003: Coalesce one task-edit session into a usable undo step](../adr/0003-task-edit-session-undo-coalescing.md)
- [ADR-0004: Use responsive named widths for the task panel](../adr/0004-responsive-task-panel-layout.md)

## Outcome

A task continues to open in the right panel as a calm, read-only detail view.
When that panel is open, `Tab` changes it into an editable form and focuses the
first field; `Shift-Tab` enters at the last field. Inside the form, moving away
from a changed field validates and saves that field before focus moves. Text
inputs, multi-line notes, choices, token lists, and date pickers use consistent
keyboard behavior and reflow as the panel changes width.

The form machinery is built as `TermForm`, an embedded stdlib-only component
library inspired by Charm's [Huh](https://github.com/charmbracelet/huh). It owns
fields, groups, focus, validation, reactive properties, and semantic rendering,
but it does not know what a task or Store is. `Tui::TaskEditForm` supplies the
task-specific fields and policy, while `Tui::TaskEditorSession` coordinates
persistence and the existing right panel. The shipped boundary works inside
this project and is demonstrated independently by the plain renderer; it
remains an internal API until another real use can test the design.

## Approved product direction

- The panel is read-only by default. Editing is an explicit mode, not the only
  way to inspect a task.
- `Tab` is contextual: panel closed means the existing agent prompt; task panel
  open means enter editing; edit mode means save-on-blur and traverse.
- Persistence is save-on-blur, not save-on-keystroke and not whole-form Save.
- `TermForm` is the working library name.
- The panel has named width modes and directional resize actions. Form elements
  stack or reflow down to a documented edit minimum.
- Existing CLI and TUI mutations remain the source of task lifecycle semantics.
- Publishing a gem is not part of this feature. Extraction is demonstrated,
  then considered separately.

Review on 2026-07-13 settled the keys: `Ctrl-K`/`Ctrl-L` directional resize,
`Ctrl-O` for finish editing, and a confirming second `Escape` before an unsaved
field buffer is discarded. ADR-0003 is accepted: immediately durable field
writes coalesce only within one byte-contiguous edit session.

## Historical baseline before implementation

The work began from these seams in the repository. This list records the
pre-implementation state; it is not a description of missing work today:

- `lib/tui/form.rb` owns small single-field popup flows.
- `lib/tui/text_input.rb` provides grapheme-aware single-line editing.
- `lib/tui/right_panel.rb` owns persistent panel identity and scrolling.
- `lib/tui/task_details.rb` is the pure read-only detail builder.
- `lib/tui/ui_state.rb` validates legal interaction-mode transitions.
- `lib/tui/screen_layout.rb` was the single terminal-geometry authority. Its
  original split used a 28-cell panel minimum and two cells of panel chrome.
- `lib/tui/shortcuts.rb` is the declarative action and help registry. Its list
  `Tab` already dispatches `focus_prompt`, so contextual behavior must extend
  that handler rather than register a colliding list shortcut.
- `Tasks::Store#with_history` already provides locking, before/after snapshots,
  atomic writes, `Tasks::Check`, rollback, reload, and the shared undo journal.

At that point, `Tui::Form` was a migration source rather than a multi-field
design. The Store had many correct individual mutations but lacked exact body
replacement and a typed semantic field-patch operation. `Tasks::Item` carried
no `body` or `parent` members, so the plan required Store-built snapshots from
fresh records rather than TUI reconstruction.

The shipped implementation added exact body replacement, `EditSnapshot`,
`TaskPatch`, `PatchResult`, `Store#patch_task!`, grouped journal history, the
multi-field adapter/session, and responsive edit layouts. `Tui::Form` now
remains only as a compatibility wrapper for the quick single-field flows.

## Delivered goals

- Edit every appropriate live-task property: title, priority, deferred status,
  dates, recurrence, contexts, tags, notes/body, location, and workflow state.
- Keep ID and closed date visible as derived, read-only metadata.
- Make keyboard traversal predictable without requiring field-specific keys.
- Make quick date text the fastest path while retaining a compact calendar.
- Save one changed semantic field on blur through one checked Store operation.
- Keep consecutive blur commits from turning one edit session into a pile of
  undo steps.
- Bind the editor to a stable task ID across resorting and external file writes.
- Preserve a focused field's text, cursor, validation, and identity when the
  task panel or terminal resizes.
- Keep generic form code independent of task records, persistence, ANSI themes,
  the app event loop, and global terminal geometry.
- Use the same field machinery for the existing date and recurrence popups.
- Work in wide, narrow, short, Unicode, pasted, monochrome, and `NO_COLOR`
  terminals with non-color focus and error cues.

## Non-goals

- A capture/new-task form in this implementation.
- Editing archived tasks, sections, IDs, closed timestamps, or raw JSON keys.
- Autosaving on every keystroke.
- A whole-form dirty draft with Save/Cancel semantics.
- Automatic three-way merging after a conflicting edit.
- Replacing the agent prompt or removing the fast `d` and `r` actions.
- Mouse interaction or drag-and-drop task moves.
- Publishing or promising a stable public gem API.

## Interaction contract

### Read mode

- `Return` opens the selected task in the current read-only detail panel.
- Normal list movement may continue to update that read-only panel.
- With no detail panel open, `Tab` continues to focus the agent prompt.
- With a task detail panel open, `Tab` enters edit mode at the first editable
  field and `Shift-Tab` enters at the last editable field.
- An `Edit task` palette action exposes the same transition for discoverability.
- `Ctrl-K` grows the task panel and `Ctrl-L` shrinks it, stepping through the
  named widths in both read and edit mode (palette: `Grow task panel` /
  `Shrink task panel`) without changing the panel's task identity.
- `Escape` closes the read-only panel as it does today.

### Edit mode and blur lifecycle

The active editor freezes the target task ID. List movement, view shortcuts,
and prompt shortcuts do not receive keys while a field owns focus.

| Key/action | Behavior |
|---|---|
| `Tab` | Validate and save a changed field, then focus the next visible enabled field. An unchanged field moves immediately. |
| `Shift-Tab` | The same operation in reverse. |
| `Ctrl-S` | Save the focused field in place without changing focus. This is a convenience and recovery action. (The TUI already runs the terminal raw, so `Ctrl-S` is not eaten by XOFF flow control.) |
| `Return` | Accept a picker choice or field-specific action; in a text area it inserts a newline. |
| `Ctrl-K` / `Ctrl-L` | Grow / shrink the panel one named width without blurring, validating, or saving the focused field. In task-edit text fields `Ctrl-K` shadows readline kill-to-end — an accepted trade; `Ctrl-U` and `Ctrl-W` still kill, and the agent prompt keeps `Ctrl-K` kill-line. |
| `Escape` | Close an inner picker first. On a dirty field, revert takes a confirming second `Escape`: the first press discards nothing and announces what would be lost. On a clean field, leave edit mode. |
| `Ctrl-O` | Finish editing: save the focused field if changed, then return to the read-only panel. This needs a direct key because the `:` action palette is unreachable while a field owns focus — `:` is ordinary text there. |

`Shift-Tab` arrives as the escape sequence `\e[Z`. Because a lone `Escape` is
meaningful inside the editor, the key reader must decode complete CSI sequences
before dispatch — a partial read must never turn `\e[Z` into an Escape (reverting
a field) followed by stray `[Z` text.

Blur is a two-phase transition, not a callback that performs IO inside a field:

1. `TermForm` normalizes and validates the field and coupled local rules.
2. If unchanged, it moves focus without asking the host to save.
3. If changed, it emits `commit_requested` with the field key, proposed value,
   expected semantic baseline, and intended next focus.
4. `TaskEditorSession` asks the Store to apply the semantic patch.
5. On success, the session refreshes the task snapshot, tells the form to accept
   the new baseline, and permits the requested focus change.
6. On validation failure or conflict, focus stays put, the pending buffer stays
   copyable, and the field or form receives an actionable error.

Opening a picker, resizing the panel, scrolling a field, or resizing the
terminal is not blur. No focus movement occurs until persistence succeeds.

### Feedback and exit behavior

- The panel title reads `task · editing` while the editor is active.
- The focused field has a label, value, hint, virtual cursor, and a visible
  `unsaved` marker after its buffer diverges from its persisted baseline.
- Parse previews are immediate; for example, `fri -> 2026-07-17 (Fri)`.
- Successful blur clears the marker and refreshes values from the Store's fresh
  snapshot, including side effects such as INBOX promotion.
- A save error remains next to the field. It never becomes a transient flash
  that disappears before the user can act.
- Escape discards only the currently unsaved field buffer, and only on the
  confirming second press — a single keystroke can never lose typed text.
  Values saved on earlier blurs remain durable.
- If the edited task leaves the current view after a successful state or
  location patch, the app selects a deterministic nearby row, returns to the
  read panel or list, and explains where the task went.

## Task form

High-impact fields are deliberately late in traversal so ordinary edits do not
cross them accidentally. Traversal order is also render order, so a field's
group must never pull it visually earlier than its traversal position — which
is why Location gets its own late `Placement` group after Notes instead of
rejoining Organization.

| Order | Group | Field | Component | Task behavior |
|---:|---|---|---|---|
| 1 | Basics | Title | `Input` | Required after trimming; normal cursor and word movement. |
| 2 | Basics | Priority | `Select` | None, A, B, C; None is a real option. |
| 3 | Basics | Deferred | `Confirm` | Owns the semantic `defer` tag. |
| 4 | Timing | Scheduled | `DateInput` | Empty unsets; quick text uses `Tasks::Dates`; picker anchors to the current date or today. |
| 5 | Timing | Deadline | `DateInput` | Same behavior; focusing it never invents a deadline. |
| 6 | Timing | Recurrence | `Input` with presets | Accepts friendly and cookie forms; requires at least one live date. |
| 7 | Organization | Contexts | creatable `MultiSelect` | Suggests existing contexts; normalizes leading `@`; preserves order and removes duplicates. |
| 8 | Organization | Tags | creatable `MultiSelect` | Suggests non-context tags; excludes `defer` because Deferred owns it. |
| 9 | Notes | Notes/body | `TextArea` | Exact multi-line replacement; links remain ordinary source text. |
| 10 | Placement | Location | searchable `Select` | Sections and eligible parent tasks; rejects self, descendants, cycles, and excessive depth. |
| 11 | Lifecycle | State | `Select` | INBOX, TODO, NEXT, WAITING, DONE, CANCELLED; irreversible-looking consequences require confirmation. |
| — | Metadata | ID | read-only | Always visible and copyable. |
| — | Metadata | Closed | read-only | Shown when present; lifecycle code owns it. |

### Semantic field ownership

Each field owns a semantic slice, not the entire record. That keeps blur
conflicts precise and prevents one control from overwriting another control's
data.

| Field | Owned semantic slice and permitted side effects |
|---|---|
| Title | `title` only. |
| Priority | `priority` only. |
| Deferred | Presence of `defer` only. |
| Scheduled / Deadline | Its date; dating INBOX may promote it to TODO; clearing the final live date also clears recurrence. |
| Recurrence | `recur`, validated against the current fresh dates. |
| Contexts | Only tags beginning with `@`. |
| Tags | Only non-context tags other than `defer`. |
| Notes | Exact raw `body`. |
| Location | Direct parent and subtree order; compare the affected structural fingerprint. |
| State | `state`, `closed`, recurrence completion behavior, and any documented body/tag/subtree lifecycle effects; compare affected task/subtree state. |

The Store merges tag slices so Contexts, Tags, and Deferred can never erase one
another. State and date helpers operate on the freshly proposed record rather
than a stale `Tasks::Item#recurring?` value.

### High-impact confirmation

When a pending blur would complete/cancel a task, advance a recurrence, cascade
to descendants, clear recurrence, or move a subtree, the session shows the exact
consequence before calling the Store. Confirm continues the pending blur;
cancel returns to the field without changing its persisted baseline. The
shipped editor rejects combinations that cannot be explained clearly rather
than splitting them into multiple writes.

## Date input behavior

`DateInput` is text-first and receives an injected parser, formatter, clock,
suggestions, and default calendar anchor. In the tasks adapter:

- `today`, `tomorrow`, `fri`, `+3`, `07-15`, `7/15`, and ISO dates use
  `Tasks::Dates` and show the canonical preview.
- Empty input is a deliberate unset operation, never a parse error.
- `Return` opens the calendar at a valid pending date, existing date, or today.
- Arrow keys move by day/week, Page Up/Down changes month, `t` returns to today,
  Return selects, and Escape returns to text entry without blur.
- Selecting a day produces the same `Date` value as quick text.
- The injected `today` makes parsing and calendar tests deterministic.
- At wide widths the calendar may sit beside text help; at narrow widths it
  stacks or becomes a compact single-month view.

## Notes text area behavior

The text area preserves pasted line breaks. Enter inserts a newline. Tab remains
form traversal; literal tab characters are not stored in task notes — pasted
tabs are normalized to spaces on entry, not silently rewritten at save time.

## `TermForm` library design

### Shipped files

```text
lib/
  term_form.rb
  term_form_event.rb
  term_form_fields.rb
  term_form_form.rb
  term_form_model.rb
  term_form_support.rb
  term_form_text.rb
  tui/
    form_renderer.rb
    task_edit_form.rb
    task_editor_session.rb
```

The flat `lib/term_form*.rb` layout is the implementation record. The dependency
direction remains:

```text
Tasks domain and Store
          ^
          | snapshots, semantic patches, typed results
Tui::TaskEditForm + Tui::TaskEditorSession
          ^
          | values, normalized events, semantic render model
       TermForm
```

`TermForm` may use the Ruby standard library. It may not require `tasks/*`,
`tui/app`, `tui/store`, task theme constants, or terminal-global state.

### Core contracts

```ruby
form = TermForm::Form.new(
  groups: [
    TermForm::Group.new(
      key: :basics,
      fields: [
        TermForm::Fields::Input.new(
          key: :title,
          label: "Title",
          value: "Book flight",
          validate: ->(value, _) { "Title is required" if value.strip.empty? }
        )
      ]
    )
  ]
)

transition = form.handle(TermForm::Event.key(:tab))
render_model = form.render_model
```

The constructors remain internal and can evolve. The shipped behavioral
contracts are:

- field keys are unique;
- events are normalized before fields see them: decoded keys and whole
  bracketed pastes arrive as distinct typed events (`Event.key`, `Event.paste`);
  fields never parse raw escape bytes (the app already brackets paste input);
- values and persisted baselines are explicit and copied;
- current values are exposed through a read-only context;
- `handle` returns one of the shipped typed transitions: `unhandled`, `handled`,
  `changed`, `focus_changed`, `invalid`, `commit_requested`, `commit_pending`,
  `commit_accepted`, `commit_rejected`, `cancel_requested`, or `refreshed`;
- fields own cursor, selection, option search, and inner viewport state;
- the form owns groups, focus order, pending values, persisted baselines,
  validation, and reactive recomputation;
- render output names semantic roles and the focused row/cursor without ANSI;
- the host accepts or rejects a commit and owns every external effect.

This protocol is persistence-neutral. Another application can accept a commit
into memory, batch it, send it to an API, or use a manual-submit host without
changing the fields.

### Reactivity

Labels, hints, visibility, enabled state, options, and validators may be static
or callables over a read-only form context. They recompute synchronously after
accepted local changes and after the host refreshes external context. The
shipped implementation uses synchronous recomputation, not an observer graph or
background subscriptions.

Task-specific examples include recurrence availability, DONE consequences,
eligible locations, and ownership of `defer`. If a selected dynamic option
disappears, the field becomes invalid; it never silently chooses another value.

### Rendering and accessibility

`TermForm` emits a semantic render document. `Tui::FormRenderer` converts roles
such as label, value, focus, placeholder, hint, error, disabled, unsaved,
choice cursor, and selected choice into ANSI and theme output. It also returns
the focused content row so `RightPanel` can keep that row visible.

Focus and errors do not rely on color alone. Monochrome and `NO_COLOR` output
use text, attributes, or glyphs. The plain renderer example renders labels,
hints, errors, and choices without cursor addressing. A full screen-reader
runner remains later work, but the semantic model leaves that path open.

### Extraction proof

- `ruby -Ilib -e 'require "term_form"'` works without loading task code.
- `examples/term_form_demo.rb` uses no `Tasks` constants.
- library tests inject fake parsers, clocks, options, key maps, and renderers.
- only the tasks adapter mentions GTD states, recurrence, task locations, or
  Store results.

Passing this proves an extraction seam. It does not by itself justify a gem.

## Responsive task panel

`ScreenLayout` remains the only geometry authority. It receives the named mode,
sampled terminal dimensions, list-context minimum, and form-content minimum,
then returns final list, panel, and content widths. Fields and renderers never
call `IO.console`.

| Mode | Intended use | Width rule |
|---|---|---|
| compact | Short read-only details | About 32 content cells where the terminal permits; not selected for editing below the minimum. |
| standard | Default reading | Roughly 40 percent split, centrally clamped. |
| wide | Forms and long notes | About 58 percent of body width. |
| focus | Dense forms and small terminals | Gives the panel the body width, preserving a narrow list strip only when feasible. |

The session persists the named preference, not a raw column count. Read mode can
honor compact. Entering edit mode promotes the effective layout to the first
mode that supplies the edit minimum while preserving the user's preference for
later read mode.

- 48 or more panel-content cells: short labels and controls may share a row.
- 32–47 content cells: labels stack above controls; hints/errors wrap below.
- 32 content cells: the shipped editable minimum.
- Below 32 available cells: automatically use focus mode if the terminal can
  provide the minimum; otherwise keep a usable read view and show the required
  terminal width instead of presenting a broken form.

These thresholds are panel-content cells after borders/dividers, not total
terminal columns. The prior 28-cell read-panel minimum was reconciled centrally
in `ScreenLayout`, not duplicated in the renderer. Height also degrades cleanly:
the focused row remains visible, text areas gain an inner viewport, and the
panel footer yields space before the focused control disappears.

`Ctrl-K`/`Ctrl-L` and terminal resize preserve task ID, focused field, pending
buffer, cursor, errors, picker state, and editor scroll. None of these actions
triggers blur.

## Task-domain persistence

### Typed values

The shipped immutable values are defined in `lib/tasks/edit_snapshot.rb`,
`lib/tasks/task_patch.rb`, and `lib/tasks/patch_result.rb`:

- `EditSnapshot`: stable ID; exact raw editable values including `body` and
  direct `parent`; per-field semantic baselines; affected-subtree fingerprints;
  display metadata for confirmation.
- `TaskPatch`: one field key, normalized proposed value, expected semantic
  baseline, edit-session coalesce key, and any confirmed consequence token.
- `PatchResult`: status, fresh snapshot when available, field/form errors,
  touched IDs, and lifecycle/location summary.

The TUI does not inspect or mutate raw record hashes and does not calculate
fingerprints. The Store builds snapshots from current records.

### `Store#patch_task!`

Each changed blur calls one semantic patch operation. Under the existing lock it:

1. re-reads the file and finds the target by stable ID;
2. compares the field's owned semantic slice, or the affected structural/state
   fingerprint for location and lifecycle operations;
3. merges the proposed value into fresh records through a pure domain helper;
4. applies documented coupled effects, such as INBOX date promotion or clearing
   recurrence with the last date;
5. validates the resulting record set and subtree rules;
6. writes exactly once through `Tasks::Format`, runs `Tasks::Check`, rolls back
   on failure, reloads, and records history;
7. returns a typed result and fresh edit snapshot.

Expected statuses include `ok`, `no_change`, `conflict`, `missing`, `invalid`,
`cycle`, and `too_deep`. Boolean failure is insufficient for an editor.

Existing CLI operations continue to work. Store-side fresh-record helpers share
blank-title, date, recurrence, lifecycle, tag, body, move, DFS, depth,
validation, and rollback rules between CLI and TUI paths. They receive the
fresh proposed record or record set rather than inferring new behavior from a
stale `Tasks::Item` captured when the editor opened.

### Conflict and reload behavior

Field-slice comparison allows an unrelated task change—and an unrelated field
on the same task—to coexist without a false conflict. It does not allow a blur
to overwrite a newer value in the slice it owns.

External writers include the in-process agent: a queued agent request can
complete and mutate the file while the editor is open. Its writes go through
the same CLI/Store path, follow the same slice-conflict rules, and break undo
coalescing like any other intervening mutation. No special case is needed, but
tests cover it because it is the most likely concurrent writer in practice.

- If the active field's owned slice changed externally, save returns conflict.
  Focus and buffer remain. Actions are Reload field, Revert local, or Keep for
  copy; overwrite is not offered.
- After every successful patch, the session adopts the Store's fresh snapshot
  for every clean field and refreshes suggestions/reactive context.
- Unfocused clean fields may reflect external changes immediately. A pending
  buffer is never replaced silently.
- Location and state compare affected-subtree fingerprints because their side
  effects are wider than one record.
- If the target disappears, the session becomes inert but retains the active
  buffer long enough to copy or discard. It never retargets the adjacent row.

A whole-record digest is not the ordinary blur guard: it would make unrelated
same-task edits conflict. Exact record/subtree fingerprints remain appropriate
for operations that truly own the whole affected structure.

### Undo coalescing

Every successful blur is immediately durable, but an ordinary editing pass
should normally be one undo step.

- `TaskEditorSession` generates a random coalesce key when editing begins.
- Each field patch supplies that key to `Store#with_history`.
- The journal may replace its current tip only when the optional key matches
  and the new mutation's `before` bytes exactly equal the current tip's `after`
  bytes. The earliest `before` and newest `after` are retained.
- CLI calls omit the key and retain today's one-command/one-entry behavior.
- Any intervening CLI/external mutation, undo/redo, reopened edit session, or
  journal mismatch breaks the group. A later blur starts a new undo segment.
- Crashes still leave every completed blur durable and undoable.

This is edit-session grouping, not a time window. It avoids losing an undo
boundary merely because someone paused while writing notes.

## TUI integration

### `TaskEditorSession`

One controller owns target ID, edit snapshot, form, coalesce key, pending blur,
confirmation, and conflict/missing state. It converts TermForm transitions into
Store calls and fresh snapshots. `App` keeps IO, polling, mode dispatch, flash
messages, list reconciliation, and panel refreshes.

Do not introduce an overlapping `TaskPanel` abstraction. `RightPanel` remains
the low-level panel and scrolling host; `TaskDetails` remains the read-only
builder. `TaskEditForm` is the domain adapter, and `TaskEditorSession` is the
editing controller.

### `UiState`, dispatch, and shortcuts

- Add `:task_edit` as an explicit legal `UiState` mode with one active session.
- Change the existing list `Tab` handler to act contextually on `detail_panel?`;
  do not register a second colliding list key.
- In edit mode, normalized events go to the session before list/prompt actions.
- The `:` action palette is unreachable while a field owns focus (`:` is text
  there), so every edit-mode action needs a direct key and generated-help entry.
- Global quit/cancel safety remains explicit.
- Generated help and the action palette derive from the same registry/key map.
- Agent prompt remains reachable with panel closed and by its palette action.

### Existing quick actions

`d` and `r` remain on their original keys. Their popup implementations use
`TermForm::Fields::DateInput` and `TermForm::Fields::Input` through the
compatibility wrapper, while retaining their return modes, error behavior, and
Store methods. This is the compatibility proof for the form engine and renderer.

## Delivery record

The implementation was delivered in reviewable phases that preserved unrelated
work and kept the full suite green. The numbered steps below are retained as the
historical implementation checklist; their imperative wording does not mark
current gaps. Phases 0–5 and Phase 6A are complete, while the independent Phase
6B adversarial review remains separate.

After Phase 0, the TermForm track (Phases 1–3) and persistence track (Phase 4)
shared no files or contracts beyond this plan, so they were built and reviewed
independently. Phase 5 was the first change that needed both.

### Phase 0: approve contracts and freeze behavior — complete

1. Review this plan and ADRs 0001–0004; mark accepted decisions accordingly.
2. Update `docs/cli-spec.md` before code with contextual Tab, blur lifecycle,
   field order/ownership, Escape, conflicts, resize, and undo behavior.
3. Add characterization tests for current prompt Tab, detail panel, `d`/`r`,
   UI transitions, session persistence, Store history, external reload, and
   screen-layout boundaries.

Exit evidence: tests derive from the interaction and persistence contracts
without inventing policy during implementation.

### Phase 1: build the form engine — complete

1. Add events, typed transitions, context, field base, group, form, baselines,
   focus traversal, validation, and reactive properties.
2. Implement the two-phase `commit_requested` / accept-or-reject protocol.
3. Add injectable key maps and semantic render output with focus/cursor rows.
4. Add dependency-boundary and require-smoke tests.

Exit evidence: a fake multi-group form can edit, traverse, skip disabled fields, react,
validate, request a commit, remain focused on rejection, and render without ANSI.

### Phase 2: add reusable fields — complete

Implemented in small slices:

1. `Input`, reusing or extracting proven grapheme behavior from `TextInput`.
2. `TextArea`, including real newline paste, wrapping, and inner scrolling.
3. `Select` and creatable `MultiSelect`, including search and dynamic options.
4. `Confirm` for boolean choices and consequence confirmation.
5. `DateInput` with injected parser, clock, preview, picker, and unset behavior.

Exit evidence: every field works without `Tasks`, respects supplied cell budgets at and
above its documented minimum, and performs no IO.

### Phase 3: add the renderer and migrate quick actions — complete

1. Add semantic form theme slots and `Tui::FormRenderer`.
2. Adapt `Tui::Form` as a compatibility wrapper where useful.
3. Move `d` and `r` onto new fields without changing their user contract.
4. Prove focus/error cues in color, mono, `NO_COLOR`, narrow, and Unicode cases.

Exit evidence: existing quick-edit flows use the new engine with no visible regression.

### Phase 4: add semantic Store patches and grouped history — complete

1. Add `EditSnapshot`, `TaskPatch`, and `PatchResult`.
2. Build exact raw body/parent snapshots inside Store.
3. Extract fresh-record mutation helpers and implement every ownership slice.
4. Add `Store#patch_task!` with typed conflict/failure results.
5. Add optional, rigorously contiguous journal coalescing to `with_history`.
6. Keep every existing CLI mutation and undo test green.

Exit evidence: every field patch writes atomically, coupled effects match CLI behavior,
conflicts are slice-accurate, and consecutive session patches undo together
without merging across an intervening mutation.

### Phase 5: integrate panel editing and responsive layout — complete

1. Add `TaskEditForm` and `TaskEditorSession`.
2. Add `:task_edit`, contextual Tab, edit actions, and generated help.
3. Add named panel widths, central clamping, content breakpoints, and focused-row
   scrolling to `ScreenLayout`/`RightPanel`.
4. Wire fields in the documented order, including confirmation and conflicts.
5. Reconcile stable selection after save, move, lifecycle change, view resort,
   missing target, and exit.

Exit evidence: every in-scope property is editable from all five views, and resize never
loses or commits a pending field.

### Phase 6: harden, review, and document — 6A complete; 6B pending

1. Add the standalone TermForm and plain-render examples.
2. Test bracketed paste, long notes/options, tiny/short/wide terminals, live
   resize, external CLI writes, malformed files, and writer failure.
3. Run an independent adversarial review focused on stale identity, partial
   writes, incorrect semantic ownership, undo merging, invisible focus/actions,
   and competing geometry.
4. Fix findings and rerun focused and full verification.
5. Update README, CLI spec, generated help, ADR statuses, and this plan's status.

Exit evidence: documentation matches the live keys and behavior; extraction and all
quality gates are proven.

## Test plan

### TermForm

- Forward/backward traversal skips hidden and disabled fields deterministically.
- Changed-field Tab requests a commit and does not move until accepted.
- A rejected commit retains field, buffer, cursor, intended direction, and error.
- Accepting a fresh snapshot updates clean baselines without erasing a pending
  buffer in another field.
- Reactive options/labels/hints/validators recompute; vanished selections become
  errors rather than implicit replacements.
- Input and text area edit grapheme clusters, combining marks, and wide emoji.
- Single-line paste sanitizes newlines; text-area paste preserves them.
- Date parsing, clearing, preview, picker navigation, leap/month boundaries, and
  injected today are deterministic.
- Semantic render documents fit supplied dimensions and identify focused row,
  cursor, errors, and non-color state.
- Loading `term_form` does not load task or app code.

### Store and journal

- Every field ownership slice changes only its owned data and documented side
  effects; especially Contexts/Tags/Deferred merging.
- No-op blur writes nothing and consumes no undo history.
- Each changed blur performs one checked atomic write.
- Consecutive patches with a valid session key coalesce; one undo restores the
  edit-session start and redo restores its final state.
- An intervening CLI mutation, external write, undo/redo, or mismatched bytes
  breaks coalescing and preserves independent undo entries.
- Blank title, invalid recurrence/date/state, cycle, and excessive depth fail
  without changing live or journal data.
- Dating INBOX, clearing the last date, recurring completion, DONE cascade,
  CANCELLED, reopen, raw body replacement, and moves match existing behavior.
- Same owned-slice and affected-subtree changes conflict; unrelated task and
  unrelated same-task slices do not.
- Missing targets, malformed external files, check failure, and writer failure
  retain recoverable original data.

### TUI and layout

- Panel-closed Tab still focuses the agent prompt.
- Panel-open Tab/Shift-Tab enters first/last task field without shortcut
  collisions; edit-mode traversal never leaks to prompt/list handlers.
- Key decoding distinguishes a lone `Escape` from `Shift-Tab` (`\e[Z`) and
  other CSI sequences, including a sequence split across two reads.
- Escape closes a picker first; a first Escape on a dirty field discards
  nothing and announces the pending revert; the second discards only that
  field's buffer; a clean field exits edit mode.
- `Ctrl-K`/`Ctrl-L` and terminal resize preserve identity, focus, buffer,
  cursor, error, picker state, scroll, and coalesce session; none causes a
  write.
- In task-edit text fields `Ctrl-K` resizes the panel rather than killing to
  end of line, while `Ctrl-U`/`Ctrl-W` still kill; the agent prompt retains
  `Ctrl-K` kill-to-end.
- Named modes and breakpoints produce exact widths through the central layout at
  31, 32, 47, and 48 content cells and around list-context constraints.
- Focused controls and errors remain visible at short heights.
- External refresh cannot overwrite an active buffer or retarget a neighboring
  row; missing tasks remain safely copyable/discardable.
- State/location changes that remove the row choose a stable fallback and clear
  the editor only after a successful patch.
- Existing `d`, `r`, detail scrolling, prompt, palette, undo, and all five views
  retain their behavior.

### Manual proof matrix

Exercise 120x32, 80x24, 60x18, and the documented minimum; color, `NO_COLOR`,
and monochrome; short and long task data; fast date text and calendar; pasted
Unicode notes; growing and shrinking the panel during an unsaved field; a
single and a double Escape on a dirty field; successive blur saves;
one-step undo; an intervening CLI write; and same-slice conflict. Manual writes
must use a temporary task file and the CLI/Store—never the live JSONL by hand.

### Verification gates

```sh
ruby test/all.rb
/Users/marcus/code/tasks/bin/tasks check
git diff --check
ruby -Ilib -e 'require "term_form"'
ruby examples/term_form_demo.rb
```

The TermForm require smoke and standalone demo are required gates. Focused tests
for the changed slice run before the full suite.

## Acceptance criteria

- A task opens read-only and enters editing only through the contextual action.
- Panel-closed Tab retains agent-prompt behavior; panel-open and edit-mode Tab
  implement entry and save-on-blur traversal.
- Every in-scope live-task property is keyboard editable in the right panel.
- Invalid or conflicting fields retain focus and never partially overwrite data.
- Each blur is one validated semantic Store patch; ordinary consecutive blur
  commits form one safe undo segment without crossing another mutation.
- Quick date strings and calendar choices produce the same canonical values.
- Notes support exact multi-line replacement.
- State and location consequences are explicit before persistence.
- Selection cannot drift to a neighbor during edit, external reload, or removal.
- Form elements reflow at 48 and 32 content-cell breakpoints; below minimum the
  layout falls back safely rather than clipping an unusable editor.
- Panel resizing and terminal resize preserve pending edit state without blur,
  and a single Escape never discards typed text.
- Existing list, detail, prompt, quick edit, palette, undo, and reload behavior
  remains covered and green.
- The flat `lib/term_form*.rb` implementation loads without task dependencies,
  and a standalone example uses the same engine.
- README, CLI spec, generated help, ADRs, and actual behavior agree.
- Full tests, task-store check, dependency smoke, and diff checks pass.

## Risks and controls

| Risk | Control |
|---|---|
| Save-on-blur creates noisy undo history | Immediate durable writes plus byte-contiguous edit-session journal coalescing. |
| One field erases another field's semantic data | Explicit ownership slices and Store-side merging, especially for tags. |
| External changes overwrite a pending edit | Stable IDs, owned-slice expectations, affected-subtree fingerprints, typed conflict results, and no implicit overwrite. |
| `App` becomes a form controller | Put editing state/effects coordination in `TaskEditorSession`; keep App on IO and dispatch. |
| Generic fields acquire task policy | Enforce dependency tests and inject parsers/options/validators through `TaskEditForm`. |
| New Store paths diverge from CLI semantics | Extract fresh-record helpers and pin parity with existing CLI mutation tests. |
| Resize produces competing widths or accidental save | One `ScreenLayout` authority and explicit non-blur resize events. |
| Contextual Tab collides with the agent shortcut | Branch the existing handler on panel/edit state; test registry uniqueness and dispatch precedence. |
| High-impact blur surprises the user | Put location/state late and require a concrete consequence confirmation. |
| Early extraction freezes a weak API | Keep TermForm embedded, prove a standalone use, and defer gem publication. |

## Accepted review decisions

This consolidated plan incorporates the independent review of both forked plan
drafts.

Accepted 2026-07-13: `Ctrl-K` grows and `Ctrl-L` shrinks the panel through the
compact → standard → wide → focus order (task-edit text fields trade away
readline kill-to-end, keeping `Ctrl-U`/`Ctrl-W`; the agent prompt keeps
`Ctrl-K`); `Ctrl-O` finishes editing, since the `:` palette is unreachable
while a field owns focus; reverting an unsaved field takes a confirming second
`Escape`.

- 32 panel-content cells is the editable minimum; 48 is the inline-layout
  breakpoint.
- Session undo coalescing follows accepted ADR-0003.
- Location remains near the end of traversal in its own Placement group; State
  remains last.
- If a successful Location or State patch removes the task from the current
  view, editing exits immediately to the read panel or list after selecting a
  deterministic nearby row. There is no inert completion screen.

All other interaction decisions above—including read-by-default, contextual
Tab, save-on-blur, and `TermForm`—reflect the approved direction.
