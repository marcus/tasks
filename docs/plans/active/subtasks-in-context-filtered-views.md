# Show subtasks in list views when a context filter is active

Status: reviewed; ready to implement

## Context

When a context filter (e.g. `@work`) is selected, the agenda — and Next / Quadrants / Inbox — silently drop into a **flat** render path that never walks the tree, so subtasks disappear. Pressing `L`/`H` (expand/collapse all) does nothing in that state because those keys only toggle the collapse set, which the flat path doesn't consult. This isn't a bug in `L`; subtask rendering was simply never implemented for the filtered path.

Marcus keeps `@work` selected essentially all the time (it persists in `~/.local/state/tasks/tui.json`), so in practice he *never* sees subtasks. Confirmed against real data: task `b2ab048a` ("Surface LARA to Denis") has subtask `4afad57c`, which renders fine with no filter but vanishes under `@work`.

**Goal:** with a context filter active (and no `/` search), Agenda / Next / Quadrants / Inbox render as trees scoped to that context, with subtasks visible. Subtasks show under a matching parent **regardless of the subtask's own tags** (decided with the user) — so nothing hides just because a subtask wasn't re-tagged.

## Approach

Thread the context filter through the shared `Query` object as a **separate** `context_match?` predicate (do **not** fold it into `eligible?`), then route context-filtered list views through the existing tree builders instead of the flat ones.

Why not fold into `eligible?`: Agenda anchors a root when *any* subtree item is agenda-eligible (open + dated). Folding context into `eligible?` would drop an undated `@work` parent whose only dates sit on untagged children — the parent matches the filter, the dated kids don't, and the whole thread vanishes. Keeping the predicates separate preserves today's Agenda rule (dated work somewhere in the subtree) while adding "some visible item carries the context."

Because tree **riders** are chosen by `visible?` (open + available), *not* by `eligible?` / `matching?`, subtasks under a selected anchor stay fully visible — the "show all subtasks" behavior, with no new rider-filtering logic.

| View | How context scopes anchors | Riders |
| --- | --- | --- |
| Agenda | Subtree has ≥1 `eligible?` item **and** ≥1 `context_match?` item (may be different nodes) | All `visible?` descendants |
| Next / Inbox | Nodes where `matching?` (`eligible? && context_match?`); maximal-match via `matching_ancestor?` using `matching?` | All `visible?` descendants |
| Quadrants | `anchor_roots` selected with `matching?` (root itself must carry the context) | All `visible?` descendants |

Search (`/`) stays on the flat path (flat results are the expected shape; when both `/` and `@` are active, flat + pre-filter by both). Outline and Projects keep their current flat-under-context behavior (out of scope; avoids destabilizing their more complex builders).

## Changes

`lib/tui/views.rb`, `lib/tui/app.rb`, plus tests.

### `lib/tui/views.rb`

1. **`Query`**: add `context_filter:` to `initialize` (store `@context_filter`). Add:
   ```ruby
   def context_match?(item)
     @context_filter.nil? || item.contexts.include?(@context_filter)
   end

   def matching?(item) = eligible?(item) && context_match?(item)
   ```
   Leave `eligible?` as view-only (availability + agenda/next/quadrants/inbox rules). Change `select` (and therefore `grouped`) to use `matching?` instead of `eligible?`.
   `item.contexts` already returns `@`-prefixed tags (`lib/tasks/task_view.rb`); `active_context_filter` yields the same `"@work"` form via `ContextPalette.normalize`.

2. **`matching_ancestor?`**: switch the ancestor check from `query.eligible?` to `query.matching?`. Required so a context-matching NEXT/INBOX child under a same-state but *non*-matching parent still anchors itself (with context folded only into `eligible?`, or with ancestor checks left on bare `eligible?`, that child would be wrongly suppressed).

3. **`view_query`**: accept `context_filter: nil` and pass it into `Query.new`.

4. **`rows`**: add `context_filter: nil`; include it in the `ctx` hash handed to the tree builders. Flat builders keep calling `view_query` without it (pre-filtered `items` from app.rb — no double filtering).

5. **Tree builders** `agenda_tree`, `next_tree`, `quadrants_tree`, `inbox_tree`: add `context_filter:` and forward it to `view_query(...)`.
   - **Agenda only** — replace the anchor `any? { query.eligible? }` with the two-predicate form:
     ```ruby
     items = subtree_items(n, show_deferred, reader: reader, today: today)
     items.any? { |item| query.eligible?(item) } &&
       items.any? { |item| query.context_match?(item) }
     ```
   - Next / Inbox / Quadrants need no further logic: they already go through `query.select` / `matching_ancestor?`, which become context-aware via (1)–(2).

### `lib/tui/app.rb`

In `rows`, replace the flat-vs-tree branch. Precise rule:

```ruby
tree_views = %i[agenda next quadrants inbox]
use_tree = if active_filter
             false                                    # `/` (alone or with @) → flat
           elsif active_context_filter
             tree_views.include?(@ui.view)            # @ on list views → tree
           else
             true                                     # no filter → tree (incl. outline/projects)
           end
```

- When `use_tree` and `active_context_filter`: pass `tree:`, `collapsed:`, and `context_filter: active_context_filter` into `Views.rows`.
- When `!use_tree` (search, or context on outline/projects): flat path as today.
- The existing `items = items.select { contexts.include?(ctx) }` pre-filter is harmless on the tree path (those builders read `tree`, not `items`); leave it. `rows_fingerprint` already includes `active_context_filter`.

Footer `@work · N matches` counts `@row_item_count` (visible task rows). Under the tree path that includes untagged riders — acceptable; "matches" means rows shown, not tag-hits only.

## Known behaviors

**Quadrants — nested match under non-matching open parent.** `quadrants_tree` selects from `anchor_roots` with `matching?`, so the **root** of an open subtree must itself carry the context. A `@work` task nested under an open non-`@work` parent won't surface in `@work` Quadrants (Agenda / Next / Inbox are unaffected). Documenting rather than reworking quadrant grouping; flag if it bites.

**Agenda — non-matching ancestor scaffolding.** Agenda still anchors at the open subtree root. If a `@work` dated child sits under an open parent that lacks `@work`, the parent row appears as scaffolding so the matching descendant can nest. Same shape as unfiltered Agenda (undated/non-qualifying parents already surface when a descendant qualifies). Inverse of the primary ask; rare when threads are tagged consistently.

## Verification

1. **Unit tests** in `test/test_views.rb` (extend `tree_rows` to accept `context_filter:`, or pass it via `V.rows`; reuse `with_records`, `NESTED`):
   - Agenda + `context_filter: "@computer"`: matching dated parent still nests its subtasks (thread glyph), mirroring `test_children_render_indented_in_agenda`.
   - Untagged subtask under matching parent still appears (`c2` "undated rider" under `p1`).
   - Top-level task lacking the context is excluded (`plan trip` / `p2` under `@computer`).
   - Agenda edge: undated matching parent + dated untagged child still anchors (proves context stayed out of `eligible?`).
   - `context_filter: nil` produces byte-identical rows to today.
   - Parallel Next / Inbox cases; one Quadrants case that a matching root still rides untagged children.
2. **App regression** in `test/test_app.rb`: with `context_filter` set and no `/` filter on Agenda, rows carry nodes and `collapse_all` / `expand_all` change visible titles (counterpart to `test_collapse_expand_do_not_crash_during_filter`, which pins the flat `/` path).
3. **Full suite:** `ruby -Itest test/test_views.rb test/test_app.rb`, then `ruby test/all.rb`.
4. **Live smoke:** TUI with `@work` selected, Agenda shows `b2ab048a` with "Talk with David about how LARA relates to Johnny" nested beneath it; `L`/`H`/`h`/`l` expand/collapse it.
