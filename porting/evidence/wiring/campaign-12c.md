# CAMPAIGN 12, GROUP C — forms, modals, pickers and text input

Companion to the **eight** records in `campaign-12c-records.jsonl`. Campaign 12
is being seeded by three agents at once against the partition in
`campaigns-5-6-9-12-inventory.md`; this file holds group C's reasoning, in the
shape `campaign-10.md` set — where the cuts are, what is deliberately out of
scope, and above all **what the conformance method cannot see**, which in this
campaign is *everything*.

Group C owns thirteen test files and **206 tests**, all of them whole-file
assignments from the inventory's §2c C12 block. No record touches a mixed file
(group A owns every campaign-12 test inside those) and no record claims a source
file outside group C's surface, with two named and deliberate exceptions
recorded in §4.

**This file writes no manifest record and no campaign record.** The eight lines
are appended by Marcus centrally, together with groups A and B.

---

## 1. The method, decided before the slicing

This is the first thing to read, because it changes what every record means.

Marcus's call at seeding (2026-08): **campaign 12 is proved by TRANSLATED UNIT
TESTS, and the harness is not extended — no pty, no frame capture, no key-input
vocabulary.** It is the method `agent-request-queue` already uses, for reasons
that are the same in kind and worse in degree.

The inventory's §5d states the obstacle; the decision follows from it:

- **There is no field for a rendered frame.** `process.stdout` is a *superset* of
  one and a much weaker assertion. Two implementations that paint the same
  visible screen with different cursor-movement strategies differ byte for byte,
  and a comparison that failed on that would not be reporting a finding.
- **There is no cursor field and no cell-attribute field.** Nineteen of this
  group's tests assert cursor cells, wrapped-row mapping and per-slot styling.
- **There is no input vocabulary for keys.** `invocation.stdin` is one up-front
  byte blob; a form session is inherently interleaved — press Tab, observe that
  focus did *not* move because the buffer is dirty, accept the commit, observe
  that it did. One case is one invocation, and that shape cannot express a
  sequence.
- **`invocation.tty` is false on all three streams** under the copy protocol, and
  the schema says in words that this "is not evidence about what the
  implementation would print if one were".

The reasoning to keep on the record: **a pty-driven differential frame harness is
not worth its cost for a personal task manager.** Building one would mean a
twelfth step in the copy protocol, a `TERM` pin in `determinism.md`, a frame
model in the observation schema and a comparator that knows which cursor moves
are equivalent — a larger and riskier body of work than the TUI it would be
judging, and one that would still not observe the orderings §5 of this file
shows are most of what group C actually proves.

Every one of the eight records therefore carries `"fixtures": []`,
`"fixtures_todo": null`, and an `oracle_gaps` entry whose first sentence is
**"NO CONFORMANCE CASE CAN DRIVE THIS SLICE, AND NONE IS ASKED FOR."** The gap
ends by saying what must not be misread:

> A green conformance run says NOTHING WHATEVER about this slice.

That sentence is the point of the entry. `agent-request-queue` set the precedent
for the shape (no fixtures, no todo, the reason in the record); what is new here
is that the reason is a *decision* rather than a fixture that has not been built
yet, so no record asks for one. **Do not add a `fixtures_todo` to any of these
eight requesting a pty.** It would read as an obligation Marcus has already
declined.

### The consequence for slicing, which is why there are eight and not twenty

Because the proof is translated tests, **the `ruby_tests` list IS the
specification**. There is no differential backstop underneath it: a behavior
absent from the list is a behavior the port is free to get wrong and stay green.
Every record says so as its second `oracle_gap`, and it drove two decisions:

1. **Coarse slices.** A slice is a review unit here, not a conformance unit.
   Eight slices at 15–38 tests each keep each list readable as one acceptance
   criteria document. Twenty slices would have produced twenty partial
   specifications with the seams between them unowned.
2. **Whole test files kept together** wherever the file corresponds to one
   subject, so the list can be re-derived from the source and audited against it.
   The three exceptions are named in §4.

---

## 2. The eight slices, in dependency order

```
term-form-engine ──┬──> term-form-text-fields ──> term-form-choice-date-fields ──┐
                   │            │                              │                │
                   └────────────┴──────────────────────────────┴──> tui-form-surface
                                │
                                └──> tui-choice-picker-and-palettes
                   tui-modal-overlay
                   tui-task-detail-content
term-form-{engine,text-fields,choice-date-fields} ──> tui-task-editor-session
```

| # | id | tests | risk | source |
|---|---|---:|---|---|
| 1 | `term-form-engine` | 30 | medium | `lib/term_form.rb`, `term_form/{support,event,model,form}.rb` |
| 2 | `term-form-text-fields` | 26 | medium | `term_form/{text,fields}.rb`, `tui/text_input.rb` |
| 3 | `term-form-choice-date-fields` | 17 | medium | `term_form/fields.rb` |
| 4 | `tui-form-surface` | 32 | medium | `tui/{form,form_renderer}.rb` |
| 5 | `tui-choice-picker-and-palettes` | 31 | medium | `tui/{choice_picker,context_palette,action_palette}.rb` |
| 6 | `tui-modal-overlay` | 19 | low | `tui/modal.rb` |
| 7 | `tui-task-detail-content` | 18 | medium | `tui/task_details.rb` |
| 8 | `tui-task-editor-session` | 33 | high | `tui/{task_editor_session,task_edit_form}.rb` |

`30+26+17+32+31+19+18+33 = 206`. Every one of the thirteen assigned files is
fully consumed, and the per-file totals match the brief exactly:
`test_term_form` 30, `test_task_editor_session` 29, `test_form_renderer` 19,
`test_modal` 19, `test_term_form_text_fields` 18,
`test_term_form_choice_date_fields` 17, `test_modals` 15, `test_context_palette`
13, `test_form` 11, `test_choice_picker` 10, `test_tui_lead` 9,
`test_action_palette` 8, `test_text_input` 8.

### Why the term_form subsystem is three slices and not one

All three could have been "the TermForm library" — 73 tests, seven small files.
They are three on `campaign-10.md`'s registry/protocol/adapters reasoning: they
fail in unrelated ways and want unrelated reviews.

- **The engine** fails at *ordering*. Its expected defect is the dirty-departure
  guard: a port that lets Tab move focus while a buffer is dirty, instead of
  returning a `CommitRequest` and holding focus until the host accepts or
  rejects, loses the user's edit silently on every tab out of a field.
- **The text fields** fail at *measurement*. Cursor motion counts grapheme
  clusters and rendering counts terminal cells; a port that indexes by rune puts
  the cursor inside a combining sequence, and one that measures by rune count
  splits a wide grapheme across a cell boundary.
- **The choice and date fields** fail at *identity and containment*. A selection
  whose option vanished must be retained and marked invalid rather than dropped;
  Escape must close the picker before it can mean cancel-the-form.

Folding them together would give one slice whose review could not be done by one
person in one sitting and whose risk tier would be the maximum of the three.

`term-form-text-fields` and `term-form-choice-date-fields` both name
`lib/term_form/fields.rb` in `source_paths`. That is the deliberate overlap
`campaign-10.md` §6 recorded for `lib/tasks/config.rb`: the file holds both
families and neither slice ports all of it. **No test is claimed by both.**

The edge `term-form-choice-date-fields ──> term-form-text-fields` is a *true*
edge, not a plausible one: `ChoiceField` and `DateInput` each construct a
`TermForm::TextEditor` for their query and text buffers (`fields.rb:282, 583`).

### Why the form renderer and the popup form are one slice

`Tui::Form` is little more than the lifecycle around `FormRenderer` plus one
`TermForm` field, and `test_form.rb` and `test_form_renderer.rb` assert the same
popup geometry from two sides. Splitting them would put the cell-budget property
under two owners.

### Why `tui-task-editor-session` is one slice and tiered high

`TaskEditorSession` and `TaskEditForm` split by Ruby layering (policy adapter
versus effect coordinator), not by behavior: no test drives the form without the
session except through the session's own accessors, and reviewing either alone
would leave the expectation-binding rule unreviewed. It is **high** because it is
the only slice in group C that writes — every commit goes through
`Tasks::Application` and `Store#patch_task!`.

Its expected defect is the expectation binding. A dirty buffer keeps the semantic
value it was **opened** against, not the one currently in the store, and that is
the entire mechanism by which a blur three minutes later notices someone else
changed the same field instead of clobbering them. A port that re-reads the
expectation at commit time passes every clean-path test and turns every
concurrent edit into a lost update.

---

## 3. The `lib/term_form` boundary, recorded where it will be found

`test/test_term_form_require_boundary.rb` asserts that `require "term_form"`
loads nothing from `lib/tasks`, `lib/tui` or `lib/ansi.rb`, and that
`examples/term_form_demo.rb` names no `Tasks` or `Tui` constant. The inventory's
OOS list excludes both of its tests on the correct ground that they are
properties of Ruby's `$LOADED_FEATURES` with no runtime analogue in Go.

**The architectural rule survives the port and nothing else records it.** So all
three `term-form-*` records carry the same `oracle_gap`: the Go package that
receives TermForm must not import the tasks or tui packages, and Go enforces that
at compile time (an import cycle, or a lint rule over the import graph), not at
runtime. `slicing.md` §1 item 2 is the precedent for why an exclusion stated once
in a wiring note and nowhere else goes missing — `lib/tasks/opener.rb` sat in no
slice for a whole pass exactly that way.

One mechanical detail the rule depends on: the single file outside
`lib/term_form/` that the subsystem reaches is **`lib/char_width.rb`**, the
dependency-free width kernel `Tui::Ansi` also uses. It is deliberately *not* in
any group C `source_paths` — group B's ANSI slice ports it — but it is in all
three term_form closures transitively, so drift watches it either way. That is
the first time `lib/char_width.rb` has been inside any slice's closure at all
(inventory §1a listed it among the 45 files invisible to `drift`).

---

## 4. Three misattribution traps, all of them in this group

Each is written into the relevant record's `oracle_gaps`, not only here.

### 4a. `test/test_modals.rb` does not test `lib/tui/modals.rb`

It `require`s `tui/task_details`, and all fifteen of its tests are named
`test_detail_*`. `lib/tui/modals.rb` is the 43-line help-modal content builder
and is exercised by `test/test_shortcuts.rb` and `test/test_theme.rb` — groups A
and B. The inventory's whole-file C12 table lists `test_modals.rb` by filename
and gives no hint of this. `tui-task-detail-content` claims the fifteen;
`tui-modal-overlay` (which owns `lib/tui/modal.rb`, the *box*) claims none of
them and says so.

### 4b. `test/test_tui_lead.rb` is a cross-surface feature file, split four ways

Nine tests, and only four of them are about a widget group C owns. The file is a
whole-file C12 assignment and landed in group C's list, so all nine are claimed
here — but four assert behavior whose *code* belongs to groups A and B, and the
records say so explicitly rather than pretending otherwise:

| test | claimed by | what it actually drives |
|---|---|---|
| `test_lead_field_sits_beside_recurrence_and_writes_only_lead` | `tui-task-editor-session` | the editor field |
| `test_lead_field_clears_with_off_or_an_empty_buffer` | `tui-task-editor-session` | the editor field |
| `test_lead_field_reports_the_engines_reason_and_the_two_rules_it_can_see` | `tui-task-editor-session` | the editor field |
| `test_committed_lead_renders_as_prose_with_its_resolved_date` | `tui-task-editor-session` | the render model |
| `test_details_panel_and_export_render_the_span_and_the_derived_date` | `tui-task-detail-content` | `TaskDetails` **and `Tui::Export` (group A)** |
| `test_a_lead_gated_row_reuses_the_existing_timed_unavailable_marker` | `tui-task-detail-content` | **`Tui::Views.badge` (group B)** |
| `test_an_idle_tui_notices_a_gate_instant_passing_without_a_reload` | `tui-task-detail-content` | **`Tui::App`'s minute-boundary read-model invalidation (group A)** |
| `test_the_picker_refuses_a_timed_choice_and_releases_one_occurrence_with_now` | `tui-form-surface` | the defer prompt (`Tui::Form`) **through `Tui::App` (group A)** |
| `test_someday_still_holds_a_lead_task_indefinitely` | `tui-form-surface` | same |

**The views, export and app-shell slices must not re-claim any of these four.**
They should point at the group C record that took them. `validate` will not catch
a double claim — sharing a test between two slices is legal — which is exactly
why it is enumerated here and in both records rather than left to be noticed.

The lead-window arithmetic itself (`Tasks::Lead`) is campaign 5's and is being
seeded in parallel; no id exists to depend on yet (§6).

### 4c. `test/test_text_input.rb` is a TermForm test wearing a TUI name

`lib/tui/text_input.rb` is ten lines: `class Tui::TextInput <
TermForm::TextEditor`, a compatibility name so the existing TUI can use the
neutral editor without loading anything from TermForm's host. Its eight tests
exercise the inherited editor. They are claimed by `term-form-text-fields` so one
kernel does not end up under two owners. A Go port very likely has one type here
and no alias at all — a naming difference, not an intentional difference, and
recorded so a reviewer does not go hunting for a second implementation.

---

## 5. What the method cannot see, beyond the frame problem

Worth separating, because a reader who accepts §1 may still assume that a frame
oracle would have closed the gap. For most of group C it would not.

**Most of what these slices prove is a non-change between two states.** Group C's
tests contain, by count, at least twenty-eight assertions of the form "X did
*not* move / did *not* resize / did *not* reorder / was *not* dropped":

- The modal's three anti-jitter invariants — width from the full content, height
  retained while filtering including with no matches, filter and scroll intent
  preserved across a live content replacement. Four tests exist *only* for the
  height rule. A port that recomputes geometry from the visible lines is correct
  on every single frame and wrong in motion.
- The picker's stability set — results do not reorder under cursor motion or
  toggling, geometry never shrinks across refresh, the cursor is preserved by id
  when live options are rebuilt, staged choices survive an external store reload.
- The engine's pending-commit machine — a hidden or disabled pending owner keeps
  semantic focus until the host resolves it, a refresh during pending does not
  re-dirty a clean remote value, a same-field refresh wins over a stale accept.

A single-frame oracle cannot express any of them. The differential protocol's
*one case, one invocation* shape is the wrong shape for these slices, not merely
an underpowered one — which is the second, independent reason the pty was not
worth building.

Three further gaps, recorded on the records that own them:

1. **Unicode table skew** (`term-form-text-fields`). Grapheme segmentation and
   cell width come from `lib/char_width.rb` and Ruby's `each_grapheme_cluster`;
   the tests pin specific combining sequences and wide characters, so a
   disagreement fails — but only for the sequences the tests happen to name.
   Neither `drift` nor any conformance run would notice a Unicode version skew
   between Ruby's build and Go's segmentation package.
2. **A performance contract with no performance gate** (`tui-modal-overlay`).
   `Modal#haystack` memoizes a stripped, downcased view so a keystroke costs one
   substring scan rather than a fresh ANSI strip of every line. Nothing asserts
   it; `metrics.wall_ms` is advisory only and never part of conformance equality
   in either direction, and the slice's `perf_budget` is null. A port that strips
   per keystroke passes every test and makes the agent-activity modal, whose
   content is replaced live, visibly laggy.
3. **`reach` is blind to every edge in this group** (all eight records).
   `VERB_OWNERS` maps store mutation verb methods only, so an oracle reaching
   downstream through a key handler, a renderer or a form transition is invisible
   to the tool, and it will keep reporting zero unexplained reaches no matter
   what campaign 12 claims. `campaign-10.md` §4g found this; inventory §5e
   repeats it for 6, 9 and 12. Reading the test body is the only defence, and it
   is how §4b above was found.

And one gap that runs the other way, worth stating because it is the good news:
**`test_rendering_does_not_sample_terminal_geometry` is the most important test in
group C.** `FormRenderer` is pure and takes its entire cell budget as an
argument, which is precisely what makes the translated-unit-test method viable
without a pty. A Go renderer that read the terminal size itself would pass every
other test in `tui-form-surface` and destroy the method for the whole campaign.

---

## 6. Cross-group and cross-campaign dependencies I assumed

`depends_on` must name real slice ids, so these are listed for reconciliation at
merge. **Seven cross-group ids**, all campaign 12:

| id | group | used by | status |
|---|---|---|---|
| `tui-ansi` | B | 4, 5, 6, 7 | **assumed** — group B unseeded when this was written |
| `tui-theme` | B | 4, 5, 6, 7 | **assumed** |
| `tui-border` | B | 4, 5 | **assumed** |
| `tui-views-render` | B | 7 | **assumed** |
| `tui-app-shell` | A | 4, 7 | confirmed against `campaign-12a-records.jsonl` |
| `tui-input-decoding-and-shortcuts` | A | 5 | confirmed (my working name was `tui-shortcuts`) |
| `tui-export-and-clipboard` | A | 7 | confirmed (my working name was `tui-export`) |

Group A's records were already on disk when this pass finished, so the three
group A ids were reconciled here rather than left for the merge. Group B's four
are still guesses and are the only ids Marcus needs to check. Note also that
group A's records name `lib/tui/form.rb`, `lib/tui/context_palette.rb`,
`lib/tui/action_palette.rb` and `lib/tui/task_details.rb` in their own
`source_paths` alongside `lib/tui/app.rb` — that is the intended overlap (a
closure that does not reach a file does not watch it), and no test is shared.

Existing manifest ids depended on, all of which resolve today:
`changeset-apply-basic`, `state-transitions`, `state-cascade-close`,
`task-view-projection` (all on `tui-task-editor-session`), and
`task-view-projection` on `tui-task-detail-content`.

**Two real edges could not be named and must be added at merge**, both because the
campaigns are being seeded in parallel and have no ids yet. Both are written into
`tui-task-editor-session`'s `oracle_gaps` rather than left to be rediscovered:

- **Campaign 6 (journal / coalescing).** `test_blur_commits_and_one_session_coalesces_history`
  and `test_mutable_identity_coalesce_and_request_strings_are_bound_at_entry` pin
  that a whole editing session is one undo step, keyed by a coalesce key bound
  when the session opens. This slice cannot go green before campaign 6's journal
  slice.
- **Campaign 5 (temporal / recurrence / lead).** `TaskEditForm` injects
  `Tasks::TemporalParser`, `Tasks::Dates`, `Tasks::Recur` and `Tasks::Lead`; this
  slice owns the *seam* and not the grammar. `term-form-choice-date-fields` has
  the same relationship to the injected date parser and says so, and
  `tui-task-detail-content` needs `Tasks::Lead` for the derived opening date.

The tzdb exposure the inventory §5a describes applies in full to
`tui-task-editor-session`'s temporal control: `environment.tzdb_version` is
recorded and never compared, so a zone or DST-gap disagreement between Ruby's
TZInfo and Go's `time/tzdata` is invisible to every automated check on either
side.

---

## 7. Source files claimed, and the two I was unsure about

Claimed by group C (17 distinct files):

```
lib/term_form.rb                lib/tui/form.rb
lib/term_form/support.rb        lib/tui/form_renderer.rb
lib/term_form/event.rb          lib/tui/modal.rb
lib/term_form/model.rb          lib/tui/choice_picker.rb
lib/term_form/form.rb           lib/tui/context_palette.rb
lib/term_form/text.rb           lib/tui/action_palette.rb
lib/term_form/fields.rb         lib/tui/text_input.rb
                                lib/tui/task_details.rb
                                lib/tui/task_editor_session.rb
                                lib/tui/task_edit_form.rb
```

(18 `source_paths` entries across the eight records; `lib/term_form/fields.rb` is
named by two of them.)

**Not claimed, though a filename suggests it should be:**

- `lib/tui/modals.rb` — group A's, per §4a.
- `lib/char_width.rb` — group B's, per §3. In three closures transitively.

**The two I was unsure about**, flagged for the merge:

1. **`lib/tui/task_details.rb`.** Its host is the right panel, whose tests
   (`test/test_right_panel.rb`) are group B's. I claimed the *builder* because
   `test/test_modals.rb` — a group C file — is its only direct oracle, and
   because the Ruby class comment says the output is meant to be hosted by the
   right panel today and by future editing surfaces, i.e. it is deliberately not
   the panel. If group B's right-panel slice also names it in `source_paths`,
   that is the established overlap and no test moves. **If group B claims any
   `test_modals.rb` test, that is a collision and mine should win**, since the
   file is on group C's exclusive list.
2. **`lib/tui/text_input.rb`.** Reasoning in §4c. It is a ten-line subclass and
   could defensibly have gone to a TUI slice; I put it with the kernel it
   inherits from so the editor has one owner.

Nothing else in `lib/tui/` was touched. `lib/tui/right_panel.rb` (the host),
`lib/tui/project_details.rb`, `lib/tui/session.rb`, `lib/tui/ui_state.rb`,
`lib/tui/export.rb`, `lib/tui/clipboard.rb` and the remaining files under
`lib/tui/` are groups A and B's, as are `bin/tasks-tui` and
`lib/tui/generated_themes.rb`.

---

## 8. Manifest records this pass falsifies

Checked against all 53 existing records. **None is falsified**, and that is a
finding rather than an absence of one — group C's surface is the part of the TUI
no existing `oracle_gap` reserved by name. Two nearby sentences deserve mention
so nobody assumes group C handled them:

- **`config-resolution`'s `oracle_gaps[0]`** ("…the timezone, date-order, theme,
  mouse and host-context keys — belong to campaign 8's CLI and TUI work…") is
  the amendment inventory §4 item 1 requires. The theme and mouse half is
  **groups A and B's**, not group C's: no group C slice reads
  `lib/tasks/config.rb` at all, and none names it in `source_paths`. Left alone
  deliberately.
- **`links-read`'s `oracle_gaps[0]`** ("the TUI is campaign 8") is wrong and is
  inventory §4 item 2. Its three TUI cases live in `test/test_links_feature.rb`,
  a mixed file, so they are group A's. Left alone deliberately.

The one record that *mentions* group C's surface is **`agent-request-queue`**,
whose campaign 12 exclusion names `test/test_app_agent_queue.rb`,
`test/test_agent_activity.rb` and "13 more across `test/test_app.rb` and
`test/test_app_modals.rb`". All four of those files are groups A and B's; group C
claims none of them, and the amendment inventory §4 item 6 asks for (enumerate
the 13) is group A's to make.

---

## 9. Verification

Run against the two delivered files, at the tree as of this pass:

```
records                          → 8 lines, valid JSON, key set and ordering
                                   identical to porting/manifest.jsonl's records
ruby_tests claimed               → 206, every one a `path/file.rb#test_name`
                                   whose `def test_name` exists; 0 whole-file
                                   claims
duplicates within these 8        → 0
duplicates against the 53        → 0
slice id collisions              → 0
source_paths resolve             → 18/18 entries, 17 distinct files
depends_on resolve               → every id is an existing manifest slice, a
                                   group C slice, or one of the 7 assumed
                                   cross-group ids listed in 6
fixtures / fixtures_todo         → [] and null on all 8, by decision (1)
oracle_gaps                      → 5-7 entries per slice; the method entry is
                                   first on every one
```

`source_sha` is `"PENDING"` on all eight per the brief. For whoever pins them,
the closure last-touch commits computed the way `manifest.md` prescribes (the
transitive `require_relative` closure of `source_paths`, `git log -1`) are:

| slice | last touch |
|---|---|
| `term-form-engine` | `36716c2e623e80195e3a4fc8cdf39eb7c2c7fc8b` |
| `term-form-text-fields` | `36716c2e623e80195e3a4fc8cdf39eb7c2c7fc8b` |
| `term-form-choice-date-fields` | `36716c2e623e80195e3a4fc8cdf39eb7c2c7fc8b` |
| `tui-form-surface` | `64ad8d6dbc28ca57235119231b0c70b3dd7d6334` |
| `tui-choice-picker-and-palettes` | `64ad8d6dbc28ca57235119231b0c70b3dd7d6334` |
| `tui-modal-overlay` | `64ad8d6dbc28ca57235119231b0c70b3dd7d6334` |
| `tui-task-detail-content` | `87d8cc201410669e5b4ed1987eb44a01946ae92f` |
| `tui-task-editor-session` | `b3d297c32be63ab3ea2802e3900846bf6fa60a23` |

They differ because the closures differ, which is the point of the rule. Note
that none of them is `9b9e6e9` — no group C slice names `bin/tasks`, so group C
is untouched by the pre-existing eight-slice drift the inventory §0 item 1
describes, and none of these should be pinned to HEAD.

---

## Addendum — merge review, 2026-08-02

§6's four assumed group B ids resolved as follows in
`porting/manifest.jsonl`: `tui-ansi` → **`tui-text-metrics`** (it is the slice
that owns `Ansi.strip` and `vislen`), `tui-theme` and `tui-border` → a single
**`tui-theme-and-border`**, and `tui-views-render` →
**`tui-view-row-decoration`**. Every group C record's `notes` was rewritten to
name the real ids and to drop the "must be reconciled at merge" instruction,
which is now executed.

Two group C claims were corrected on the merits by the same review:

- `tui-form-surface` was claiming
  `test/test_tui_lead.rb#test_the_picker_refuses_a_timed_choice_and_releases_one_occurrence_with_now`
  and `#test_someday_still_holds_a_lead_task_indefinitely`. Both drive
  `app.send(:defer_selected)` and assert store writes plus `@flash`, and
  `test_tui_lead.rb` never requires `tui/form`; they moved to
  `tui-task-quick-actions`, which owns defer-until and its ancestor-hold
  reporting.
- `tui-task-editor-session` lost
  `#test_editor_routes_snapshots_and_patches_through_the_application_boundary`
  as unportable — it proves the routing by inspecting Ruby structure — and
  gained the campaign 5 and 6 edges §6 could not name: `timezone-resolution`,
  `temporal-value-instants`, `recur-calendar-grammar`, `lead-span-grammar` and
  `history-coalescing`.
