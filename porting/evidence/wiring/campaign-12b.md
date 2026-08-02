# CAMPAIGN 12, GROUP B — rendering, layout, theming and mouse

Companion to the ten records in `campaign-12b-records.jsonl`. Campaign 12 was
seeded by three agents working in parallel off one partition
(`campaigns-5-6-9-12-inventory.md`): group A took the app shell, group C took
forms, modals and pickers, and this pass took everything that measures, lays out,
paints or hit-tests. This file holds group B's reasoning, in the shape
`campaign-10.md` set: where the cuts are, what the method cannot see, and what the
next agent would otherwise have to re-derive.

Result: **10 slices, 260 Ruby tests claimed** from twelve test files this group
owns exclusively. Every slice carries `"fixtures": []`, `"fixtures_todo": null`,
and an `oracle_gaps` entry stating the method and why. No fixture is requested,
no schema extension is proposed, and no existing record is edited.

---

## 1. The method, decided before the slicing

Marcus's decision, taken before this pass and recorded verbatim in every one of
the ten records: **campaign 12 is proved by translated unit tests, not by
differential conformance.** The reasoning, so that a later reader does not
re-open it:

- `porting/specs/observations.schema.json` has **no frame, cell, cursor or
  attribute field.** The nearest thing is `process.stdout`, a raw byte stream
  that is a much weaker *superset* of the visible screen. Two implementations
  that paint the identical screen with different cursor-movement strategies
  differ byte-for-byte, and a comparison that fails on that is not a finding.
- There is **no key or mouse input vocabulary.** `invocation.stdin` is one blob
  written before the process starts; a TUI session interleaves reads with
  repaints. This is why all 832 campaign 12 tests drive objects directly.
- `invocation.tty` is **false on all three streams** under the copy protocol, by
  construction, and the schema's own description says that is "not evidence about
  what the implementation would print if one were."

The buildable alternative — a pty plus frame capture — is **not being built.**
This is a personal task manager; a pty-driven differential frame harness is not
worth its cost. What follows from that has to be said out loud rather than left
as an absence, and each record says it:

> A GREEN CONFORMANCE RUN SAYS NOTHING WHATEVER ABOUT THIS SLICE.

Nobody should read the empty `fixtures` array as an oversight, and nobody should
file a `fixtures_todo` asking for a pty. This is the method `agent-request-queue`
already uses for the same structural reason (campaign 10 §4d: a queue is a
sequence of interleaved calls against a live object, which the case-list format
cannot express), and it is PORTING.md's *recorded* skip rather than a silent one:
"Three things are never skipped at any tier … Skipping anything else is recorded
in the manifest entry."

**The consequence for slicing, which is the reason this pass looks different from
campaign 10's:** because the proof is the translated tests, the `ruby_tests` list
*is* the specification. It is not a pointer at a fixture that carries the real
contract; it is the contract. So the lists are exhaustive and exact — every test
in group B's twelve files is claimed by exactly one slice, with one deliberate
exception (§3) — and the slices are deliberately coarse, because a boundary drawn
between two tests that argue with each other produces two reviews that cannot
settle anything.

---

## 2. Where the cuts are

Ten slices. Dependency order, top to bottom:

```
tui-text-metrics
 ├─> tui-theme-and-border ──┐
 ├─> tui-screen-layout ─────┼─> tui-frame-composite ──┐
 └─> tui-view-row-projection ──> tui-view-row-decoration ──> tui-hit-map ──> tui-mouse-input
                                                            ^                 (deps: hit-map)
        tui-theme-and-border + agent-request-queue ──> tui-agent-activity
        tui-view-row-projection + tui-frame-composite + tui-app-shell ──> tui-paint-cache
```

| Slice | Tests | Source | Risk |
|---|---:|---|---|
| `tui-text-metrics` | 32 | `lib/tui/ansi.rb`, `lib/char_width.rb` | medium |
| `tui-theme-and-border` | 39 | `theme.rb`, `generated_themes.rb`, `border.rb` | medium |
| `tui-screen-layout` | 17 | `screen_layout.rb` | medium |
| `tui-frame-composite` | 37 | `frame.rb`, `right_panel.rb` | medium |
| `tui-view-row-projection` | 59 | `views.rb`, `tui/store.rb` | medium |
| `tui-view-row-decoration` | 24 | `views.rb`, `project_details.rb`, `task_details.rb` | medium |
| `tui-hit-map` | 12 | `hit_map.rb` | medium |
| `tui-mouse-input` | 23 | `mouse.rb`, `mouse_router.rb` | medium |
| `tui-agent-activity` | 4 | `agent_activity.rb` | low |
| `tui-paint-cache` | 13 | `app.rb` | medium |

No slice writes anything, so nothing here is high risk under PORTING.md's tier
table. Nothing is low except `tui-agent-activity`, and that only because it is 116
lines of pure projection from a value object to strings — everything else has
enough judgement in it that a self-approved review would be the wrong shape,
especially given that the differential leg is absent.

### The four cuts worth defending

**`tui-text-metrics` is first and has no dependencies, because everything
measures through it.** It is also the one slice in group B with a consumer
outside the renderer: `lib/term_form/text.rb` (group C) delegates to the same
`CharWidth` module. The Ruby comment says why the module exists at all — `Tui::Ansi`
and `TermForm::Text` each carried a verbatim copy of the tables, so one perf
regression lived in two places. The Go package therefore has to be reachable from
the form layer without importing the renderer, and that is a constraint on the
port's package graph, not a detail.

**`tui-screen-layout` is separate from `tui-frame-composite` even though the frame
is its only consumer.** Layout is 188 lines of pure geometry and its tests are
*exact-boundary characterizations*: `test_editing_admission_is_exact_at_minimum_terminal_width`
and `test_editing_admission_is_exact_at_named_height_across_widths_and_modes`
assert that admission flips between adjacent integer sizes. An off-by-one there is
invisible at every terminal size a human happens to try and locks the editor out
at one specific size. Folding it into the frame would bury a set of arithmetic
boundaries inside a slice whose review is about escape sequences.

**`test_views.rb` is cut in two, not in one and not in four.** 81 tests over a
1321-line file is more than one review, and the natural seam is sharp:
*projection* fails by a row being absent or in the wrong place; *decoration* fails
by a row being present and wrong. The projection slice keeps `eligible?`,
`matching?`, `matching_ancestor?`, the anchor rules and the context filters
together because the test file's own comments argue them against each other —
`matching_ancestor?` must use `matching?` and not `eligible?`, and the comment
explains what breaks otherwise. Splitting *those* would produce two reviews
neither of which could settle whether a row is missing or merely moved. The
decoration slice takes the delegation-marker block, which is its own thing: a
width-budget negotiation between three independent markers, with the truncation
*order* pinned by seven tests.

**`tui-hit-map` and `tui-mouse-input` are separate, and one test crosses each
boundary.** The hit map fails by answering the wrong zone for a correct
coordinate; the router fails by taking the wrong action for a correct zone.
`test_hit_map_list_cells_match_rendered_row_glyphs` lives in `test_frame.rb` and is
claimed by `tui-frame-composite`, because the assertion is over the rendered
frame; `tui-hit-map` carries a dep edge on the frame for it. Symmetrically,
`tui-hit-map`'s two tab-span tests consume `Tui::Views.tab_spans`, which
`tui-view-row-decoration` owns — a real dep edge, not a plausible one.

### One merge that could be undone

`border.rb` (209 lines, 14 tests, requires only `ansi`) is folded into
`tui-theme-and-border` rather than sliced alone. It is the cleanest available
boundary in group B and it was merged deliberately, to keep the slice count in the
range Marcus asked for and because the gradient *is* a theme slot — four
`test_theme.rb` tests are about border behavior. If that slice ever proves too big
to review in one sitting, border is the seam, and its record's `notes` says so.

---

## 3. The one test in group B's files that group B does not claim

`test/test_views.rb#test_agenda_sorts_timed_items_by_exact_boundary_not_file_order`
is **not claimed**, and this is the second time the exclusion has had to be
written down.

`query-named-views` (campaign 3) reserved it by name — agenda's timed-boundary
sort "lands with campaign 5" — and inventory §3c honors that reservation while
calling the file otherwise 81/81 campaign 12's. The file is `test_views.rb`;
`lib/tui/views.rb` is a renderer; the behavior is temporal. Campaign 12 owns the
file, campaign 5 claims the single test across the file boundary.

Two things make this worth a section rather than a line:

1. **`validate` will not catch a double claim.** Two slices sharing one test is
   legal — the manifest has four such tests already. So the only defence is that
   both agents decide explicitly, and inventory §6 says so: "The two agents must
   not both claim it silently." This is group B's explicit decision not to.
2. **The hole it leaves is real.** Agenda ordering by exact instant is proved by
   campaign 5's slice and by *nothing* in campaign 12, so a port of
   `tui-view-row-projection` can order timed agenda rows by file order and stay
   green against all 59 of its tests. That sentence is in the record's
   `oracle_gaps`, not only here.

Inventory §4 item 13 already owes `query-named-views` an amendment; it now needs
two ids, campaign 5's slice and `tui-view-row-projection`.

---

## 4. `test/test_app_paint_perf.rb` — claimed, and why the inventory said otherwise

Inventory §5d files this file's 13 tests as having "no home in the schema",
because `metrics.wall_ms` is "advisory only; never part of conformance equality in
either direction" and the RSS and CPU fields are permanently null. **That is
entirely true of a repaint budget and it is not true of these tests.**

Not one of the 13 measures time. Every assertion is `assert_same` / `refute_same`
on object identity, a stubbed call count, or a dirty flag:

```ruby
first = app.send(:rows); second = app.send(:rows); assert_same first, second
ui(app).filter = "flight";                          refute_same before, after
app.instance_variable_set(:@paint_dirty, false); …  assert_equal 0, paints
app.instance_variable_set(:@last_paint_size, [10, 40]); assert app.send(:idle_layout_changed?)
```

They are **cache-invalidation correctness tests wearing a perf file's name**.
Under this campaign's method they are exactly as provable as everything else in
group B, and the behavior they pin is not performance: it is whether the screen
goes stale after a filter change, a collapse, a terminal resize or midnight. A
port that reuses the row list across a collapse shows the user the wrong tree.

So they are **claimed**, as `tui-paint-cache`, with three gaps stated in the
record:

- the repaint *budget* — the thing §5d is actually about — remains proved by
  nothing, `perf_budget` is null, and there is no perf gate in the tree;
- every test reaches into `Tui::App` private state, so the Go port must expose
  those seams deliberately (an observable row-cache fingerprint, an injectable
  date provider — the Ruby already has `date_provider:` — and an injectable
  winsize reader) or the behavior becomes unprovable;
- `test_idle_layout_changed_on_terminal_resize` stubs `IO.console.winsize` and
  `test_idle_layout_changed_on_date_rollover` injects the date, so both **bypass
  the determinism pins entirely**. Nothing asserts the app reads `LINES`/`COLUMNS`
  or `TASKS_PIN_NOW`, which is the "a pin that resolves but is not used is
  invisible" defect class determinism.md names — and the Ruby-only defence
  (`test_porting_determinism_seams.rb`, which patches `Date.today` through
  `RUBYOPT`) has no Go analogue.

The cost of the decision is honest and stated: this slice names `lib/tui/app.rb`,
whose drift closure is **83 files** — effectively the whole product. It ports a
handful of methods on that file, not the file, in the two-owners pattern
`campaign-10.md` §6 recorded for `lib/tasks/config.rb`.

---

## 5. What the method cannot see, beyond the frame problem

The frame/stdout gap is in every record. These are the ones a reader would not
expect, listed here so they are not scattered.

**5a. Truecolor capability is unpinned, and it changes every painted byte.**
`test_no_truecolor_drops_gradient_to_solid` supplies the decision as an argument.
Nothing proves how the Ruby *concludes* a terminal is truecolor-capable, and
`porting/specs/determinism.md` has no `TERM` or `COLORTERM` pin. Inventory §5d
says whether `TERM` becomes a pin is campaign 12's first harness question; it is
not answered here, and no change to determinism.md is proposed. The gap is
recorded on `tui-theme-and-border`, where the decision lives, and again on
`tui-frame-composite`, where the damage shows.

**5b. `char_width.rb`'s tables are hand-maintained data with a dozen samples.**
The WIDE and ZERO_WIDTH range lists are not a generated Unicode property export.
A port that reaches for a rune-width library will disagree with Ruby at some
codepoint no test names — most likely a text-presentation symbol, which this file
deliberately keeps at **one** cell where several width libraries say two. The
tables are the contract; transcribe them. Separately, grapheme segmentation is
Ruby's `each_grapheme_cluster` at whatever Unicode version Ruby was built
against, and Go's comes from a different library at a possibly different version —
the same "recorded, never compared" shape as `environment.tzdb_version`.

**5c. `generated_themes.rb` is 1887 lines of data proved only by properties.**
Three tests assert that the generated themes exist, parse, and override the intake
slots. None asserts a single colour. A port that transcribes one theme's hex value
wrongly is green forever. Port it as data and diff the data.

**5d. Hit precedence is pairwise, never a full ordering.** Only modal-over-list
and popup-over-list are proved. `test_popup_renders_on_top_of_modal` proves the
*paint* order for the modal/popup pair; **no test proves the hit order for it.** A
port that gets paint right and hit wrong routes clicks to an invisible widget with
the suite green. This is the sharpest untested gap group B found.

**5e. The mouse *reader* is proved by nothing.** `Tui::Mouse.decode` is a pure
function of one complete report and is fully provable. What no test anywhere
covers is getting a complete report out of a byte stream: a report split across
two reads, a report embedded in a buffer of ordinary keypresses, or the fate of
the bytes of a report the decoder rejected. That reader is in the app loop, which
group A owns. If it has no test there either, this is a hole in the campaign, not
in the slice.

**5f. Legacy X10 mouse encoding is deliberately unsupported and no test says so.**
The Ruby has a comment pointing at the plan document. A port that "helpfully"
accepts X10 is green and then mis-decodes every coordinate above 95. Preserve the
refusal on purpose.

**5g. `WHEEL_DELTA = 3` is pinned by no test.** Two tests assert the shared
*direction*, never the magnitude. A port that scrolls one line per notch passes
everything and feels broken.

**5h. Empty-state copy has no other owner.** `test_inbox_empty_state` and the two
projects empty states assert exact strings. `tasks` has no CLI equivalent of these
views, so campaign 8's human-formatting work will never reach them —
`tui-view-row-decoration` is the only place that wording is pinned.

**5i. `reach` is blind to all of it, again.** `VERB_OWNERS` maps store *mutation*
verbs only, so an oracle reaching downstream through a renderer or a read model is
invisible to the tool (campaign 10 §4g, inventory §5e). `tui-view-row-projection`
drives `Tui::Views` against a real `Tasks::Store` snapshot through the 10-line
`lib/tui/store.rb` adapter, and `reach` will keep reporting zero unexplained no
matter what. Reading the test body is the only defence and it is part of
characterizing every slice here.

---

## 6. Ownership decisions that touch other groups

Recorded because the merge has to reconcile them, and because an exclusion stated
once and never enumerated goes missing (`slicing.md` §1 item 2).

**Cross-group `depends_on`, one only.** `tui-paint-cache` depends on
**`tui-app-shell`**, a group A slice id this pass guessed. The edge is real — a row
cache is meaningless without the loop that asks for rows. Checked afterwards
against group A's records as written alongside this pass: `tui-app-shell` is in
fact their first slice's id, so the edge resolves; the orchestrator should still
confirm it survived their final revision. Every other dep in these ten records
names either another group B slice or `agent-request-queue`, which already exists
in the manifest.

**No test collides across the three groups.** The 260 refs here were diffed
against group A's and group C's records as written: zero overlap in either
direction, and zero overlap with the 546 refs already in `manifest.jsonl`.

**Files group B does *not* claim, with the reason:**

- `lib/tasks/config.rb` — the `theme`, `colors.*`, generated-theme-name,
  `TASKS_THEME`/`NO_COLOR` and `mouse` key parsing. Group A claims the ten
  `test/test_config.rb` tests that prove them (the brief reserved theme-related
  config keys to group A explicitly), so group A should name the file. Inventory
  §3g says C12 needs `config.rb` in *some* `source_paths` or the parse rule is
  unwatched for drift; group B's slices take an already-resolved overrides hash and
  never read a config file, so naming it here would be claiming a closure for
  behavior we do not port. **If group A did not name it, nobody did.**
- `lib/tasks/determinism.rb` — the `LINES`/`COLUMNS` winsize pin. Its two tests are
  in `test/test_determinism.rb`, a mixed file group A owns (inventory §3k).
- `lib/tui/dates.rb` — the four-line compat shim `Tui::Dates = Tasks::Dates`, with
  no Go analogue (inventory §3m). `lib/tui/app.rb` requires it, so it is already
  inside group A's closure and inside `tui-paint-cache`'s; naming it as a ported
  path anywhere would be pretending to port an alias.
- `bin/tasks-tui` (11 lines), `lib/tui/clipboard.rb`, `lib/tui/export.rb`,
  `lib/tui/session.rb`, `lib/tui/ui_state.rb`, `lib/tui/shortcuts.rb`,
  `lib/tui/app.rb` proper — group A. `lib/tui/task_edit_form.rb`,
  `task_editor_session.rb`, `form*.rb`, `modal*.rb`, `*_palette.rb`,
  `choice_picker.rb`, `text_input.rb`, `lib/term_form**` — group C.

**One file no group named.** Checked against groups A's and C's records as
written alongside this pass: the union of all three groups' `source_paths` plus
the 53 existing manifest records covers every file under `lib/tui/`,
`lib/term_form**`, `lib/char_width.rb` and `bin/tasks-tui` **except
`lib/tui/modals.rb`** (43 lines). Its tests — `test/test_modals.rb` (15) — are
group C's, so the file is group C's to name; group B owns neither the file nor a
test in it and does not claim it here. Until it is named, a change to it is
reported by no `drift` run, which is the exact hole `campaign-10.md` §5 item 3
asked campaign 12 to close.

**Files group B names that another group will also name.** Established practice
(`bin/tasks` is named by five slices; `campaign-10.md` §6 records the `config.rb`
case). Each record's `notes` says which half it ports:

- `lib/tui/views.rb` — both `tui-view-row-projection` and
  `tui-view-row-decoration`. No test is claimed by both.
- `lib/tui/task_details.rb` — `tui-view-row-decoration` ports `note_line` and the
  parts `ProjectDetails` reuses; the detail *panel* (`TaskDetails.build`, and the
  three TUI cases in `test/test_links_feature.rb`) is group A's.
- `lib/tui/app.rb` — `tui-paint-cache` ports the paint-path memoization; group A
  owns `App`.

**Three tests claimed out of another group's file.**
`test/test_theme.rb`'s three `note_line` tests are claimed by
`tui-view-row-decoration` rather than `tui-theme-and-border`. `test_theme.rb` is
group B's own file so this crosses no group boundary — it crosses a *slice*
boundary, and the reason is drift closure: naming `task_details.rb` in the theme
slice would pull `views.rb` → `tui/store.rb` → `lib/tasks/store.rb` and the whole
task core in, taking that slice's watched set from **5 files to 35** for one
function.

---

## 7. Amendments this pass does **not** make

No existing record is edited; `manifest.jsonl` and `campaigns.jsonl` are
untouched. The amendments campaign 12 makes necessary are already enumerated in
inventory §4 and are the merging orchestrator's, not group B's. The two that bear
directly on these ten records:

1. **`config-resolution`**'s gap still says the theme and mouse keys "belong to
   campaign 8's CLI and TUI work". Wrong twice — they are campaign 12's, and they
   were never campaign 8's (inventory §4 item 1). `tui-theme-and-border`'s gap
   records the correction without making it.
2. **`query-named-views`**'s "lands with campaign 5" sentence now needs both slice
   ids and a note that the test lives in a file campaign 12 otherwise owns
   entirely (inventory §4 item 13, and §3 above).
3. **`agent-request-queue`**'s campaign 12 exclusion sizes the "13 more" tests as a
   count and not a list, which cannot be re-derived mechanically (inventory §3b:
   a name grep returns 34 candidates). `tui-agent-activity` claims 4 of the 29 and
   says in its own `oracle_gaps` that it cannot discharge the rest — so the
   obligation is now carried by a record rather than only by a wiring note.

`links-read`'s "the TUI is campaign 8" error (inventory §4 item 2) is unaffected by
group B — its three TUI cases are in `test/test_links_feature.rb`, group A's file.

---

## 8. Verification

Run against `campaign-12b-records.jsonl` as written, at the current tree:

```
10 records, key set and ordering identical to porting/manifest.jsonl   ok
campaign 12, source_sha "PENDING", status "not_started",
  evidence "porting/evidence/<slice-id>/" on all ten                   ok
fixtures [] and fixtures_todo null on all ten, each with an
  oracle_gaps entry stating the method                                 ok
260 ruby_tests, every one path/file.rb#test_name, every `def` resolves ok
no bare-file claims, no duplicate within or across the ten records     ok
no collision with the 546 refs in the 53 manifest records              ok
every source_paths entry exists                                        ok
depends_on resolves to a manifest id, a group B id, or `tui-app-shell` ok
no cycle in the group B dependency graph                               ok
test/test_views.rb: 80 of 81 claimed; the missing one is
  test_agenda_sorts_timed_items_by_exact_boundary_not_file_order       intended (§3)
  [superseded 2026-08-02: the merge review claimed it here too — see
   the addendum at the end of this document]                           ok
the other eleven files: every test claimed                             ok
```

Per-slice closure last-touch commits, computed the way `manifest.md` prescribes
(`git log -1 --format=%H -- <closure>` over the transitive `require_relative`
closure) rather than pinned to HEAD, for whoever fills in `source_sha`:

| Slice | watched | last touch |
|---|---:|---|
| `tui-text-metrics` | 2 | `5de0ea5138b4` |
| `tui-theme-and-border` | 5 | `64ad8d6dbc28` |
| `tui-screen-layout` | 3 | `5de0ea5138b4` |
| `tui-frame-composite` | 8 | `64ad8d6dbc28` |
| `tui-view-row-projection` | 34 | `87d8cc201410` |
| `tui-view-row-decoration` | 36 | `87d8cc201410` |
| `tui-hit-map` | 3 | `5de0ea5138b4` |
| `tui-mouse-input` | 2 | `804f50ad04f0` |
| `tui-agent-activity` | 6 | `64ad8d6dbc28` |
| `tui-paint-cache` | 83 | `87d8cc201410` |

They differ because the closures differ, which is the point of the rule. Note that
none of group B's slices names `bin/tasks`, so none of them inherits the
`9b9e6e9` pin inventory §0 warns the other campaigns about — and none of them is
one of the eight pre-existing drifted records, which are not this pass's to touch.

One consequence of seeding this group worth stating: **`lib/tui/` is no longer
outside every drift closure.** `campaign-10.md` §5 item 3 predicted that seeding
campaign 12 would close the hole it had opened; group B closes fifteen of the
thirty-odd files, and group A's and group C's records close the rest. If any of the
three groups leaves a `lib/tui/*.rb` unnamed, a change there is still reported by
no `drift` run.

---

## Addendum — merge review, 2026-08-02

Four group B claims moved in the merge review, each because the claiming slice
sat upstream of the slice whose behavior the test asserts:

- `tui-screen-layout` lost
  `test_frame_consumes_layout_body_and_viewport_without_recomputing` to
  `tui-frame-composite`: it drives `Tui::Frame`, whose slice depends on
  `tui-screen-layout`, and also `Tui::Views`, which was upstream of neither.
  `tui-frame-composite` took a `tui-view-row-projection` edge with it.
- `tui-frame-composite` lost `test_hit_map_list_cells_match_rendered_row_glyphs`
  to `tui-hit-map`, which depends on it.
- `tui-view-row-decoration` lost the three `test/test_theme.rb` note-line tests
  to `tui-task-detail-content`, which depends on it. The consequence is recorded
  on both records: this slice's note styling now has no local oracle at all.
- `tui-paint-cache` took a `tui-task-detail-content` dependency for its two
  `test_detail_refresh_*` refs, and took the row-cache reset contract plus its
  two Ruby-only tests from `tui-app-shell`, where the clause had no portable
  oracle and here it has one (`test_clear_row_caches_drops_stale_row_list`).

`tui-view-row-projection` also gained four `test/test_lead_matrix.rb` refs and
`test/test_views.rb#test_agenda_sorts_timed_items_by_exact_boundary_not_file_order`.
The last reverses this campaign's original exclusion: `query-named-views`
reserved that test for campaign 5, but the ordering it asserts is produced by
`temporal_sort_key` in `lib/tui/views.rb` and both its fixture tasks are plain
deadlines with no hold, so campaign 5's availability model never discriminates
them. Exact-instant agenda ordering is proved here and by nothing in campaign 5.
