# Plan: reusable, stable choice-picker modal

Status: proposed

Date: 2026-07-24

## Outcome

Replace the current context palette with a traditional, stable selector:

- The search field stays at the top.
- Matching choices remain in their normal ranked order while the cursor moves.
- The modal keeps a fixed footprint for the life of the picker; filtering
  changes the visible choices, not the box size.
- `Up` / `Down` move a visible cursor. The cursor is rendered with `❯`, the
  existing width-one TUI glyph.
- `Space` toggles the context under the cursor without closing or reordering
  the list.
- `Return` applies the complete staged selection and closes the picker.
- `Escape` cancels staged changes and leaves the active filters untouched.
- More than one context can be active and every selected context remains
  visibly checked.

Build this on a reusable `Tui::ChoicePicker` primitive, then use that primitive
for both the context picker and action palette. Future list selectors should
only need to supply options, labels/search text, selection mode, and the event
to run when the user accepts.

## Product decisions

### Multiple context semantics

Use **OR within the context group**: selecting `@home` and `@work` includes a
task that has either context. Continue to AND the context group with the `/`
text filter and with the view's normal eligibility rules.

This is the conventional behavior for multiple selections within one filter
facet and makes the new capability useful for viewing two locations together.
Requiring every selected context (AND within the group) would make common
location contexts mutually exclusive and usually return nothing.

Represent this explicitly in the query API as `context_filters:` plus
`context_filter_mode: :any`; do not hide the behavior in a UI-only predicate.
The mode argument leaves a clean extension point for a future "match all"
selector without changing the picker or persisted data shape.

### Cursor versus selection

Cursor and selection are separate states:

- `❯` means "the row operated on by Return or Space."
- `✓` means "this item is in the staged selection."
- `context_filter_active` color applies to checked rows.
- The cursor row also uses the normal `selection` slot so focus remains
  perceivable in monochrome and low-color terminals.

Do not use a single color to mean both states. A checked item must remain
recognizable when the cursor moves away from it.

### Apply, cancel, and clear

Changes are transactional inside the picker:

- Opening copies the active context set into a staged set.
- `Space` toggles only the staged set and keeps the picker open.
- `Return` commits the staged set, including an empty set.
- `Escape` discards staged changes.
- Keep a leading **Clear all contexts** command row for discoverability and
  one-keystroke clearing. `Space` on it empties the staged set and keeps the
  picker open; `Return` on it clears, applies, and closes in one step. The
  existing list-mode Escape shortcut continues to clear the active context
  group directly when no picker is open.

This preserves existing clearing behavior while making accidental toggles
reversible until acceptance.

## Interaction wireframe

The normal eight-row viewport:

```text
╭─ contexts ─────────────────────────────────────╮
│ search: ho█                                    │
│                                                │
│   Clear all contexts                           │
│ ❯ ✓ @home                                      │
│     @phone                                     │
│     @shopping                                  │
│                                                │
│ 2 selected · ↑↓ move · space toggle · enter apply · esc cancel
╰────────────────────────────────────────────────╯
```

With multiple selected and the cursor elsewhere:

```text
│   ✓ @home                                      │
│   ✓ @work                                      │
│ ❯   @computer                                  │
```

Filtering preserves the modal's rows. Unused result rows are blank. If there
are no matches, render `no matching contexts` in the first result row and keep
the remaining reserved rows blank.

## Why the current picker moves

`lib/tui/context_palette.rb` and `lib/tui/action_palette.rb` independently:

1. derive a result list,
2. find the selected rendered line,
3. remove it from the content list, and
4. render `[selected_line, query, *other_lines, hint]`.

That guarantees the selected choice remains visible in very short terminals,
but it also makes the selected row jump to the top and puts search below it.
Both palettes calculate height from the current `inner` rows, so filtering can
also shrink the box.

The redesign must remove this selected-line extraction. Visibility comes from
a stable viewport offset, not from rearranging content.

The other relevant seams are:

- `lib/tui/app.rb`: opens, restores, renders, accepts, and clears the picker;
  applies the context filter to rows and footer text.
- `lib/tui/ui_state.rb`: owns the singular `context_filter`, overlay modes, and
  session projection.
- `lib/tui/views.rb`: owns singular context matching and contextual tree
  anchoring.
- `lib/tui/session.rb`: stores the versioned TUI state.
- `lib/tui/screen_layout.rb`: clamps and places popup rectangles.
- `test/test_context_palette.rb`, `test/test_action_palette.rb`,
  `test/test_app_modals.rb`, `test/test_session.rb`, and `test/test_views.rb`:
  pin the existing contracts.

The active `subtasks-in-context-filtered-views.md` plan and its shipped code use
a singular context. Preserve its tree behavior and generalize its predicate;
do not create a second filtering path.

## Reusable component design

### `Tui::ChoicePicker`

Add `lib/tui/choice_picker.rb`. It owns interaction state and returns semantic
events; it performs no application mutations.

Suggested construction:

```ruby
ChoicePicker.new(
  title: "contexts",
  options: options,
  selection: active_contexts,
  selection_mode: :multiple,
  accept_label: "apply",
  empty_label: "no matching contexts",
  max_visible: 8,
  matcher: matcher,
)
```

Each immutable option has:

- `id`: stable identity used to preserve cursor and selection across reloads.
- `label`: display text.
- `search_text`: one string or an array of strings.
- `kind`: `:choice` by default; optionally `:command` for "Clear all".
- `metadata`: opaque caller data returned on acceptance.

The picker exposes:

- `input`
- `all_options`
- `results`
- `cursor_id` and `cursor_index`
- `staged_selection`
- `viewport_start`
- `dirty?`
- `handle_key`
- `paste`
- `refresh_options`
- `popup`

Semantic return values:

- `:changed` for query, cursor, viewport, or staged-selection changes.
- `[:accepted, selected_ids]` on Return.
- `:cancelled` on Escape.
- `:handled` for recognized no-ops.

For `selection_mode: :single`, Return accepts the cursor item immediately and
Space is either an alias for Return or disabled by configuration. For
`:multiple`, Space toggles and Return accepts the staged set. A configurable
command option receives `handle_option` so the generic component does not know
what "clear contexts" means.

### Thin domain adapters

Keep domain meaning out of the generic picker:

- `ContextPalette` becomes a thin factory/presenter around a multiple-choice
  `ChoicePicker`. It normalizes `@contexts`, supplies the clear command, and
  translates accepted ids into the context-filter set.
- `ActionPalette` becomes a thin single-choice adapter. It supplies shortcut
  descriptions/keys as labels and search fields, and translates acceptance
  back to the selected registry entry.

Migrating both existing palettes in this feature is the proof that the
component is genuinely reusable. Do not duplicate a second renderer or input
loop in either adapter.

Form fields can adopt `ChoicePicker` later, but replacing their specialized
date/time controls or changing task-editor key contracts is out of scope.

## Stable layout and viewport

### Geometry

Calculate preferred geometry from the complete option set when the picker
opens, not from filtered results:

- Width: widest title, search line, option label, selection summary, and hint,
  clamped to the live body width.
- Result capacity: `min(max_visible, max(all_options.size, 1))`.
- Height: border + search + separator + fixed result capacity + status/hint.
- Once open, keep preferred width and result capacity stable.
- On terminal resize, re-clamp to the new body rectangle but do not otherwise
  derive size from the query.
- On an external task reload, options may change. Preserve the existing box
  size if the new content fits; grow only if required and space is available.
  Never shrink until the picker closes.

The tiny-terminal fallback still renders the cursor item first because a
one- or two-row terminal cannot show a traditional modal. Treat that as an
explicit accessibility fallback, tested separately; normal boxed layouts must
never reorder rows.

### Viewport

Keep `results` in deterministic match order and track `viewport_start`:

- Moving within the visible page changes only `cursor_index`.
- Crossing the top or bottom scrolls by one row.
- Page/half-page navigation can be added through the same methods without
  changing rendering.
- Query edits move the cursor to the highest-ranked match and reset the
  viewport to zero.
- Toggling selection does not change results, cursor, or viewport.
- Refresh preserves `cursor_id` if it still matches; otherwise it selects the
  highest-ranked result.

When a query produces no results, the cursor is absent. Backspacing into a
non-empty result set restores it to the highest-ranked item.

## Matching and relevance

The generic picker accepts a matcher/ranker. The default deterministic ranking:

1. exact normalized label,
2. label prefix,
3. token prefix,
4. substring,
5. caller-provided original order as the tie-breaker.

Context matching strips an optional leading `@` from both query and candidate,
so `ho` and `@ho` rank `@home` identically. It should remain a compact
contains-style search, not introduce a fuzzy dependency.

Action-palette matching uses description, display key, and handler name as it
does now, but ranks matching entries with the same rules. The top-ranked result
is always the initial cursor row after a query change.

## Application and query changes

### UI state

Replace singular `context_filter` state with an ordered, normalized collection
named `context_filters`:

- Normalize to unique `@context` strings.
- Store in option order for deterministic footer text and session JSON.
- Expose `active_context_filters`; an empty array means no context filter.
- During implementation, a temporary singular compatibility reader is fine,
  but new code must not choose only the first selected context.

Update the footer to show all active contexts when space permits:

```text
 @home + @work · 14 matches · esc clears · @ changes
```

At narrow widths, truncate the rendered context summary by display cells, not
bytes; never silently imply that only one filter is active.

### Query/view semantics

Update `Views::Query` and tree builders to accept `context_filters:`. The match
predicate for the recommended `:any` mode is:

```ruby
context_filters.empty? ||
  item.contexts.any? { |context| context_filters.include?(context) }
```

Preserve the current contextual-tree contract: once a matching parent/root
anchors a thread, its visible subtasks may ride with it even if they lack the
context themselves. For multiple contexts, any selected context can anchor the
thread. Text filtering continues to use the existing flat path and composes as
AND with the context group.

Include the entire normalized set and filter mode in row fingerprints so
toggling one context cannot reuse stale rows.

### Session compatibility

Persist `context_filters` as an array. Migrate legacy
`"context_filter": "@home"` values to `["@home"]` when restoring. Save only the
array form.

Treat this as a backwards-compatible optional-key evolution of the current
session format rather than discarding view, collapse, and panel state. Invalid
elements are ignored; duplicates are removed; contexts no longer present in
the live task set are pruned individually. If all are stale, omit the key.

## Key contract

| Key | Multiple choice | Single choice |
|---|---|---|
| Printable text / paste | Edit search and select top-ranked match | Same |
| `Up` / `Ctrl-P` | Move cursor up | Same |
| `Down` / `Ctrl-N` | Move cursor down | Same |
| `Space` | Toggle cursor item; keep open | Configurable; unused for actions |
| `Return` | Apply staged set and close | Accept cursor item and close |
| `Escape` | Cancel staged changes and close | Cancel and close |
| Backspace/Delete | Edit search | Same |

`j` and `k` remain searchable characters while the search input owns typing;
they must not become navigation aliases in this modal.

## Implementation phases

1. **Generic picker state and renderer**
   - Add `ChoicePicker::Option`, matching/ranking, cursor, staged selection,
     viewport, fixed geometry, resize behavior, compact fallback, and events.
   - Unit-test state separately from ANSI rendering.

2. **Action-palette migration**
   - Put the existing action palette on the generic single-select component.
   - Preserve registry ordering as the final ranking tie-breaker, error display,
     modal return behavior, and tiny-terminal guarantees.
   - This is a low-risk proof of the abstraction before changing filter
     semantics.

3. **Context picker redesign**
   - Adapt contexts to multiple choice, add the clear command, checked-row
     styling, transactional Space/Return/Escape behavior, stable box geometry,
     and live-option refresh.
   - Add a visual regression fixture for unfiltered, filtered, multiple-checked,
     no-match, scrolled, narrow, and compact states.

4. **Multiple-context application semantics**
   - Change `UiState`, App filtering/footer/flash text, `Views::Query`, all
     affected tree builders, fingerprints, and session persistence.
   - Preserve legacy single-context restoration and list-mode Escape clearing.

5. **Documentation and proof**
   - Update the TUI section of `docs/cli-spec.md`, shortcut/help text, and theme
     slot comments.
   - Run focused picker, modal, session, query, view, and app tests.
   - Run `ruby test/all.rb` and `git diff --check`.
   - Capture reproducible terminal screenshots at a normal size and a narrow
     size; visually verify that Space toggles never move choices and filtering
     never changes the normal modal footprint.

## Test matrix

### Generic component

- Normalization and duplicate ids are deterministic.
- Exact/prefix/token/substring ranking is stable.
- Every query edit selects the highest-ranked match.
- Arrow keys clamp and scroll without reordering results.
- Space toggles only staged selection in multiple mode.
- Return accepts; Escape returns no changes.
- Cursor and viewport survive option refresh by stable id.
- Removed options are pruned from staged selection.
- Filtered/no-match states keep the same normal box dimensions.
- Unicode labels, cursor editing, paste, and cell truncation remain valid.
- Every positive width/height stays within bounds.

### Context integration

- One selected context exactly preserves current filtering behavior.
- Two contexts use OR within the group in flat and tree views.
- `/text` ANDs with the selected context group.
- Matching parent/root keeps the current subtask visibility contract.
- Toggle, cancel, apply-empty, clear command, and list-mode Escape are distinct.
- Footer and flash text name all selections or provide an honest count.
- Legacy session string migrates; arrays round-trip; stale items prune
  individually.
- External reload preserves query, cursor, staged selections, and geometry
  where possible.

### Action-palette regression

- Direct keys and palette actions still share the shortcut registry.
- Search still covers description, key, and handler.
- Return executes exactly one action; Escape executes none.
- Failure restoration keeps query/cursor and displays the error.
- Modal and task-detail return modes still cannot target a disappeared task.

## Review gates

Before implementation is called complete:

1. Review the generic component API for domain leakage: it must not know about
   contexts, tasks, shortcut handlers, or App mutations.
2. Adversarially test resize/reload/query/toggle sequences for lost selection,
   cursor jumps, or stale filters.
3. Visually compare consecutive frames while moving and toggling. Only the old
   and new cursor/check cells may change; unaffected choice rows and the box
   coordinates must remain identical.
4. Verify monochrome mode communicates cursor and checked state without color.
5. Run the full core suite and `git diff --check`.

## Explicit non-goals

- Changing task tags or adding contexts to tasks from this picker.
- Replacing the temporal/date picker.
- Adding fuzzy-search dependencies.
- Adding mouse input.
- Exposing multi-context filtering through the CLI or HTTP API; this is TUI
  view state, not a task-domain mutation.
- Redesigning the outer TUI frame or right panel.
