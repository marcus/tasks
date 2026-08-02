# CAMPAIGN 12, GROUP A — the app shell and session

Companion to the twelve records in `campaign-12a-records.jsonl`, which are
appended to `porting/manifest.jsonl` centrally. Campaign 12 is **Bubble Tea TUI
behavior**, 832 unclaimed tests — too large for one seeding pass, so it is split
three ways by test file. This file is group **A**: the app shell and session.
Groups **B** (rendering, layout, mouse decoding) and **C** (forms, modals,
pickers) are seeded in parallel by sibling agents and write their own notes.

Prerequisites read in full: `porting/PORTING.md`,
`porting/evidence/wiring/campaign-10.md` (the exemplar, and the source of the
method precedent used here), and
`porting/evidence/wiring/campaigns-5-6-9-12-inventory.md` §2a, §2c, §2d, §3a,
§3b, §3j, §3l, §4, §5d and **Appendix A**.

**Result: 12 slices, 365 tests claimed, 0 fixtures, 0 fixture todos.**

---

## 1. The method decision, and its consequence

This is the first thing to read and the reason campaign 12 reads unlike every
campaign before it. The decision is **Marcus's, already taken**; it is recorded
here so the shape of the records is legible, not re-argued.

### 1a. Why differential conformance cannot see the TUI

`porting/specs/observations.schema.json` records `files.before/after/deltas`,
`process.stdout/stderr/exit_status/signal`, `journal`, `revisions`,
`invocation` and `environment`. Against a TUI, three of those are decisive
absences:

- **There is no frame.** No screen buffer, no cursor position, no cell
  attributes, no scroll region. `process.stdout` captures the raw byte stream
  including escape sequences, which is a *superset* of the frame and a much
  weaker assertion: two implementations that paint the same visible screen with
  different cursor-movement strategies differ byte-for-byte, and a comparison
  that fails on that is not a finding.
- **There is no key vocabulary.** `invocation` has `argv` and `stdin`
  (`bytes_base64`). A TUI session is a *sequence* of keypresses with a repaint
  observed between each; `stdin` as a single blob written up front cannot
  express "press `j`, observe, press `u`, observe" — the reads are interleaved
  with the writes. This is precisely why all 263 tests in `test/test_app.rb` and
  `test/test_app_modals.rb` drive the `App` object directly.
- **There is no terminal, and the schema says so in words.** `invocation.tty` is
  false on all three streams under the copy protocol, and its own description
  reads: *"Note what this field does NOT claim — recording that no stream was a
  terminal is not evidence about what the implementation would print if one
  were."* The TUI does not run without a terminal (`bin/tasks-tui` aborts), and
  the protocol guarantees there is not one.

### 1b. The decision

**No harness extension. No pty. No frame capture. Campaign 12 is proved by
TRANSLATED UNIT TESTS** — the Go port reimplements these tests against the
Bubble Tea implementation, and those tests are the evidence. This is the same
method `agent-request-queue` already uses for the same structural reason, and
`agent-diff-report` uses for a different one.

The reasoning, recorded because a reader deserves the *why* and not just the
*what*: this is a personal task manager. A pty-driven differential frame harness
— a new `surface`, a new capture step in the eleven-step copy protocol, a frame
abstraction in the schema, a `TERM` pin in `determinism.md`, and a normalizer
for cursor-movement strategy — is not worth its cost against 832 tests that
already exist and already run.

### 1c. The consequence, stated so nobody reads the absence as an oversight

**A green conformance run says nothing whatever about the TUI.** No case in
`porting/cases/` will ever exercise a keypress, a repaint, or a modal. If every
fixture passes and every campaign-12 slice is `not_started`, the corpus is still
green — which is exactly what it should report, and exactly what a reader must
not mistake for coverage.

Therefore, in every one of the twelve records:

- `"fixtures": []`
- `"fixtures_todo": null`
- `oracle_gaps[0]` is the method paragraph, verbatim and identical across all
  twelve, stating the method, the schema facts behind it, the decision, and this
  consequence.

There is deliberately **no** `fixtures_todo` asking for a pty anywhere in group
A. `campaign-10.md` §5 shows what a `fixtures_todo` is for — a buildable
fixture, described precisely enough to build. "Add a pty to the runner" is not
that; it is a decision that was taken the other way.

Two second-order consequences worth naming:

1. **The `ruby_tests` list IS the specification.** With no fixture and no case
   list, a slice's only written statement of what it must do is the set of tests
   it claims. Every record here is therefore precise and complete about that
   list, and the slices are deliberately coarse — twelve, not thirty — so that a
   port has a behavior to implement rather than a fragment.
2. **The tier table's high-risk steps partly do not apply.** Four slices are
   tiered `high` because they write. There is no way to crash a Go TUI
   mid-write from outside and observe the store, and no way to race two TUIs
   through the case protocol. What the high tier buys here is the review
   discipline — two independent reviews, independent approval — not the fault
   injection. Each high record says so in its own gaps.

---

## 2. Where the cuts are

Twelve slices. Dependency order (edges into other campaigns and into groups B
and C are shown once, at their first use):

```
tui-app-shell ──┬──> tui-ui-state-and-session ──> tui-input-decoding-and-shortcuts
                │                                          │
                │                                          v
                ├────────────────────────> tui-selection-navigation-and-mouse
                │                                          │
                │                                          v
                ├────────────────────────> tui-filters-and-palettes
                │                             │      │      │      │
                │                             v      v      v      v
                │            tui-task-quick-actions  │  tui-delegation-keys
                │            tui-project-and-archive-actions
                │            tui-agent-prompt-and-queue-integration
                ├────────────────────────> tui-task-editor-integration
                ├────────────────────────> tui-export-and-clipboard
                └────────────────────────> tui-config-keys
```

| Slice | Tests | Risk |
|---|---:|---|
| `tui-app-shell` | 27 | medium |
| `tui-ui-state-and-session` | 36 | medium |
| `tui-input-decoding-and-shortcuts` | 34 | medium |
| `tui-selection-navigation-and-mouse` | 51 | medium |
| `tui-filters-and-palettes` | 42 | medium |
| `tui-task-quick-actions` | 55 | high |
| `tui-delegation-keys` | 24 | high |
| `tui-task-editor-integration` | 29 | high |
| `tui-project-and-archive-actions` | 13 | high |
| `tui-agent-prompt-and-queue-integration` | 33 | high |
| `tui-export-and-clipboard` | 11 | low |
| `tui-config-keys` | 10 | medium |
| **total** | **365** | |

### Why these boundaries

**Coarse on purpose.** With no fixtures, a small slice is a small paragraph and
nothing else. Three natural sub-slices were folded in rather than seeded:

- **Mouse** into `tui-selection-navigation-and-mouse`. Every App-level mouse
  assertion is "this gesture produced this selection or view change" — the same
  behavior the keyboard tests assert, reached through a different decoder. The
  decoder itself (`test/test_mouse.rb`) and the router
  (`test/test_mouse_router.rb`) are group B's.
- **Outline reordering** into `tui-task-quick-actions`. `alt-up`/`alt-down`/
  indent/outdent are quick actions on the selected row like any other, sharing
  the refusal-and-flash path; their distinguishing property (one journal entry,
  `u` restores exact bytes) is campaign 6's journal behavior observed through
  this dispatch.
- **The action palette** into `tui-filters-and-palettes`. The palette is the
  keyless path to actions other slices own; what it adds is *narrowing and
  availability*, which is what the rest of that slice is.

**Session persistence is not split from the mode machine.** `lib/tui/ui_state.rb`
owns both the legality of mode edges *and* the restore/prune validation, so
splitting would put one file's invariants in two slices. `lib/tui/session.rb` is
the file format and rides along.

**Delegation is its own slice at 24 tests**, and would be even at 10. Its
distinguishing content is a refusal vocabulary that exists nowhere else in the
product: `@` is the context-filter key, so a mistyped `@word` is one slip of
muscle memory from the delegate prompt and must be refused *by name*; and a
one-character answer must never resolve to the widest authority (`implement`) or
to the destructive `off`/`none`, which revokes a live claim with no
confirmation. The CLI takes explicit flags, so there is no CLI oracle for the
prefix-resolution table at all.

**`tui-export-and-clipboard` is the only low-tier slice in group A**, because it
is the only behavior here that is a pure function of a task record. Export is
TUI-only — `tasks` has no export verb — which is why the inventory's §3m puts
`test/test_export.rb` in campaign 12 rather than in campaign 8's formatting.

---

## 3. The 19 mixed-file tests, resolved from Appendix A

These live in files campaign 12 does not dominate. They are group A's alone, by
assignment, so the three-way split cannot collide on them. Each is claimed by
exactly one slice.

| File | Tests | Slice | Inventory ruling |
|---|---:|---|---|
| `test/test_config.rb` | 10 | `tui-config-keys` | §3g — the theme/colors/mouse keys are C12, not C8 |
| `test/test_determinism.rb` | 2 | `tui-app-shell` | §3k — `LINES`/`COLUMNS` "has no effect on the CLI" |
| `test/test_schema_v2.rb` | 1 | `tui-app-shell` | §3l — the TUI's own version gate |
| `test/test_links_feature.rb` | 3 | 1 → `tui-selection-navigation-and-mouse`, 2 → `tui-task-quick-actions` | §3m — excluded twice before, by `links-read` and `open-command` |
| `test/test_lead_matrix.rb` | 3 | `tui-selection-navigation-and-mouse` | §3j — renderer/query agreement about hidden rows |

Two of these deserve a sentence each:

- **The lead-matrix three are the renderer half only.** The query side of that
  agreement is campaign 5's, and campaign 5 must not claim these three. The
  inventory's §3j gives the split from the file's own section comments.
- **`test_the_tui_and_the_store_answer_the_version_question_identically` is the
  one place group A claims into a behavior an existing slice already half-owns.**
  The store's half is `check-meta-and-ids`'; what this claims is that the TUI's
  gate agrees. It is claimed by one slice, named, not shared.

`test/test_config.rb`'s split has a further consequence recorded in §5.

---

## 4. `agent-request-queue`'s deferral, discharged — and its "13 more" resolved

`agent-request-queue` (campaign 10) `oracle_gaps` deferred "the rendering and
App integration" to campaign 12 and named two tests as carrying behavior nothing
else does. §3b of the inventory verified both exist and are unclaimed, and
required this pass to claim them. **Both are claimed, by name, in
`tui-agent-prompt-and-queue-integration`:**

- `test/test_app_agent_queue.rb#test_store_reloads_after_completion_before_next_request_starts`
  — the visible checkpoint between runs. `AgentQueue#pump` deliberately does
  *not* start the next request; App reloads task state first. The queue's own
  comment says why, and only this test proves it.
- `test/test_app_agent_queue.rb#test_queued_requests_build_fresh_context_so_a_memory_edit_hits_only_the_second`
  — the `agent_factory` contract that the system context is built at **start**,
  not at enqueue. It is the reason `agent-context-assembly` says the sidecar is
  read fresh on every call.

The same gap also said "**13 more** across `test/test_app.rb` and
`test/test_app_modals.rb`" and **named none of them**. §3b records why that
cannot be re-derived mechanically: a name grep for `agent|prompt|switcher` over
those two files returns 34 tests, most of which are the *delegate* prompt, the
*revert* prompt, or generic prompt-widget rendering.

**Resolution, and a discrepancy reported rather than reconciled.** Reading the
bodies, the tests in those two files that are genuinely the agent prompt and the
queue's App integration number **21**, not 13:

From `test/test_app.rb` (7) — `test_submit_prompt_queues_while_agent_running`,
`test_submit_prompt_ignores_blank_input_without_touching_agent`,
`test_submit_prompt_flashes_when_agent_unavailable`,
`test_submit_prompt_starts_agent_with_selected_model`,
`test_prompt_mode_hides_selection_without_scrolling_to_it`,
`test_panel_closed_tab_focuses_prompt_without_rebinding_selection`, and
`test_footer_roles_marks_wrapped_prompt_continuations` — the last of these is
prompt-line classification for the footer, not a queue test, and is included
because nothing else owns the prompt's rendered lines.

From `test/test_app_modals.rb` (14) — the prompt rendering block
(`test_prompt_expands_up_to_five_lines_when_input_wraps`,
`test_prompt_renders_trailing_space_immediately`,
`test_prompt_cursor_renders_at_insertion_point`,
`test_prompt_wraps_wide_characters_by_terminal_width`,
`test_prompt_width_one_substitutes_wide_grapheme_and_draws_one_cursor`,
`test_prompt_exact_ascii_boundary_draws_cursor_once`,
`test_prompt_single_hint_line_when_not_focused`), the model/provider header block
(`test_model_toggle_cycles_provider_and_model_in_header`,
`test_header_shortens_configured_cursor_model`,
`test_switching_provider_builds_that_adapter_only_when_request_is_submitted`,
`test_model_change_while_active_is_snapshotted_only_for_new_request`), the
prompt-vs-palette focus test
(`test_colon_opens_palette_while_tab_still_focuses_agent_prompt`), and the two
paste-a-ref-into-the-prompt tests (`test_p_pastes_quoted_ref_into_prompt`,
`test_p_from_panel_keeps_it_open_and_appends_to_existing_input`).

The number 13 was an estimate with no list behind it, so it is **not** treated as
authoritative and the records are not trimmed to match it. The obligation is
discharged by naming a set, which is what `slicing.md` §1 item 2 asks for and
what the original gap failed to do. Group A reports the discrepancy; reconciling
the sentence in `agent-request-queue`'s record is a central merge edit (§5).

Two nearby tests are deliberately **not** in that slice:

- `test/test_app.rb#test_dirty_editor_quit_confirmation_also_accounts_for_agent_queue`
  is `tui-task-editor-integration`'s. It reads like an agent test and is a quit
  test: the confirmation lives in the App's quit path and must fire once for
  both a dirty draft and a live request.
- The delegate and revert prompts are `tui-delegation-keys`' and
  `tui-task-editor-integration`'s respectively. They are the bulk of the 34
  tests §3b's grep returns, and the reason that grep could not be trusted.

---

## 5. Existing records this pass does NOT amend, and what it owes

Per the seeding constraint, **`porting/manifest.jsonl` and
`porting/campaigns.jsonl` are untouched**. Three amendments are owed at merge
and are recorded here and inside the relevant record's `oracle_gaps`, in the
shape `campaign-10.md` §6 used:

1. **`config-resolution`, `oracle_gaps[0]`, final sentence.** It currently reads
   *"The rest — the timezone, date-order, theme, mouse and host-context keys —
   belong to campaign 8's CLI and TUI work and to create-basic, and are unclaimed
   on purpose."* Theme and mouse are campaign 12's and were never campaign 8's;
   `tui-config-keys` claims those ten tests. Timezone and date-order become
   campaign 5's (group 5's pass owes the other half of the same sentence). Only
   host-context stays as written. **This is the third time this one sentence has
   gone stale** — `campaign-10.md` §6 already rewrote it once, for the seven
   `prompt.*` tests. Whoever merges should consider replacing the enumeration
   with a pointer, since it goes stale every time a campaign is seeded.

2. **`links-read`, `oracle_gaps[0]`.** It says the three TUI cases are excluded
   because "the TUI is campaign 8". The TUI is campaign 12. `open-command`'s gap
   already says campaign 12 for the identical exclusion, so the two records
   contradict each other today. Group A now claims all three tests, so both
   sentences should name the claiming slices.

3. **`agent-request-queue`, `oracle_gaps` (the campaign 12 exclusion).** "13
   more" should become the enumerated 21 of §4, or a pointer to
   `tui-agent-prompt-and-queue-integration`, plus the two named tests' new owner.
   The count discrepancy should be recorded, not silently corrected.

No existing record's `source_paths` becomes wrong. `lib/tasks/config.rb` gains a
third owner (`config-resolution`, `prompt-facts`, now `tui-config-keys`), which
is the established pattern — `bin/tasks` is named by five slices and
`lib/tasks/store.rb` by nine — and `tui-config-keys`' `notes` says in the
required form that it ports only part of the file.

---

## 6. What this pass measurably changes about drift

Before campaign 12, **45 source files sat in no slice's drift closure at all**,
of which 32 were `lib/tui/*.rb` (`agent_queue.rb` excepted) and two were
`bin/tasks-tui` and `lib/char_width.rb`. `campaign-10.md` §5 item 3 predicted
this and said it was "worth fixing when campaign 12 is seeded".

Group A's twelve records name, and therefore start watching:
`bin/tasks-tui`, `lib/tui/app.rb`, `lib/tui/ui_state.rb`, `lib/tui/session.rb`,
`lib/tui/shortcuts.rb`, `lib/tui/export.rb`, `lib/tui/clipboard.rb`,
`lib/tui/form.rb`, `lib/tui/task_details.rb`, `lib/tui/project_details.rb`,
`lib/tui/context_palette.rb`, `lib/tui/action_palette.rb`,
`lib/tui/agent_activity.rb`, `lib/tui/store.rb`, `lib/tui/dates.rb`,
`lib/tui/theme.rb`, plus `lib/tasks/determinism.rb` and `lib/tasks/config.rb`
(already watched).

Two of those are named for drift and **not** for translation, and both records
say so: `lib/tui/store.rb` and `lib/tui/dates.rb` are four-to-ten-line Ruby
compat shims (`Tui::Store = Tasks::Store`, `Tui::Dates = Tasks::Dates`) with no
Go analogue — the port imports the shared package instead. §3m of the inventory
asks for exactly that statement rather than a pretence of porting them.

Four widget files (`context_palette.rb`, `action_palette.rb`,
`agent_activity.rb`, `theme.rb`) are named here for their App integration while
groups B and C own the widgets themselves. That co-ownership is deliberate: a
closure that does not reach a file does not watch it, and no test is shared.

Group A does **not** name: `lib/char_width.rb`, `lib/term_form*`, and the
remaining `lib/tui/*.rb`. Those belong to groups B and C, and if either group
omits one it stays invisible to `drift`. That is worth checking at merge.

---

## 7. Cross-group dependencies assumed

Group A `depends_on` the following ids, which **do not exist yet** — they are
guesses at what groups B and C will name their slices, using the obvious names
the brief suggested. Every one must be reconciled at merge; if a name differs,
the edit is one string in one record.

| Assumed id | Owner | Used by |
|---|---|---|
| `tui-frame-buffer` | B (`test_frame.rb`) | `tui-app-shell` |
| `tui-screen-layout` | B (`test_screen_layout.rb`) | `tui-app-shell` |
| `tui-theme` | B (`test_theme.rb`) | `tui-config-keys` |
| `tui-views-renderer` | B (`test_views.rb`) | `tui-selection-navigation-and-mouse` |
| `tui-right-panel` | B (`test_right_panel.rb`) | `tui-selection-navigation-and-mouse` |
| `tui-hit-map` | B (`test_hit_map.rb`) | `tui-selection-navigation-and-mouse` |
| `tui-mouse-router` | B (`test_mouse_router.rb`, `test_mouse.rb`) | `tui-selection-navigation-and-mouse` |
| `tui-agent-activity-widget` | B (`test_agent_activity.rb`) | `tui-agent-prompt-and-queue-integration` |
| `tui-tui-lead` | B (`test_tui_lead.rb`) | `tui-selection-navigation-and-mouse` |
| `tui-modal-stack` | C (`test_modal.rb`, `test_modals.rb`) | `tui-ui-state-and-session`, `tui-filters-and-palettes` |
| `tui-context-palette` | C (`test_context_palette.rb`) | `tui-filters-and-palettes` |
| `tui-action-palette` | C (`test_action_palette.rb`) | `tui-filters-and-palettes` |
| `tui-text-input` | C (`test_text_input.rb`) | `tui-input-decoding-and-shortcuts` |
| `tui-form` | C (`test_form.rb`) | `tui-task-quick-actions` |
| `tui-form-renderer` | C (`test_form_renderer.rb`) | `tui-task-quick-actions`, `tui-task-editor-integration` |
| `tui-choice-picker` | C (`test_choice_picker.rb`) | `tui-task-quick-actions` |
| `tui-task-editor-session` | C (`test_task_editor_session.rb`) | `tui-task-editor-integration` |
| `tui-term-form` | C (`test_term_form*.rb`) | `tui-task-editor-integration` |

`tui-tui-lead` is the least confident guess — group B may fold `test_tui_lead.rb`
into its views slice, in which case the edge should point there.

Dependencies on **existing** slices (all real ids in `porting/manifest.jsonl`):
`config-resolution`, `check-meta-and-ids`, `store-snapshot-items`,
`query-list-filters`, `task-view-projection`, `state-transitions`,
`task-placement`, `changeset-apply-basic`, `delegation-assign`,
`delegation-claim-release`, `delegation-record-shape`, `open-command`,
`archive-sweep`, `archive-project`, `project-complete-and-close`,
`section-create-and-rename`, `agent-request-queue`, `agent-context-assembly`,
`llm-provider-registry`.

---

## 8. The blind spot this campaign inherits

`porting/manifest-issues reach` maps **store mutation verb methods** only
(`VERB_OWNERS`). An oracle that reaches downstream through a key handler, a
renderer, or an overlay is invisible to it. `reach` will keep reporting "0
unexplained" no matter what campaign 12 claims — `campaign-10.md` §4g found this
and the inventory's §5e says it applies here with more force.

Reading the test body is the only defense, and it was the method used for every
one of the 365 assignments in this pass. Three assignments turned on it and
would have been wrong from the name alone:

- `test_date_error_clears_only_when_input_changes` sits in
  `test_app_modals.rb`'s prompt block and is about the **date popup's** error
  state → `tui-task-quick-actions`, not the agent prompt.
- `test_escape_prefixed_alt_sequences_stay_atomic_across_split_reads` and
  `test_coalesced_escape_and_non_alt_followup_remain_separate_keys` sit inside
  the outline-ordering block and assert the **key decoder** →
  `tui-input-decoding-and-shortcuts`, not `tui-task-quick-actions`.
- `test_read_panel_resize_steps_one_column_without_identity_change` and its
  clamp sibling sit in the editor block and are **read-mode** panel resizing →
  `tui-selection-navigation-and-mouse`.

---

## 9. Verification

Run against the two files this pass wrote, before merge:

```
12 records, one JSON object per line, key set and ordering copied
  byte-for-byte from porting/manifest.jsonl's first record        ✓
365 ruby_tests, every one a real `def test_` in the named file    ✓
0 duplicates within these records                                 ✓
0 collisions with the 546 refs in porting/manifest.jsonl's 53     ✓
every source_paths entry exists on disk                           ✓
every depends_on id is an existing slice, a group A id, or one of
  the 18 group B/C ids enumerated in §7                           ✓
all 7 wholly-owned test files fully claimed (346 tests):
  test_app.rb 180, test_app_modals.rb 83, test_app_agent_queue.rb 12,
  test_session.rb 23, test_ui_state.rb 13, test_shortcuts.rb 26,
  test_export.rb 9                                                ✓
plus the 19 mixed-file C12 tests from Appendix A                   ✓
every record: fixtures [], fixtures_todo null, source_sha PENDING,
  campaign 12, status not_started, evidence porting/evidence/<id>/ ✓
no fixtures_todo anywhere asks for a pty                          ✓
```

`porting/manifest.jsonl`, `porting/campaigns.jsonl`, all source, all tests and
all fixtures are **unmodified** by this pass, and no git write command was run.
`source_sha` is `PENDING` on all twelve: each slice's closure last-touch commit
is computed centrally at merge, per `manifest.md`, and must **not** be pinned to
HEAD. Note the inventory's §0 item 1 — the tree carries eight pre-existing
drifted slices whose `source_sha` is stale; that is not this pass's to fix, and
anything naming `bin/tasks` resolves to `9b9e6e9` rather than `e75019a3`.

---

## Addendum — merge review, 2026-08-02

The merge that folded groups A, B and C into `porting/manifest.jsonl` executed
§7's reconciliation. Two of its resolutions differ from what this document
predicted, and one was wrong when first applied:

- **`tui-tui-lead` resolved to `tui-view-row-projection`, not to a lead slice.**
  §7 called it "the least confident guess" and said that if group B folded
  `test_tui_lead.rb` into its views slice "the edge should point there" — which
  is what happened, and this document was right. The merge initially pointed it
  at campaign 5's `lead-availability-gate` instead. The review reversed that:
  the three `test/test_lead_matrix.rb` tests that pulled the edge in drive
  `V.view_query`, `V.rows` and `V.availability_for` — `Tui::Views` and nothing
  else, and the file's only TUI require is `tui/views`. All three moved to
  `tui-view-row-projection` and the `lead-availability-gate` edge came off
  `tui-selection-navigation-and-mouse`.
- **The remaining §7 placeholders** resolved as: `tui-frame-buffer` →
  `tui-frame-composite`; `tui-theme` and `tui-border` → `tui-theme-and-border`;
  `tui-views-renderer` → `tui-view-row-projection`; `tui-right-panel` →
  `tui-task-detail-content`; `tui-mouse-router` → `tui-mouse-input`;
  `tui-agent-activity-widget` → `tui-agent-activity`; `tui-modal-stack` →
  `tui-modal-overlay`; `tui-context-palette` and `tui-action-palette` →
  `tui-filters-and-palettes` / `tui-choice-picker-and-palettes`; `tui-text-input`
  → `tui-input-decoding-and-shortcuts`; `tui-form` and `tui-form-renderer` →
  `tui-form-surface`; `tui-choice-picker` → `tui-choice-picker-and-palettes`;
  `tui-term-form` → the three `term-form-*` slices. `tui-screen-layout`,
  `tui-hit-map` and `tui-task-editor-session` were guessed exactly.
- **§4's "21, not 13"** was written back to `agent-request-queue`'s
  `oracle_gaps` by the same review; it had been resolved here and never applied.
  The record now reads 4 + 12 + 21 = 37 with the owning slices named.

The review also removed five `test/test_app.rb` refs from `tui-app-shell`: four
were inverted claims on behavior owned by slices that depend on it (the two
eight-by-six popup tests, the short-footer filter test, the dismissed-notice
archive/history test), and three were Ruby object-model or source-grep
assertions a Go port can neither pass nor fail. `tui-app-shell`'s
`changeset-apply-basic` dependency was also removed: it had been added to
silence a `reach` report that turns out to be a false positive, because the test
asserts the verb is ABSENT and `reach` matched the string inside the refutation.
