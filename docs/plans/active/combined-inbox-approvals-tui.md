# Combine Inbox and Approvals in the final TUI tab

Status: implemented; pending visual proof and independent review

Related decisions and plans:

- [ADR-0012: Combine TUI intake queues as one projection](../../adr/0012-combine-tui-intake-queues.md)
- [ADR-0011: Task proposals are a distinct lifecycle state](../../adr/0011-task-proposals-as-lifecycle-state.md)
- [Agent task approval queue](agent-task-approval-queue.md)
- [Subtasks in context-filtered views](subtasks-in-context-filtered-views.md)

## Goal

Replace the separate Inbox and Approvals tabs with one final TUI tab that:

- presents Inbox tasks and proposals as visibly distinct sections;
- shows both filtered counts in the tab label;
- preserves single-key approval/rejection and Inbox processing behavior; and
- applies the active context filters to both sections without flattening the
  Inbox tree or admitting proposals into ordinary open-work semantics.

This is a TUI projection change. It does not change task state, persistence,
the application command boundary, CLI commands, HTTP endpoints, or OpenAPI.

## Current behavior and constraints

The relevant paths are already separated cleanly:

- `Tui::Views::Query(:inbox)` owns Inbox state and availability eligibility.
- `Tui::Views.approvals` selects only `PROPOSED` items in canonical file order.
- `Tui::App#filtered_items` applies the active context group and text search
  before building proposal rows and badge counts.
- Context-filtered Inbox rendering uses `inbox_tree`, where matching Inbox
  anchors may carry visible untagged descendants as contextual riders.
- `Tui::App#tab_counts` independently counts eligible Inbox items and matching
  proposals.
- Proposal shortcut availability is based on the selected item's state, not
  the active view, so `a` and `r` can remain row-specific in a mixed surface.
- `Views.tab_presentation` is the shared source for painted labels and mouse
  hit spans. Its narrow-width behavior currently treats a non-empty Approvals
  tab as required.
- Session restore validates the saved view against `Views::TABS`; simply
  deleting `:approvals` would send those users to Agenda.

The implementation should compose these paths rather than introduce an
`intake` state or duplicate filtering predicates in the renderer.

## Interaction design

### Tab order and count label

Use six tabs, with the combined Inbox last:

```text
1 Agenda  2 Next  3 Quadrants  4 Projects  5 Outline  6 Inbox 4 · Approvals 2
```

The two numbers are intentionally labelled, not summed. A total such as `6`
would hide the authority boundary, while an unexplained `4/2` would be hard to
decode. Unlike ordinary single badges, render both values even when one is
zero:

```text
6 Inbox 4 · Approvals 0
6 Inbox 0 · Approvals 2
6 Inbox 0 · Approvals 0
```

The last form makes an empty combined queue unambiguous. Recommended compact
variants are:

```text
6 In 4 · Ap 2
6 I4 A2
```

The full, compact, and minimum cells must all come from
`Views.tab_presentation`. The returned strip, keys, and spans remain the only
inputs to header painting and `HitMap`.

When the full strip does not fit:

- always preserve the active tab;
- preserve the combined Inbox tab when either count is nonzero;
- try the compact and then minimum Inbox label before dropping other nearby
  inactive tabs; and
- never clip a painted cell independently of its click span.

### Section layout

Render Approvals first, followed by Inbox:

```text
APPROVALS 2                         a approve · r reject
  PROPOSED  Research backup providers
  PROPOSED  Compare accounting tools

INBOX 4
  Book dentist appointment  @phone
  ▾ Plan conference trip    @work
  │   Reserve hotel
  · Buy printer paper       @errands
```

The exact casing may follow the existing theme, but the semantics are fixed:

- section headers and the blank separator are non-selectable;
- the Approvals header uses a proposal/approval semantic style (yellow in the
  default theme) and includes a muted `a`/`r` hint when non-empty;
- proposal rows retain `outline_body`, including the visible `PROPOSED` state;
- the Inbox header uses an Inbox semantic style (magenta in the default theme);
- Inbox rows retain `inbox_body`, including inline contexts, availability
  markers, tree guides, and collapse state; and
- monochrome themes still distinguish the sections through labels, state text,
  spacing, and the action hint rather than color alone.

Keep both section headers present at zero. Empty groups render their current
messages beneath the header:

```text
APPROVALS 0
  No tasks pending approval

INBOX 0
  Inbox empty. ✨
```

This stable shape avoids making the tab appear to change modes when one queue
empties.

### Navigation and decisions

Ordinary `j`/`k`, arrows, mouse selection, details, editing, links, and task
actions continue to operate on selectable rows across both sections.

`a` and `r` remain available only when the selected item is `PROPOSED`.
Recurrence `r` remains available on an eligible accepted task, and project
capture `a` remains available only on a selected project in the Projects tab.

Before approving or rejecting, capture the next visible proposal id in review
order:

- after approval, select that next proposal so repeated `a` presses continue
  processing the approval queue;
- if no proposal remains, select the newly approved Inbox item when it is
  visible under the active filters, otherwise the nearest selectable row;
- after rejection, select the next proposal, otherwise the nearest selectable
  Inbox row; and
- undo/redo and external refresh continue to reconcile by stable id.

This explicit policy is necessary because the approved task no longer leaves
the active tab: generic stable-id reconciliation would otherwise jump from the
approval section to that task in Inbox and interrupt rapid review.

Approving moves one item from the Approvals section into Inbox only when the
Inbox query considers it visible. For example, an approved future-scheduled
capture disappears while unavailable tasks are hidden. The counts and
selection fallback must follow the actual rendered result rather than assuming
every approval increments the visible Inbox count.

### Filters, trees, and counts

Context behavior stays one OR facet:

```text
(@home OR @errands) AND /optional text search AND section eligibility
```

Build the combined rows as follows:

| Active filters | Approvals section | Inbox section |
| --- | --- | --- |
| none | Flat matching proposals in canonical DFS/file order | Existing Inbox tree |
| context only | Flat proposals carrying any selected context | Existing context-scoped Inbox tree, including contextual riders |
| text, with or without context | Flat proposals from `filtered_items` | Existing flat Inbox rows from `filtered_items` |

Do not count tree riders as Inbox work. The two tab counts remain:

- **Approvals:** matching `PROPOSED` items after context/text filtering;
- **Inbox:** items passing the Inbox query after context/text filtering and the
  current show-unavailable setting.

The footer's `N matches` remains a visible task-row count across both sections.
It may exceed the Inbox badge because an Inbox anchor can show untagged riders;
this is the existing and documented meaning of that footer.

`Z` affects Inbox rows and the Inbox count only. Proposals remain visible
regardless of effective availability because they are awaiting a decision, not
claiming to be actionable.

## Implementation plan

### 1. Define the six-tab projection and migrate saved views

Files:

- `lib/tui/views.rb`
- `lib/tui/ui_state.rb`
- `lib/tui/app.rb`
- `lib/tui/shortcuts.rb`

Changes:

1. Remove the standalone `:approvals` entry from `Views::TABS`.
2. Move `:inbox` after Outline and renumber Projects, Outline, and Inbox to
   `4`, `5`, and `6`.
3. Update full/compact/minimum tab metadata and numeric shortcut documentation
   to `1-6`.
4. Add an explicit restore alias from saved `approvals` to `inbox` before
   validating the saved view. Do not make this a general symbol fallback.
5. Let `suspended_target_canonical_view` discover proposals through the
   combined Inbox rows; it must still recover a paused proposal edit.
6. Update left/right cycling, numeric jump behavior, and mouse switching through
   the shared `TABS` source rather than adding special cases.

Exit evidence: sessions saved on either old Inbox or old Approvals open the new
combined tab, and all six numeric/arrow/mouse navigation paths agree.

### 2. Compose the two sections without merging their queries

File: `lib/tui/views.rb`

Changes:

1. Split the current Inbox row construction into reusable flat/tree section
   helpers that can return rows without owning the whole-tab empty state.
2. Keep the proposal selector separate and pass it the already filtered items.
3. Add one combined Inbox builder that emits:
   - Approvals header;
   - proposal rows or proposal empty row;
   - one blank separator;
   - Inbox header; and
   - Inbox tree/flat rows or Inbox empty row.
4. Give both headers their scoped count so the list and tab label can be
   visually cross-checked.
5. Preserve `Row#selectable?`, task ids, task nodes, and marker spans. Only
   Inbox task rows carry tree nodes; proposal rows and section chrome do not.
6. Keep Inbox query eligibility and proposal state checks in their existing
   helpers. The combined builder only composes results.

The App must still choose tree versus flat mode using the existing search rule.
Treat combined `:inbox` as a context-tree view, but pass filtered items alongside
the tree so the proposal section respects context filters.

Exit evidence: each section is byte-for-byte equivalent to its current task-row
treatment apart from the new section chrome.

### 3. Model and render the paired tab counts once

Files:

- `lib/tui/app.rb`
- `lib/tui/views.rb`
- `lib/tui/hit_map.rb` only if its existing input type needs documentation

Changes:

1. Keep one memoized count calculation keyed by read-model identity, text
   filter, normalized context filters, and `show_deferred`.
2. Represent the combined tab's paired values explicitly; do not overload one
   integer as a total.
3. Format full, compact, and minimum Inbox cells from that paired value inside
   `tab_presentation`.
4. Remove the old special case that preserves a separately counted Approvals
   tab at narrow widths. Preserve the final Inbox tab when either paired count
   is positive.
5. Continue returning exact painted spans from the final cells. `header` and
   `hit_map` consume the same cached presentation.
6. Invalidate the existing row/count caches on approve, reject, undo, redo,
   show-unavailable changes, filter changes, and external reload through the
   current invalidation path.

Exit evidence: approving a visible proposal changes `Inbox 4 · Approvals 2` to
`Inbox 5 · Approvals 1` in the same repaint; a held approved item instead yields
`Inbox 4 · Approvals 1` until `Z` reveals it.

### 4. Add semantic section treatment

Files:

- `lib/tui/theme.rb`
- generated theme source/generator if section slots are generated there
- `docs/cli-spec.md`

Add semantic slots such as `inbox_section` and `approval_section`, with
contrasting default styles and attribute-only monochrome definitions. Use
these slots only for section presentation; keep proposal state styling on the
existing warning semantics and Inbox task styling unchanged.

Do not repurpose `tab_approvals` for body content. Existing
`color.tab_approvals` configuration may remain accepted as a compatibility
slot, but the removed tab no longer consumes it. Document the new section
slots and the combined tab's use of the Inbox tab style.

Exit evidence: default, monochrome/`NO_COLOR`, and one generated theme clearly
separate the two groups without changing selected-row readability.

### 5. Preserve rapid proposal review and stable selection

File: `lib/tui/app.rb`

Changes:

1. Before a proposal decision, derive the next proposal id from the currently
   rendered selectable rows.
2. After a successful mutation and cache refresh, prefer that proposal id.
3. On final approval, prefer the approved task only if the new Inbox section
   renders it; otherwise use ordinary nearest-row fallback.
4. On final rejection, use the nearest selectable Inbox row.
5. Preserve the detail panel only when its identity remains selected; otherwise
   refresh it to the selected fallback through the existing panel path.
6. Leave store checks, revisions, undo journal behavior, conflict messages, and
   `Tasks::Application#approve_task`/`#reject_task` untouched.

Exit evidence: repeated `a` or `r` processes proposals top-to-bottom without
jumping into Inbox until the approval section is empty.

### 6. Update user-facing documentation

Files:

- `README.md`
- `docs/cli-spec.md`
- optionally a short supersession note in
  `docs/plans/active/agent-task-approval-queue.md`

Document:

- six-tab order and shortcuts;
- one final combined Inbox tab;
- the two section treatments and their ordering;
- the labelled paired count and zero behavior;
- proposal-only `a`/`r`;
- context/text/show-unavailable semantics; and
- the saved Approvals-to-Inbox session migration.

No API or OpenAPI documentation changes are required because every non-TUI
capability and state transition is unchanged.

## Verification

### Focused unit and integration coverage

`test/test_views.rb`

- Combined rows always contain Approvals then Inbox headers.
- Each empty/non-empty combination renders stable section chrome and messages.
- Proposal rows retain `PROPOSED`; Inbox rows retain contexts and tree markers.
- Context-only filtering scopes both groups, while Inbox riders still render
  and do not change the Inbox count.
- Text plus context filtering uses the flat path for both groups.
- `Z` changes only the Inbox section.

`test/test_app.rb`

- Approval shifts the paired counts and advances to the next proposal.
- Final approval selects the moved Inbox task when visible.
- Approval of a held/future item decrements Approvals without incrementing the
  hidden Inbox count.
- Rejection, undo, redo, and external refresh update rows, selection, detail
  panel, and counts immediately.
- A proposal selected under an active context filter remains actionable.
- Wide, compact, and minimum tab cells have exact matching hit spans.
- At 72 columns, the active combined tab and both counts remain legible and
  clickable.

`test/test_session.rb`, `test/test_app_modals.rb`, `test/test_shortcuts.rb`

- Saved `approvals` and saved `inbox` both restore to the combined tab.
- The six-view arrow cycle wraps correctly.
- Keys `1` through `6` select the documented tabs; `7` is no longer advertised
  or accepted as a view jump.
- Direct shortcuts and action-palette availability distinguish proposal and
  accepted Inbox rows.
- Mouse tab switching follows the same renumbered tab source.

`test/test_theme.rb`, `test/test_hit_map.rb`

- New semantic section slots work in default and monochrome themes.
- Count-label width and click geometry remain identical for all label variants.

### Gates

Run:

```sh
ruby -Itest test/test_views.rb test/test_app.rb test/test_session.rb \
  test/test_app_modals.rb test/test_shortcuts.rb test/test_theme.rb \
  test/test_hit_map.rb
ruby test/all.rb
git diff --check
```

HTTP behavior is unchanged, so the API suite is not an implementation gate for
this TUI-only change. Run it only if implementation touches shared application
or API code unexpectedly.

### Visual proof

Capture wide and 72-column TUI evidence with fixture data containing:

- at least two proposals and three Inbox tasks;
- one proposal and one Inbox task matching `@work`;
- an Inbox parent with an untagged child rider; and
- one unavailable Inbox task.

Prove:

1. the final tab and both count labels at wide and narrow widths;
2. the two semantic sections in default and monochrome themes;
3. context filtering of both sections while the Inbox rider remains visible;
4. one `a` press moving a task between sections and advancing selection; and
5. `Z` changing the Inbox section/count without changing Approvals.

## Acceptance criteria

- Inbox and Approvals are no longer separate top-level tabs.
- The combined Inbox is the sixth and final tab.
- Its label shows separately labelled Inbox and Approvals counts, including
  zero, under the active filters.
- Approvals render first with proposal styling and decision hints; Inbox renders
  second with its current flat/tree treatment.
- Context filters use OR semantics within the context group and apply to both
  sections; optional text search composes with them using AND.
- Inbox contextual riders remain visible but are not counted as Inbox work.
- `Z` affects Inbox only.
- `a`/`r`, editing, details, undo/redo, mouse input, and action-palette behavior
  remain state-correct.
- Repeated approval/rejection stays in the proposal review flow until no
  proposal remains.
- Saved Approvals sessions migrate to the combined tab rather than Agenda.
- Full, compact, and minimum tab rendering share exact mouse hit geometry.
- README and CLI spec describe the six-tab combined workflow.
- Focused tests, the full core suite, `git diff --check`, and wide/narrow visual
  proof pass.
