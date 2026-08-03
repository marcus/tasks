# CAMPAIGNS 5, 6, 9 AND 12 — the shared inventory and the partition

Prerequisite for four seeding passes that run in parallel:

| # | Campaign (playbook, "A proposed Tasks sequence") |
|---|---|
| 5 | Temporal parsing, availability, recurrence, time zones, and calendar edges |
| 6 | Locking, atomic replacement, revisions, rollback, journal, undo/redo, and coalescing |
| 9 | OpenAPI server, ETags, error handling, events, and cross-process concurrency |
| 12 | Bubble Tea TUI behavior |

This file writes **no manifest record and no campaign record**. It is one
mechanical global inventory plus an authoritative partition, so that four agents
seeding at the same time cannot claim the same source file or the same Ruby
test, and so that nothing belonging to 5/6/9/12 falls between them.

Method follows `campaign-10.md` §1: enumerate everything, cross-check against
every existing record and against `closure --json`, then assign. Impressions are
not used anywhere below; every count is reproducible from the commands in §8.

**Baseline: 52 slices, 5 campaigns (2, 3, 4, 7, 10), at `9b9e6e9`.**

**Result: 54 unclaimed source files and 1756 unclaimed tests, partitioned into
eight buckets with no overlap and no remainder.**

---

## 0. Read this first if you are one of the four seeding agents

Three things will cost you a wasted pass if you learn them late.

1. **The repository is currently drifted and the test suite is currently red.**
   `porting/manifest-issues drift` exits **1** with **8 UNRESOLVED slices**, and
   `ruby test/all.rb` reports **1 failure**
   (`test/test_manifest_issues.rb#test_drift_watches_the_transitive_require_closure_not_just_source_paths`).
   Both have one cause: commit `9b9e6e9` ("fix: -p with no words prints only its
   usage line", td-231878) touched `bin/tasks`, which is in eight slices'
   closures, and their `source_sha` is still `e75019a3`. This is **pre-existing
   and is not yours to fix**, and `manifest.md` is explicit that refreshing a
   `source_sha` without re-characterizing is "the same unforgivable move as
   blessing Go output". Your new records must pin *their own* closures'
   last-touch commits, which for anything naming `bin/tasks` is `9b9e6e9` and not
   `e75019a3`. Do not "helpfully" bump the eight.

2. **Campaign 9's oracle does not run in the default gate.** `ruby test/all.rb`
   globs `test/test_*.rb` only — `test/api/` is a separate suite — and it
   actively *aborts* if rack or puma leak into it. The API suite needs Bundler:
   `bundle exec ruby test/api/all.rb` → **108 runs, 2660 assertions, 0
   failures**. Bare `ruby test/api/all.rb` fails at `require "minitest/mock"`.
   Every count in this document for campaign 9 comes from the Bundler run.

3. **Two of the four campaigns are much larger than the other two.** 832 of the
   1756 unclaimed tests and 41 of the 54 unclaimed files are campaign 12. 373
   tests are campaign 5. Campaign 6 has 100 tests and *one* unclaimed file, and
   campaign 9 has 109 tests and four. Slice counts should not come out equal.

---

## 1. The inventory, taken mechanically

### 1a. Source

Every file under `lib/**` and `bin/`: **94 files**. Of those, **40** appear in
some record's `source_paths` and **54 do not**.

`closure --json` watches **49** files — the 40 named plus 9 pulled in
transitively. The nine transitive-only files are exactly the temporal and
journal core:

```
lib/tasks/dates.rb            lib/tasks/temporal_context.rb
lib/tasks/journal.rb          lib/tasks/temporal_parser.rb
lib/tasks/lead.rb             lib/tasks/temporal_value.rb
lib/tasks/operation_context.rb  lib/tasks/timezones.rb
lib/tasks/recur.rb
```

They are watched for drift and ported by nobody. That is the correct shape for
"campaign 5 and 6 have not been seeded yet", and it is why `drift` has stayed
quiet about them.

**45 files sit in no closure at all** and are therefore invisible to `drift`
entirely: `bin/tasks-api`, `bin/tasks-tui`, `lib/char_width.rb`,
`lib/tasks/api/**` (3), `lib/term_form**` (7), and `lib/tui/**` minus
`agent_queue.rb` (32). `campaign-10.md` §5 item 3 predicted this ("`lib/tui/` is
still outside every drift closure except the queue… worth fixing when campaign
12 is seeded"); seeding 9 and 12 closes it, and seeding only some of the TUI
leaves the rest silently unwatched.

### 1b. Tests

**2298** `def test_` across `test/**`, of which **542 distinct** refs are
claimed by the 52 records (546 refs, 4 shared between two slices each, which is
the shape `manifest.md` permits). **Every claimed ref resolves** — zero dangling.

**1756 tests are unclaimed.** One of them,
`test/test_helper.rb#test_mutation`, is a false positive of the enumeration and
is not a test at all: `test_helper.rb:349-351` reopens `class Tasks::Store` and
defines `def test_mutation = StableMutationTestAdapter.new(self)` — a method
monkeypatched onto a **production class**, whose name happens to start with
`test_`. Any future enumeration that greps `def test_` will hit it again. It is
bucketed out of scope in §2 rather than quietly dropped, so the arithmetic
closes.

---

## 2. The partition

Eight buckets. Four are the campaigns being seeded now; two are named campaigns
that are **not** being seeded now; two are residuals. Every unclaimed file and
every unclaimed test is in exactly one.

| Bucket | Meaning | Files | Tests |
|---|---|---|---|
| **C5** | campaign 5 — temporal, availability, recurrence, zones, calendar | 7 | 373 |
| **C6** | campaign 6 — locking, revisions, rollback, journal, undo/redo, coalescing | 1 | 100 |
| **C9** | campaign 9 — OpenAPI server, ETags, errors, events, cross-process | 4 | 109 |
| **C12** | campaign 12 — TUI | 41 | 832 |
| **C8** | campaign 8 — full CLI grammar and human formatting (**not seeded now**) | 0 | 136 |
| **C11** | campaign 11 — native bindings / mobile lifecycle (**not seeded now**) | 0 | 0 |
| **PRIOR** | belongs to an already-seeded campaign (1/2/3/4/7/10) — an existing-slice coverage gap, **not** for 5/6/9/12 to claim | 1 | 125 |
| **OOS** | genuinely unclaimable / out of port scope | 0 | 81 |
| | **total** | **54** | **1756** |

Arithmetic: `7+1+4+41+0+0+1+0 = 54` files. `373+100+109+832+136+0+125+81 = 1756`
tests. Both check.

### Why there is a PRIOR bucket the brief did not ask for

The brief's residual list was C8, C11 and "genuinely unclaimable". Forcing the
125 PRIOR tests into any of those three would be wrong in a way that costs work
later. They are behaviors that **already have an owning slice or an owning
seeded campaign** and were simply not claimed — `Links.extract`'s six
uncovered branches (campaign 3's `links-read`), the delegation invariant checks
in `test_check.rb` (campaign 2), thirteen of the determinism-pin tests (the
campaign 1 harness), the `retitle!`/`set_tags!`/`add_note!`/`move!`/`capture`
store-method blocks in `test_cli_mutations.rb` (campaign 4). Filing them as
"campaign 8" would tell campaign 8's future agent to port `Links.extract`;
filing them as "unclaimable" would be false. They are a real finding — **the
manifest's existing coverage has 125 known holes** — and they are listed so that
a future audit pass (the same shape as td-940935) has a starting list. **No
agent seeding 5/6/9/12 should claim any of them.**

### C11 is empty, and that is the finding

There is no Ruby under `lib/` or `bin/` and no test under `test/` that
corresponds to campaign 11. `grep` for `mobile|ffi|jni|swift|gomobile` over the
Ruby tree returns nothing; the only mentions live in
`docs/plans/deprecated/tasks-go-port-plan.md` §"Native bindings need a smaller API
than HTTP" and §"Phase 6". Campaign 11 is net-new Go work with **no oracle at
all**, so when it is seeded its slices will carry empty `ruby_tests` with an
`oracle_gaps` sentence saying why — the shape `manifest.md` allows. It is
recorded here so that nobody seeding 5/6/9/12 is tempted to file
platform-shaped behavior (locking, signals, atomic rename) under 11: those are
campaign 6's, on real Ruby, today.

### 2a. Source files, in full

**C5 (7 files, 1529 lines)** — `lib/tasks/dates.rb`, `lib/tasks/lead.rb`,
`lib/tasks/recur.rb`, `lib/tasks/temporal_context.rb`,
`lib/tasks/temporal_parser.rb`, `lib/tasks/temporal_value.rb`,
`lib/tasks/timezones.rb`.

**C6 (1 file, 362 lines)** — `lib/tasks/journal.rb`.

**C9 (4 files, 1709 lines)** — `bin/tasks-api`, `lib/tasks/api/app.rb` (1510
lines, the largest single unported file in the tree), `lib/tasks/api/errors.rb`,
`lib/tasks/api/representation.rb`.

**C12 (41 files, 13644 lines)** — `bin/tasks-tui`, `lib/char_width.rb`,
`lib/term_form.rb` + `lib/term_form/{event,fields,form,model,support,text}.rb`,
and all 32 remaining `lib/tui/*.rb` (`agent_queue.rb` is campaign 10's already).

**PRIOR (1 file)** — `lib/tasks/operation_context.rb`. `campaign-10.md` §3
already declined it once with a reason ("it belongs with whichever campaign
seeds the application boundary and is left alone here rather than absorbed
because the name looked close"). Its `SOURCES = %i[cli tui api]` makes it look
like campaign 9's, and `determinism.md` says the operation id "is not written to
the store, the journal, or any current output". Same call as campaign 10's: not
9's, not 12's — it is the application facade's, and no campaign owns that yet.

### 2b. Files the four campaigns will also have to *name*, which are already claimed

`source_paths` overlap between slices is established practice — `bin/tasks` is
named by five slices, `lib/tasks/store.rb` by nine, and `campaign-10.md` §6
records the `config.rb` case deliberately ("two slices name
`lib/tasks/config.rb`, and neither ports all of it"). Each of the four will need
at least one such shared name, and each must add it, not avoid it, because a
closure that does not reach the file does not watch it for drift:

- **C5** — `lib/tasks/store.rb` (`advance_recurrence_records`, `set_recur!`,
  `reschedule!`), `lib/tasks/config.rb` (the `timezone` / `date_order` /
  `time_format` keys), `lib/tasks/check.rb` (`check_lead`,
  `check_temporal_time`; `check-task-fields`' gap already reserves both),
  `lib/tasks/task_queries.rb` (`build_availability`, `effective_gate`,
  `lead_gate`; `query-list-filters`' gap already reserves them),
  `lib/tasks/task_view.rb` (the lead/availability fields
  `task-view-projection`'s gap excludes), `bin/tasks`.
- **C6** — `lib/tasks/store.rb` (`with_lock`, `undo!`/`redo!`, revisions),
  `lib/tasks/application.rb`, `lib/tasks/task_changeset.rb`, `bin/tasks`.
  **Not** `lib/tasks/atomic.rb` — see the contested call in §3f.
- **C9** — `lib/tasks/application.rb`, `lib/tasks/application_read_result.rb`,
  `lib/tasks/task_view.rb` (the *third* tag projection
  `task-view-projection`'s gap names), `lib/tasks/config.rb`.
- **C12** — `lib/tasks/store.rb` (through the `lib/tui/store.rb` adapter),
  `lib/tasks/config.rb` (theme, colors, mouse), `lib/tasks/determinism.rb` (the
  `LINES`/`COLUMNS` winsize pin).

### 2c. Tests: whole-file assignments

These **63** files, carrying **1263** tests, are wholly one bucket. A seeding
agent may treat the file as theirs without reading further. The other 14 files
(493 tests) are split; `1263 + 493 = 1756`.

| Bucket | Files (unclaimed test count) |
|---|---|
| **C9** | `test/api/test_projects.rb` (29), `test/api/test_app.rb` (25), `test/api/test_delegation.rb` (17), `test/api/test_recurrence.rb` (11), `test/api/test_black_box.rb` (9), `test/api/test_toolchain.rb` (9), `test/api/test_lead.rb` (8) — **108** |
| **C12** | `test_app.rb` (180), `test_app_modals.rb` (83), `test_views.rb` (81), `test_ansi.rb` (32), `test_frame.rb` (32), `test_term_form.rb` (30), `test_task_editor_session.rb` (29), `test_theme.rb` (28), `test_shortcuts.rb` (26), `test_session.rb` (23), `test_form_renderer.rb` (19), `test_modal.rb` (19), `test_term_form_text_fields.rb` (18), `test_screen_layout.rb` (17), `test_term_form_choice_date_fields.rb` (17), `test_modals.rb` (15), `test_border.rb` (14), `test_app_paint_perf.rb` (13), `test_context_palette.rb` (13), `test_ui_state.rb` (13), `test_app_agent_queue.rb` (12), `test_hit_map.rb` (12), `test_mouse_router.rb` (12), `test_form.rb` (11), `test_mouse.rb` (11), `test_choice_picker.rb` (10), `test_export.rb` (9), `test_tui_lead.rb` (9), `test_action_palette.rb` (8), `test_text_input.rb` (8), `test_right_panel.rb` (5), `test_agent_activity.rb` (4) — **813** |
| **C5** | `test_recur_calendar.rb` (50), `test_lead.rb` (39), `test_dates.rb` (29), `test_store_recur_calendar.rb` (26), `test_recur.rb` (20), `test_cli_lead.rb` (19), `test_temporal.rb` (12), `test_temporal_queries.rb` (4), `test_create_task.rb` (1) — **200** |
| **C6** | `test_journal.rb` (32), `test_delegation.rb` (1) — **33** |
| **C8** | `test_cli_projects.rb` (7), `test_cli_json_coverage.rb` (11) — **18** |
| **PRIOR** | `test_projects.rb` (4), `test_check.rb` (3), `test_ids.rb` (2), `test_mutation_result.rb` (1) — **10** |
| **OOS** | `test_porting_compare.rb` (27), `test_porting_runner.rb` (27), `test_manifest_issues.rb` (16), `test_porting_determinism_seams.rb` (5), `test_tasks_require_boundary.rb` (3), `test_term_form_require_boundary.rb` (2), `test_helper.rb` (1) — **81** |

The seven OOS files, with the reason each:

- `test_porting_compare.rb`, `test_porting_runner.rb`,
  `test_porting_determinism_seams.rb`, `test_manifest_issues.rb` (75 tests) —
  they test the **porting harness itself** (`porting/compare`,
  `porting/runners/ruby`, `porting/manifest-issues`, the determinism traps).
  Those are campaign 1 tooling that stays Ruby by design and is not part of the
  product being ported. Porting them would mean porting the thing that judges
  the port.
- `test_tasks_require_boundary.rb` (3), `test_term_form_require_boundary.rb`
  (2) — they assert that `lib/tasks/**` never `require`s `rack`/`puma`/`tui`,
  and that `lib/term_form` stands alone. Both are properties of **Ruby's**
  `$LOADED_FEATURES`; Go's import graph enforces the same discipline at compile
  time and has no runtime analogue to assert. The *architectural rule* survives
  the port; the test does not.
- `test_helper.rb#test_mutation` (1) — not a test (§1b).

### 2d. Tests: the 14 mixed files

Fourteen files split across buckets. **Appendix A enumerates every one of their
729 tests by name and bucket.** Counts:

| File | C5 | C6 | C9 | C12 | C8 | PRIOR | total |
|---|---:|---:|---:|---:|---:|---:|---:|
| `test/test_cli_mutations.rb` | 105 | 9 | | | 113 | 24 | 251 |
| `test/test_store.rb` | 33 | 13 | 1 | | | 4 | 51 |
| `test/test_config.rb` | 10 | | | 10 | 1 | 15 | 36 |
| `test/test_store_patches.rb` | 7 | 16 | | | | 13 | 36 |
| `test/test_application.rb` | 1 | 6 | | | | 17 | 24 |
| `test/test_determinism.rb` | | 4 | | 2 | | 13 | 19 |
| `test/test_task_queries.rb` | 8 | | | | | 6 | 14 |
| `test/test_task_changeset.rb` | 2 | 6 | | | | 3 | 11 |
| `test/test_delete_task.rb` | | 5 | | | | 6 | 11 |
| `test/test_tree.rb` | | | | | 4 | 6 | 10 |
| `test/test_task_placement.rb` | | 7 | | | | 2 | 9 |
| `test/test_schema_v2.rb` | 3 | | | 1 | | 3 | 7 |
| `test/test_links_feature.rb` | | | | 3 | | 3 | 6 |
| `test/test_lead_matrix.rb` | 4 | 1 | | 3 | | | 8 |
| **total** | **173** | **67** | **1** | **19** | **118** | **115** | **493** |

Adding the whole-file totals from §2c gives §2's table exactly: C5 `200+173=373`,
C6 `33+67=100`, C9 `108+1=109`, C12 `813+19=832`, C8 `18+118=136`,
PRIOR `10+115=125`, OOS `81+0=81`.

---

## 3. The contested calls

This is the section the brief asked to be exhaustive about, and it is the part
that decides whether four parallel agents collide. Every call has a one-sentence
reason.

### 3a. Undo/redo and the journal (6) vs the TUI's undo affordances (12)

**Call: the journal, the cursor, coalescing, and the byte-restoration semantics
are 6. Every keypress that reaches them, and every rendering of what they did,
is 12.**

Reason: `Tasks::Journal` requires only `atomic` and `config` — it needs nothing
from any surface — while `lib/tui/app.rb`'s undo binding is a key handler whose
observable output is a repaint and a flash message, which is a different
behavior with a different oracle.

Concretely: `test/test_journal.rb`'s 32 unclaimed tests and
`test/test_store.rb`'s seven `test_undo_*`/`test_new_mutation_clears_redo`/
`test_failed_mutation_records_no_history` tests are **6**. `test/test_app.rb`'s
undo keypress tests are **12** and are already inside the whole-file C12 block.
`test/test_journal.rb#test_cli_undo_reverts_a_prior_invocation`,
`#test_cli_undo_with_empty_history_fails_cleanly` and
`#test_cli_undo_refuses_after_out_of_band_edit` are **6**, not campaign 8: they
assert the history outcome through the CLI because that is how the suite reaches
two processes, not because they are about CLI grammar.

### 3b. The agent queue's rendering, already deferred to 12 — verified, and one problem

**Call: honored, with a correction the campaign 12 agent must make.**

`agent-request-queue`'s `oracle_gaps` says the rendering and App integration are
"DELIBERATELY EXCLUDED as campaign 12", and names "29 tests in
`test/test_agent_activity.rb` (4) and `test/test_app_agent_queue.rb` (12), plus
13 more across `test/test_app.rb` and `test/test_app_modals.rb`". It then names
two by hand as carrying behavior nothing else does:
`test_store_reloads_after_completion_before_next_request_starts` and
`test_queued_requests_build_fresh_context_so_a_memory_edit_hits_only_the_second`.
Both exist, both are unclaimed, and both are in C12 here.

The problem: **the "13 more" are a count, not a list.** No record names them, and
they cannot be re-derived mechanically — a name grep for `agent|prompt|switcher`
over those two files returns **34** tests, most of which are the *delegate*
prompt, the *revert* prompt and generic prompt-widget rendering, not the agent
queue. So the exclusion is honored in the only way it can be (all 263 tests in
those two files are C12), but the campaign 12 agent inherits an obligation it
cannot check off. §4 records this as an amendment owed on
`agent-request-queue`.

### 3c. Recurrence and availability (5) vs the read queries campaign 3 already owns

**Call: campaign 3 keeps the query mechanics; campaign 5 takes the tests campaign
3's own `oracle_gaps` already reserved for it, and nothing else.**

Three records reserve work for 5 by name, and all three reservations are honored
here verbatim rather than re-litigated:

- `query-list-filters`: "Availability gating (`TaskQueries#build_availability`,
  `effective_gate`, `lead_gate`) is excluded: it is campaign 5 temporal work."
  → the eight availability tests in `test/test_task_queries.rb` are C5.
- `query-named-views`: agenda's timed-boundary sort
  (`test/test_views.rb#test_agenda_sorts_timed_items_by_exact_boundary_not_file_order`)
  "lands with campaign 5".
  **This is the one place where the reservation and this partition disagree, and
  the disagreement is deliberate:** that test is in `test_views.rb`, which is
  otherwise 81/81 campaign 12, and `lib/tui/views.rb` is a renderer. The call is
  **C12 owns the file; C5 must claim that single test by name across the file
  boundary** — `manifest.md` permits two slices to share a file, and
  `campaign-10.md` set the precedent for claiming into a file another campaign
  dominates. Campaign 12's agent must not treat `test_views.rb` as 81 free tests
  without checking this one.
- `task-view-projection`: "TaskView's lead and availability fields are covered by
  `test/test_lead.rb`, which is campaign 5." → all 39 are C5.

The reverse direction matters more. The six *delegation-scope* tests in
`test/test_task_queries.rb` (`test_delegation_filter_parsing_and_scope_rules`,
`test_delegated_scope_selects_every_marker_and_agent_ready_only_claimable_work`,
`test_agent_ready_excludes_claimed_human_unavailable_and_closed_tasks`,
`test_delegation_scopes_compose_with_the_existing_filters`,
`test_delegation_rides_on_the_canonical_task_resource`,
`test_operation_context_is_typed_and_immutable`) sit beside the availability
tests and read like filter work. They are **PRIOR**, not C5: they are campaign
3/4 delegation-filter behavior with no temporal content, and claiming them
because they share a file with availability is exactly the misattribution
td-940935 removed.

### 3d. API events (9) vs the journal (6)

**Call: 9 owns the transport for history and events; 6 owns the history itself.
And in Ruby, 9 owns almost nothing here, which is the real finding.**

`docs/api/openapi.yaml` documents `/history`, `/history/undo`, `/history/redo`,
`/archive-sweeps` and `/events`. **None of them is routed.**
`lib/tasks/api/app.rb:246` advertises
`capabilities: { projects: true, undo: false, redo: false, archive_sweep: false, events: false }`,
and `test/api/test_app.rb#test_unrouted_capabilities_are_advertised_as_false_and_really_are_absent`
asserts each of the three paths answers **404** for both GET and POST. `/events`
is documented as explicitly deferred ("SSE is deferred behind conditional polling
of `/meta` in v1: every open SSE response pins one thread from Puma's small pool
for its lifetime"), and it has **no test at all**.

So the playbook's word "events" in campaign 9 names a behavior that **does not
exist in the oracle**. Campaign 9's seeding pass must slice the *absence* — the
capability map and the four 404s are real, provable behavior — and record in
`oracle_gaps` that SSE has no Ruby to port and that a Go implementation of
`/events` would be a new feature, which PORTING.md forbids without a Marcus
decision. It must not seed an "events" slice with an empty oracle and a
plausible-sounding behavior sentence.

Consequence for the boundary: `test/api/test_toolchain.rb`'s contract
assertions over `/history/undo` and `/history/redo` (the 409-vocabulary check at
lines 160-180) are **9**, because they assert the OpenAPI document's shape and
not the journal's behavior.

### 3e. Locking (6) vs the API's cross-process concurrency (9)

**Call: the lock is 6. Two processes racing through HTTP is 9. The dividing line
is which side of `Store#with_lock` the test observes from.**

- `test/test_store.rb#test_with_lock_rejects_cross_fiber_contention_and_cleans_up`
  and `#test_shared_store_reads_stay_coherent_across_threads` are **6**: they
  drive `Store` directly and assert lock acquisition and sidecar cleanup.
- `test/test_journal.rb#test_concurrent_writers_do_not_lose_updates` is **6**:
  two `Store` objects, one file, no HTTP.
- `test/api/test_black_box.rb` spawns a real `bin/tasks-api` process
  (`Process.spawn` at line 39) and races an API thread against a CLI thread
  (lines 70-75). That is **9**: what it proves is that the *server* observes a
  CLI write, which is a property of the HTTP adapter's read model and its ETag,
  not of the lock.
- `test/api/test_delegation.rb`'s "concurrency" block (line 459 onwards) is
  **9** for the same reason — it races two HTTP claims and asserts one 409.

The trap to avoid: campaign 6 must not claim `test/api/test_black_box.rb`
because the words "concurrent" and "lock" appear in it, and campaign 9 must not
claim `test_concurrent_writers_do_not_lose_updates` because it looks like the
same property. They are the same *invariant* observed through two adapters, and
the port needs both proofs.

One test crosses in the other direction:
`test/test_store.rb#test_ordinary_read_snapshot_does_not_run_the_api_structural_check`
is **9**, in a file that is otherwise entirely 5/6. It asserts that the ordinary
read path *omits* a check only the API performs — the behavior under test is the
API's, stated as an absence in the store.

### 3f. Atomic replacement: the playbook says 6, but campaign 4 already ported it

**Call: campaign 6 must NOT re-slice atomic replacement. It is already
`store-canonical-write`.**

The playbook's campaign 6 line reads "Locking, **atomic replacement**,
revisions, rollback, journal, undo/redo, and coalescing". But
`store-canonical-write` (campaign 4) has `source_paths: ["lib/tasks/atomic.rb",
"lib/tasks/store.rb"]`, a behavior sentence naming "temp sibling, fsync, symlink
following, permission carry-over, directory fsync", and it already claims the
**8 `test_atomic_write_*` tests at the top of `test/test_journal.rb`** — which is
why that file shows 32 unclaimed of 40 rather than 40 of 40.

This is the highest-cost collision available to campaign 6's agent, because
`test/test_journal.rb` looks like a whole-file claim and is not. Campaign 6 owns
the journal *below* line 119 of that file and nothing above it. `lib/tasks/atomic.rb`
does not go into any campaign 6 `source_paths`; `lib/tasks/journal.rb` does.

### 3g. Date/time config keys vs `config-resolution`

**Call: the ten `timezone` / `date_order` tests are C5; the ten
`theme` / `colors` / `mouse` tests are C12; `urgent_days`, `max_depth` and
`host_context` are PRIOR; `test_cli_config_reports_resolved_host_context` is
C8.**

This is a straight repeat of the `prompt_facts` correction `campaign-10.md` §6
had to make, and it fails the same way if it is not made:
`config-resolution`'s `oracle_gap` currently ends *"The rest — the timezone,
date-order, theme, mouse and host-context keys — belong to campaign 8's CLI and
TUI work and to create-basic, and are unclaimed on purpose."* Once 5 and 12 land,
that sentence is wrong twice: timezone and date-order are campaign 5's (they
decide what `Dates.parse_when` and `TemporalContext` do, and nothing about
formatting), and theme/mouse are campaign 12's, not campaign 8's. §4 records the
amendment.

Both C5 and C12 will need `lib/tasks/config.rb` in `source_paths` for the same
reason `prompt-facts` did: the key-parse rule lives in `config.rb` and is
required *by* it, so a `timezones.rb`-only or `theme.rb`-only closure would not
watch the parse for drift.

### 3h. The temporal CLI verbs (5) vs campaign 8's human formatting

**Call: 105 of the 251 unclaimed `test_cli_mutations.rb` tests are C5.**

The rule applied — and it is the rule `campaign-10.md` used for
`agent-diff-report` ("the heading, the bold/dim styling and the trailing-newline
fixup … are campaign 8's, as all human output is") — is: **a CLI test belongs to
the campaign that owns the semantics it asserts, unless what it asserts is only
the rendering or the registry-wide grammar.**

So `test_cli_recur_*` (24 tests), `test_cli_timed_defer_*`, `test_cli_activate_*`,
`test_cli_someday_*`, `test_cli_due_*`, `test_cli_schedule_*`, `test_cli_undate_*`,
`test_set_date_*`, `test_undate_*`, and the six
`test_cli_*_uses_one_today_for_*` determinism tests are C5. `test_cli_show_human_readable`,
`test_cli_help_prints_reference`, `test_cli_capture_default_inbox` and the
dry-run/exit-status family are C8. `test_cli_lead.rb` is C5 in full, because
every one of its 19 tests asserts a rule (`rule_one` … `rule_five`) rather than a
rendering.

The 24 PRIOR tests in that file are the store-method blocks —
`test_retitle_*`, `test_set_tags_*`, `test_add_note_*`, `test_move_*`,
`test_capture_*` — which are campaign 4's `changeset-apply-basic`,
`task-placement` and `create-basic` behavior tested at the `Store` level in a
CLI-named file.

### 3i. Revision staleness: 6, per `delete-task`'s own reservation

**Call: 6, and campaign 6's agent should read `delete-task`'s gap before slicing.**

`delete-task`'s `oracle_gaps` already names five tests: *"The revision-aware
delete cases (`test_stale_when_a_descendant_changed_between_snapshot_and_cascade_delete`,
`test_strict_check_catches_a_sibling_captured_under_the_same_parent`,
`test_nil_expected_revision_skips_the_check`, `test_matching_revision_deletes_a_leaf`,
`test_malformed_expected_revision_is_invalid`) need revisions, which are campaign
6. Excluded."* All five are C6 here. The other six unclaimed tests in that file
(`test_missing_or_blank_id_is_invalid`, `test_unknown_id_is_not_found`, …) are
plain input validation with no revision content and are **PRIOR**, not C6.

The same reservation appears on `changeset-apply-basic` ("Revision staleness
(`test_changeset_returns_stale_for_*`) is campaign 6"), on `delegation-assign`
and `delegation-claim-release` ("expected-revision checking is campaign 6"), and
on `proposal-decisions`. All honored.

### 3j. The lead matrix, split three ways

**Call: `test/test_lead_matrix.rb` is 4 C5 + 3 C12 + 1 C6.**

The file is named for a campaign 5 feature and is not wholly campaign 5's. Its
own section comments give the split:
`test_flat_tree_and_reveal_stay_aligned_on_a_lead_gated_subtree`,
`test_reveal_shows_the_lead_gated_anchor_and_its_rider` and
`test_the_renderer_fallback_agrees_with_the_query_about_hidden_rows` assert
*renderer* agreement (the file's own comment: "The renderer's date-grained
fallback (used when a renderer has no canonical reader) must agree with the
query about which rows are hidden") → C12.
`test_a_lead_write_conflicts_like_any_other_field` sits under the file's
`-- concurrency --` heading and asserts the revision conflict → C6. The
remaining four (`test_project_header_counts_agree_with_the_read_model`,
`test_the_window_opens_on_the_derived_date_and_not_the_day_before`,
`test_the_window_opens_at_the_first_instant_of_the_derived_date`,
`test_unknown_writers_round_trip_the_keys_and_no_lead_key_is_untouched`) → C5.

### 3k. Determinism pins: split, not wholesale

**Call: 4 to C6, 2 to C12, 13 to PRIOR (campaign 1).**

`test/test_determinism.rb` is harness infrastructure and mostly stays PRIOR, but
three pins are owned by campaigns being seeded now:

- `test_coalesce_scope_pin`, `test_store_factory_defaults_to_a_random_coalesce_scope`,
  `test_store_factory_threads_pins_into_every_store_it_builds`,
  `test_two_pinned_runs_produce_identical_stores_and_journals` → **C6**, because
  `TASKS_PIN_COALESCE_SCOPE` is *persisted into `index.json`* (determinism.md
  says so explicitly) and is therefore journal bytes, not harness state.
- `test_winsize_requires_both_dimensions`, `test_winsize_ignores_nonsense` →
  **C12**, because `LINES`/`COLUMNS` "has no effect on the CLI" (determinism.md)
  and campaign 12 is the only campaign whose conformance depends on it.
- The clock-pin parse tests (`test_now_parses_an_iso8601_instant_as_utc`,
  `test_now_converts_an_offset_instant_to_utc`, `test_blank_pin_is_treated_as_unset`)
  are **PRIOR, not C5**, even though they parse an ISO8601 instant: they test the
  *pin reader*, which is campaign 1's seam, and the product's temporal parsing is
  `TemporalParser`.

### 3l. `test_schema_v2.rb`, split four ways

**Call:** `test_time_metadata_is_canonical_and_checked`,
`test_check_rejects_orphans_shapes_zones_and_dst_gaps`,
`test_store_round_trips_and_undoes_atomic_temporal_patch` → **C5** (zones and
DST gaps). `test_the_tui_and_the_store_answer_the_version_question_identically`
→ **C12**, because the thing under test is that the TUI's own gate agrees, and
the store's half is already `check-meta-and-ids`'. The remaining three →
**PRIOR** (campaign 2's schema gate).

### 3m. Small calls, stated so nobody re-argues them

- `test/test_links_feature.rb`'s three TUI cases → **C12**. They have now been
  excluded twice with a reason (`links-read`, then `open-command`: "The TUI's `o`
  binding shares Opener and is campaign 12"). Its three `test_config_*` cases →
  **PRIOR** (campaign 2 config), not C12.
- `test/test_tree.rb`'s six `test_extract_*`/`test_classify_*`/`test_store_links_*`
  → **PRIOR** (`links-read` coverage holes). Its four `test_cli_*` → **C8**.
- `test/test_export.rb` (9) → **C12**. Export is a TUI capability
  (`lib/tui/export.rb`), not a CLI formatter; `tasks` has no export verb.
- `test/api/test_recurrence.rb` (11) and `test/api/test_lead.rb` (8) → **C9**,
  not C5. `test_recurrence.rb`'s own header says so: *"The grammar itself is
  proven in test_recur.rb and the roll semantics in the store tests; what is
  asserted here is transport."*
- `test/test_cli_json_coverage.rb`'s 11 unclaimed → **C8**, all of them. Nine are
  the registry-wide tests `cli-mutation-json-envelopes`' gap already enumerates
  and reserves for "the final parity slice". Campaigns 5 and 6 landing is what
  *unblocks* that slice; neither may claim the tests.
- `lib/tui/dates.rb` → **C12**, though it is a four-line compat shim
  (`Tui::Dates = Tasks::Dates`) with no Go analogue. C12's record should say so
  rather than pretend to port it; `lib/tasks/dates.rb` is C5's.
- `test/test_application.rb`: the six coalescing/staleness/revision tests → **C6**;
  `test_application_injects_one_today_into_list_view_and_resource_reads` → **C5**;
  the seventeen delegation-facade tests → **PRIOR** (campaign 4).

---

## 4. Existing records that will need amending when 5/6/9/12 land

Not amended here — recorded, with the record id and the sentence.

### Must amend — the sentence becomes factually wrong

1. **`config-resolution`**, `oracle_gaps[0]`, final sentence:
   > "The rest — the timezone, date-order, theme, mouse and host-context keys —
   > belong to campaign 8's CLI and TUI work and to create-basic, and are
   > unclaimed on purpose."

   Timezone and date-order become campaign 5's (10 tests, §3g); theme and mouse
   become campaign 12's (10 tests) and were never campaign 8's. Only
   host-context stays as written. This is the same amendment `campaign-10.md` §6
   had to make to the same sentence for the seven `prompt.*` tests.

2. **`links-read`**, `oracle_gaps[0]`:
   > "…and its three TUI cases are excluded: `open` is a separate command with a
   > launcher seam, and **the TUI is campaign 8**."

   The TUI is campaign 12, not campaign 8. `open-command`'s gap already says
   campaign 12 for the identical exclusion, so the two records currently
   contradict each other — a half-drift of exactly the kind `campaigns.jsonl`
   exists to prevent. Fix regardless of whether campaign 12 claims the tests.

3. **`archive-cli`**, `oracle_gaps` (last):
   > "`undo` and `redo` have no slice, so this slice cannot prove that the three
   > lifecycle commands share one envelope; it proves archive's. The shared shape
   > is a campaign 6 obligation."

   False once campaign 6 is seeded. Replace "have no slice" with the slice ids
   and turn "a campaign 6 obligation" into a named dependency.

4. **`cli-read-json-envelopes`**, `oracle_gaps[2]`:
   > "The mutation envelopes, the lifecycle ones (archive/undo/redo), `open --json`,
   > and the {error, action, message} refusal object are not seeded anywhere yet."

   Already partly stale — `cli-mutation-json-envelopes`, `archive-cli` and
   `open-command` all exist. Campaign 6 makes the undo/redo third stale too.

5. **`cli-mutation-json-envelopes`**, `oracle_gaps[0]`:
   > "…the whole RECIPES table, which includes `recur` and `lead` (campaign 5),
   > `undo` and `redo` (campaign 6), `open` (unsliced) and `-p` (campaign 10). No
   > slice can honestly claim them until those land…"

   Two corrections: `open` is no longer unsliced (`open-command` exists — this
   was already stale before this pass), and once 5 and 6 land, the nine remaining
   registry-wide tests become claimable and the "final parity slice"
   `slicing.md` §5b recommended becomes seedable. That slice is campaign 8's and
   is **not** for any of the four to seed.

### Should amend — the sentence stays true but acquires an id it should name

6. **`agent-request-queue`**, `oracle_gaps` (the campaign 12 exclusion). It says
   "13 more across `test/test_app.rb` and `test/test_app_modals.rb`" without
   naming them, so campaign 12 cannot mechanically discharge the obligation
   (§3b). It should enumerate the 13, in the same style it used for the two it
   did name. `slicing.md` §1 item 2 is the precedent for why an unenumerated
   exclusion goes missing.
7. **`state-transitions`**, `oracle_gaps`: "`test_set_state_is_undoable` … reaches
   the journal, which is campaign 6" → name the slice.
8. **`state-cascade-close`**, `oracle_gaps`: two sentences, one naming campaign 5
   (`test_complete_cascade_retires_recurring_descendant`) and one campaign 6
   (`test_complete_cascade_is_one_undo_step_restoring_bytes`).
9. **`section-create-and-rename`** and **`project-complete-and-close`**: the
   `test/test_projects.rb` undo and recurrence sentences.
10. **`archive-sweep`** and **`archive-project`**: the "undo half … campaign 6's
    journal" sentences on
    `test_schema_v2.rb#test_archive_and_history_against_v1_are_refused_as_an_unsupported_schema`,
    `test_store.rb#test_undo_archive_sweep_restores_both_files`, and
    `test_projects.rb#test_archive_project_moves_the_subtree_and_undo_deletes_a_fresh_archive`.
11. **`delegation-assign`**, **`delegation-claim-release`**,
    **`proposal-decisions`**, **`changeset-apply-basic`**, **`delete-task`**: the
    "expected-revision checking is campaign 6" / "Revision staleness … is
    campaign 6" reservations.
12. **`task-view-projection`**, `oracle_gaps`: "`Representation.task` (the API,
    campaign 9) strips both" → name campaign 9's representation slice, since
    `valid/deferred-tags` pins the divergence and the fixture is shared.
13. **`query-named-views`**, `oracle_gaps`: agenda's timed-boundary sort "lands
    with campaign 5" — needs the C5 slice id **and** a note that the test lives
    in `test_views.rb`, which campaign 12 otherwise owns entirely (§3c).

### `source_paths` amendments — none required, but two files gain a second owner

No existing record's `source_paths` becomes wrong. Two files gain co-owners in
the `prompt-facts`/`config-resolution` pattern: `lib/tasks/config.rb` (C5 and
C12 join `config-resolution` and `prompt-facts`) and `lib/tasks/check.rb` (C5
joins `check-task-fields` for `check_lead`/`check_temporal_time`). Both are
already reserved by the incumbent's `oracle_gaps`, so nothing needs rewriting —
but the new records must say in `notes` that they port only part of the file, as
`campaign-10.md` §6 required.

---

## 5. What the observation schema and determinism.md cannot express

Per the brief: what is unobservable, stated precisely. **No schema extension is
proposed.**

The structural constraint that dominates all four campaigns: **one case is one
invocation**. `porting/runners/README.md`'s case list has `case_id`, `fixture`,
`argv`, `surface`, `cwd`, `env`, `stdin`, `timeout_ms`, `install_journal`,
`copy_root_mode`, `notes` — one argv, one process, one before/after pair. There
is no vocabulary for a second process, a second invocation against the same
copy, an interleaving, or an ordering between them.

### 5a. Campaign 5 — clocks and zones

- **What is well covered.** `TASKS_PIN_NOW`, `TZ` and `TASKS_TIMEZONE` are pinned
  (`TASKS_TIMEZONE` "out-ranks `TZ`" and is pinned to the same value), and
  `Captured [YYYY-MM-DD]` bodies are explicitly *not* normalized. Date-boundary
  behavior is fully observable through store bytes and stdout.
- **`environment.tzdb_version` is recorded and never compared.** determinism.md:
  "Two implementations resolving the same zone against different tzdb releases
  can legitimately disagree about a historical offset." Ruby resolves zones
  through TZInfo and Go through its own `time/tzdata` or the host zoneinfo.
  Nothing in the schema can assert *that the two used the same rules*, only that
  a human re-runs before classifying. Every DST-gap, fold and historical-offset
  test in campaigns 5's 373 is exposed to this.
- **There is no monotonic-clock field, and campaign 5 does not need one.**
  Recorded because campaign 10 flagged the queue's `CLOCK_MONOTONIC` use and a
  reader may expect the same problem here: campaign 5's clock reads are all
  wall-clock and all persisted, so `TASKS_PIN_NOW` covers them. The gap is
  campaign 6's and 12's, below.
- **A pin that resolves but is not *used* is invisible.** determinism.md names
  this as a real, shipped defect class: "`Application` has some thirty methods
  with a `today: Date.today` default parameter, and one adapter call site that
  forgets to pass `today:` produces wall-clock output with `applied: true`
  recorded beside it." The defence is `test/test_porting_determinism_seams.rb`'s
  source-level interception, which is **Ruby-only** — it patches `Date.today` via
  `RUBYOPT`. The Go port has no equivalent and the schema records none, so
  "every temporal call site received the injected today" is unprovable on the Go
  side by conformance alone.

### 5b. Campaign 6 — locks, fsync, and interleaving

- **No field records a lock being *acquired*.** The lock is observable only as a
  side effect: determinism.md's "tempting but not normalized" section keeps
  `.tasks.jsonl.lock` in `files.deltas` precisely so its creation is visible. So
  the harness can see *that a lock file appeared* and cannot see *that mutual
  exclusion held*. `test_with_lock_rejects_cross_fiber_contention_and_cleans_up`
  has no conformance expression at all.
- **fsync is entirely unobservable.** `files.deltas` carries
  `{path, kind, before_sha256, after_sha256}` and `files.after[]` carries mode;
  there is no field for durability, for the temp sibling (determinism.md:
  "Atomic-write temp filenames … are gone before an observation is taken"), for
  the directory fsync, or for the rename itself. A port that writes in place,
  correctly, produces a byte-identical observation to one that writes a temp
  sibling, fsyncs it, fsyncs the parent, and renames. The runner's own §"A
  failing write" says it "buys exactly one crash point — the write — from outside
  the process", not the six the tier table asks for.
- **Multi-process interleaving cannot be expressed.** One case is one invocation
  (above). `test/test_journal.rb#test_concurrent_writers_do_not_lose_updates`,
  `test/test_store_patches.rb`'s six coalescing-break tests (which each require a
  *second* store instance or an *intervening* CLI write against the same copy),
  and `test/test_cli_mutations.rb#test_cli_delete_is_undoable_across_processes`
  are all sequences, not invocations. None can be a conformance case as the
  protocol stands. They are provable by translated unit tests only — the same
  honest-but-not-differential position `agent-diff-report` is in.
- **`files.rolled_back` is null unless the caller asked for `--json`.** The
  runner's known-gaps list says so, and adds that "null means 'not reported' and
  never 'did not roll back'". Campaign 6's rollback slices — `test_journal.rb`
  has eleven `*_failure_*` / `*_rollback_*` tests — are asserting a *decision*
  the observation records as absent in most shapes.
- **The journal index's `org` field is compared, and the copy paths must match.**
  determinism.md solves this by running "both sides against copies at the same
  absolute path (sequentially, or via a per-side mount)". Campaign 6 must not
  discover this late: a cross-path run is compared "with the journal index
  excluded", which excludes precisely the bytes campaign 6 exists to prove.

### 5c. Campaign 9 — HTTP, ETags, concurrent clients

- **`surface: "http"` exists in the schema and does not exist in the runner.**
  The case-list table says `"http"` is "reserved for the phase that ports the
  HTTP adapter", and the runner's known-gaps list says flatly: **"`http` is
  always empty and `surface` is always `cli`."** Campaign 9's first obligation
  is therefore harness work, not translation.
- **The only HTTP field in the whole schema is `revisions.http_etag`.** There is
  no field for the status code, the response headers, the response body (as
  distinct from `process.stdout`), the request method, the path, the request
  headers, or `If-Match`/`If-None-Match`. Every 404/409/412/503 assertion in
  `test/api/`'s 108 tests, and every `assert_contract_response` against
  `docs/api/openapi.yaml`, is currently outside the observation vocabulary.
- **Concurrent clients cannot be expressed** for the same one-case-one-invocation
  reason as campaign 6. `test/api/test_black_box.rb` spawns a server with
  `Process.spawn` and races two threads; `test/api/test_delegation.rb`'s
  concurrency block races two claims. Neither is a case.
- **A long-lived server process has no place in the copy protocol.** Steps 1-11
  are copy → probe → observe → **invoke** → observe → probe → emit. A server that
  must be started, waited for, driven N times and stopped does not fit, and the
  before/after probes assume nothing is holding the store between them.
- **SSE is unobservable and also unimplemented** (§3d). Even if `/events` existed,
  a `text/event-stream` is an open socket producing frames over time, and the
  schema's only stream fields are `process.stdout`/`stderr` captured at exit.

### 5d. Campaign 12 — terminal rendering and keypresses

- **There is no input vocabulary for keys.** `invocation` has `argv` and `stdin`
  (`bytes_base64`). A TUI session is a *sequence* of keypresses with a repaint
  observed between each, and `stdin` as a single byte blob written up front
  cannot express "press `j`, observe, press `u`, observe" — the reads are
  interleaved with the writes. Every one of `test/test_app.rb`'s 180 tests and
  `test/test_app_modals.rb`'s 83 drives the App object directly for this reason.
- **There is no field for a rendered frame.** No screen buffer, no cursor
  position, no cell attributes, no scroll region. `process.stdout` captures the
  raw byte stream including escape sequences, which is a *superset* of the frame
  and a much weaker assertion: two implementations that paint the same visible
  screen with different cursor-movement strategies differ byte-for-byte, and a
  comparison that fails on that is not a finding. `test/test_frame.rb` (32),
  `test/test_ansi.rb` (32) and `test/test_views.rb` (81) all assert over a frame
  abstraction the schema has no name for.
- **Terminal geometry is pinned; the terminal is not.** `LINES`/`COLUMNS` are
  pinned to 40×100 and determinism.md notes they have "no effect on the CLI".
  Nothing pins the terminal *type* — no `TERM` in the pin table — so capability
  differences (which escape sequences are emitted at all) are unpinned. Campaign
  12's first harness question is whether `TERM` becomes a pin; that is a
  determinism.md change and is **not** proposed here.
- **There is no terminal at all, and an in-flight schema change says so in
  words.** Uncommitted work in the tree (§9) adds `invocation.tty` at schema
  version 2, whose description reads: *"Under this protocol all three are false —
  the runner redirects stdin, stdout and stderr to files … Note what this field
  does NOT claim — recording that no stream was a terminal is not evidence about
  what the implementation would print if one were."* That is campaign 12's
  central obstacle stated by the harness itself: the TUI does not run without a
  terminal, and the protocol guarantees there is not one. Campaign 12 needs a
  pty, and a pty is not in the copy protocol's eleven steps.
- **`test/test_app_paint_perf.rb` (13 tests) has no home in the schema.**
  `metrics.wall_ms` is "advisory only; never part of conformance equality in
  either direction", and `peak_rss_bytes` and the CPU fields are documented as
  permanently `null` ("not portably available for a child process"). A repaint
  budget cannot be a conformance assertion; it needs the separate perf gate the
  manifest's `perf_budget` field points at.
- **Mouse input has the same gap as keys, one level worse.**
  `test/test_mouse.rb` (11) and `test/test_mouse_router.rb` (12) decode SGR mouse
  sequences arriving on stdin *during* a session.

### 5e. One gap shared by 6, 9 and 12: `reach` cannot see any of it

`campaign-10.md` §4g found this and it applies with more force here.
`manifest-issues reach` maps **store mutation verb methods** only (`VERB_OWNERS`).
An oracle that reaches downstream through an HTTP route, a key handler, a
renderer, or `Journal#undo` is invisible to the tool. `reach` currently reports
"21 reach(es), 0 unexplained" and will keep reporting zero unexplained no matter
what campaigns 9 and 12 claim. Reading the test body is the only defence, and
each of the four passes should say so in its own wiring note.

---

## 6. What I did not decide

- **Whether campaign 5 or campaign 12 claims
  `test/test_views.rb#test_agenda_sorts_timed_items_by_exact_boundary_not_file_order`
  as a shared test or campaign 5 alone.** I have called the *owner* (campaign 5,
  per `query-named-views`' existing reservation) but two slices sharing one test
  is permitted and may be the honest answer here. The two agents must not both
  claim it silently — `validate` will not catch it, because sharing is legal.
- **Whether `/events` gets a slice at all** (§3d). It is a contract-only path
  with no Ruby. `not_applicable` with a reason, `blocking_cutover`, or no slice
  are all defensible; picking is campaign 9's seeding decision and possibly
  Marcus's.
- **The slice count and the cut lines inside each campaign.** This document
  partitions ownership; it does not slice.
- **Anything about the 8 drifted slices** (§0 item 1).

---

## 7. Recommendations for the four passes

1. **Campaign 6 should read §3f before writing a single record.** Atomic
   replacement is already ported.
2. **Campaign 9 should budget its first slice for the harness, not the code.**
   `surface: "http"` is unimplemented in the runner; until it exists, campaign
   9's 109 tests have no differential path at all and every slice would carry the
   same `fixtures_todo`.
3. **Campaign 12 should decide the frame-vs-stdout question up front** (§5d) and
   record the answer in every slice's `oracle_gaps`, because it decides whether
   any of the 832 tests is conformance-testable or whether the whole campaign is
   translated-unit-test-proved like `agent-request-queue`.
4. **Campaign 5 is the one campaign of the four that is mostly observable
   today** — its behavior is a pure function from (fixture bytes, argv, pinned
   env) to (fixture bytes, stdout, exit status), which is exactly the schema's
   shape. It should not inherit the other three's caveats by copy-paste.
5. **None of the four should seed the CLI-registry parity slice** that
   `cli-mutation-json-envelopes`' gap and `campaign-10.md` §5 both still owe.
   Campaigns 5 and 6 landing is what unblocks it; it belongs to campaign 8.

---

## 8. Verification

Every number above is reproducible. Run against the tree at `9b9e6e9`:

```
porting/manifest-issues validate → ok: 52 slices, 5 campaigns              (exit 0)
porting/manifest-issues drift    → 8 UNRESOLVED, all "1 commit(s) since
                                   e75019a3930f"                           (exit 1) ← pre-existing
porting/manifest-issues reach    → 21 reach(es), 0 unexplained             (exit 0)
porting/manifest-issues plan     → skip=80 total=80, nothing to do
porting/manifest-issues progress → 0/52 at a terminal status
ruby test/all.rb                 → 2190 runs, 32233 assertions,
                                   1 failure, 0 errors, 0 skips        ← pre-existing,
                                   test_drift_watches_the_transitive_require_closure_not_just_source_paths
bundle exec ruby test/api/all.rb → 108 runs, 2660 assertions, 0 failures
```

Inventory:

```sh
# 94 source files; 40 claimed; 54 unclaimed
ruby -rjson -e 'c=File.readlines("porting/manifest.jsonl").flat_map{|l| JSON.parse(l)["source_paths"]}.uniq
a=(Dir.glob("lib/**/*")+Dir.glob("bin/*")).select{|f| File.file?(f)}.sort
puts a.size, c.size, (a-c).size'

# 2298 def test_; 542 distinct claimed; 1756 unclaimed
ruby -rjson -e 'c=File.readlines("porting/manifest.jsonl").flat_map{|l| JSON.parse(l)["ruby_tests"]}.uniq
a=Dir.glob("test/**/*.rb").sort.flat_map{|f| File.readlines(f).grep(/^\s*def (test_\w+[?!]?)/){ "#{f}##{$1}" }}
puts a.size, c.size, (a-c).size, (c-a).inspect'

# 49 files in some closure; 45 in none
porting/manifest-issues closure --json | ruby -rjson -e 'd=JSON.parse($stdin.read)
w=d["slices"].flat_map{|s| s["watched"]}.uniq
a=(Dir.glob("lib/**/*")+Dir.glob("bin/*")).select{|f| File.file?(f)}.sort
puts w.size, (a-w).size'
```

Checked by hand, additionally:

- **No test appears in two buckets.** The assignment is a hash keyed by
  `file#test`; a duplicate key is impossible by construction, and the bucket
  tally sums to 1756 with zero unassigned.
- **No unclaimed test was invented.** Every key came from the enumeration, and
  every claimed ref in the manifest resolves to a real `def test_` (`c-a` above
  is empty).
- **The four `agent-request-queue` / `query-named-views` / `links-read` /
  `delete-task` reservations were checked against the record text**, not against
  memory; §3 quotes each.

---

## 9. The tree moved while this inventory was taken

Recorded because it changes how this document should be used, not as an excuse.

The session began with `git status` clean at `9b9e6e9`. It did not end that way:
**17 files carry uncommitted modifications** made by sibling fleet slots while
this pass ran — `bin/tasks`, `lib/tasks/{application,determinism,links,merge_driver_command,patch_result,store}.rb`,
`porting/compare/{lib/dimensions/cli.rb,validate}`,
`porting/runners/ruby/{probe,run}`, `porting/specs/observations.schema.json`,
`porting/fixtures/valid/link-corpus/README.md`, and four test files. There is
also a `stash@{0}` ("tmpcheck") that was not mine.

**Every count in §1–§4 is pinned to committed `9b9e6e9` with a clean tree.**
Re-running the §8 commands now returns 2310 tests rather than 2298. The twelve
new tests, and where they land in this partition, so nobody has to re-derive
them:

| New test | Bucket | Why |
|---|---|---|
| `test/test_cli_mutations.rb#test_a_failed_write_never_blames_validation` | **C6** | rollback diagnostics, beside `test_a_failed_write_is_reported_as_rolled_back_with_the_file_unchanged` |
| `test/test_cli_mutations.rb#test_a_failed_write_diagnostic_carries_no_path_and_no_exception_text` | **C6** | same |
| `test/test_cli_json_coverage.rb#test_prompt_with_no_words_prints_only_the_usage_line` | PRIOR | campaign 10 `cli-prompt-command`; this is the td-231878 fix that also caused the drift in §0 |
| `test/test_install_merge_driver.rb#test_refused_merge_leaves_working_file_untouched_when_a_side_is_another_schema_version` | PRIOR | campaign 7 `merge-driver-git` |
| `…_when_a_side_is_unparseable`, `…_when_the_merged_result_is_invalid` (same file) | PRIOR | campaign 7 |
| `test/test_jsonl_merge.rb#test_cli_driver_writes_nothing_when_a_side_is_another_schema_version` | PRIOR | campaign 7 |
| `…_when_a_side_is_unparseable`, `…_when_the_merged_result_is_invalid` (same file) | PRIOR | campaign 7 |
| `test/test_tree.rb#test_extract_prefers_the_labelled_form_regardless_of_position` | PRIOR | campaign 3 `links-read`; this looks like the resolution of td-794997, which `links-read`'s own gap left open |
| `test/test_tree.rb#test_extract_keeps_first_occurrence_order_when_a_later_label_wins` | PRIOR | same |
| `test/test_tree.rb#test_extract_keeps_the_first_label_when_a_url_carries_two` | PRIOR | same |

Adopting them: C6 → 102, PRIOR → 135, total → 1768. **No bucket boundary moves**
— nothing new lands in 5, 9 or 12, and the two C6 additions are inside a block
this document already assigns to C6.

Two of the uncommitted changes matter to §5 and are flagged there rather than
here:

- `porting/specs/observations.schema.json` is being taken to **schema version
  2**, adding `process.<stream>.sha256_normalized`, `invocation.tty`, and
  `file_state.kind` (directories become observed entries). `invocation.tty` is
  the sharpest statement of campaign 12's problem anywhere in the tree; §5d
  quotes it. Its own description says there is "no in-place migration from 1 to
  2 and there deliberately is none … The migration is a re-capture."
- `porting/runners/ruby/run` and `probe` are changing with it. §5's claims about
  what the runner does are read from the **committed** README and runner; a
  seeding agent should re-read both, because the surface they will have to
  extend is moving.

The operational point for the four passes: `manifest.jsonl` and
`campaigns.jsonl` are **not** among the modified files, so appending records is
safe — but `campaign-10.md` §6's closing warning applies with more force than
usual. Change only the lines you intend to change, and re-read `HEAD` before
writing, because someone else is in the tree.

---

## Appendix A — the 14 mixed files, every test named

Files not listed here are wholly one bucket; see §2c. Within a mixed file, a
seeding agent's set is exactly the block under its bucket heading.


#### `test/test_application.rb` — 24 unclaimed

**C5** (1):
- `test_application_injects_one_today_into_list_view_and_resource_reads`

**C6** (6):
- `test_every_application_call_gets_a_fresh_store_instance`
- `test_patch_task_preserves_field_scoped_conflicts_without_exposing_a_store`
- `test_keyed_patches_across_fresh_stores_coalesce_into_one_undo_step`
- `test_read_model_reports_staleness_after_an_external_write`
- `test_delegation_honors_an_expected_revision`
- `test_every_delegation_mutation_is_individually_undoable`

**PRIOR** (17):
- `test_factory_keeps_construction_settings_immutable`
- `test_agent_delegation_returns_the_marker_and_the_canonical_resource`
- `test_human_delegation_moves_to_waiting_in_one_undo_step`
- `test_keep_state_opts_out_of_the_waiting_default`
- `test_mode_update_keeps_the_work_ref_and_reports_the_previous_marker`
- `test_replacing_human_with_agent_delegation_and_back`
- `test_agent_delegation_only_clears_a_waiting_the_human_delegation_set`
- `test_agent_delegation_can_keep_the_inherited_waiting_state`
- `test_replacing_a_person_with_agents_undoes_state_and_marker_in_one_step`
- `test_claim_returns_the_full_resource_and_a_lost_race_names_the_holder`
- `test_release_enforces_the_worker_match_and_the_owner_can_force_it`
- `test_release_note_is_appended_in_the_same_undo_step`
- `test_work_ref_is_set_cleared_and_worker_matched`
- `test_undelegate_clears_the_marker_and_leaves_lifecycle_alone`
- `test_delegation_refuses_proposed_and_closed_tasks`
- `test_delegation_accepts_prebuilt_typed_commands`
- `test_checked_result_owns_an_immutable_copy_of_plain_payloads`

#### `test/test_cli_mutations.rb` — 251 unclaimed

**C5** (105):
- `test_set_date_replaces_existing_deadline`
- `test_set_date_adds_deadline_when_item_has_none`
- `test_set_date_deadline_ignores_existing_scheduled`
- `test_set_date_promotes_inbox_to_todo`
- `test_set_date_rejects_stale_line_numbers`
- `test_set_date_is_undoable`
- `test_set_date_scheduled_kind_sets_scheduled_not_deadline`
- `test_undate_removes_both_when_no_kind`
- `test_undate_removes_specific_kind_only`
- `test_undate_returns_false_when_no_matching_stamp`
- `test_undate_rejects_stale_line_numbers`
- `test_undate_never_deletes_prose_mentioning_a_stamp_keyword`
- `test_undate_is_undoable`
- `test_cli_defer_tags_and_hides_from_active_views`
- `test_cli_defer_synonym_snooze`
- `test_cli_deferred_task_dropped_from_next_and_default_list`
- `test_cli_list_deferred_shows_only_deferred`
- `test_cli_activate_clears_defer_tag`
- `test_cli_defer_dry_run_writes_nothing`
- `test_cli_timed_defer_sets_available_from_preserves_due_and_clears_hold`
- `test_cli_timed_defer_promotes_inbox_and_is_one_undoable_change`
- `test_cli_timed_defer_hides_active_views_and_unavailable_review_finds_it`
- `test_cli_timed_defer_json_reports_derived_availability`
- `test_cli_timed_defer_dry_run_is_hypothetical_and_writes_nothing`
- `test_cli_timed_defer_rejects_bad_date_without_writing`
- `test_cli_timed_defer_accepts_relative_date_forms_as_checked_undoable_changes`
- `test_cli_timed_defer_dry_run_and_output_report_inherited_effective_blockers`
- `test_cli_someday_is_canonical_indefinite_alias_and_rejects_a_date`
- `test_cli_someday_noop_does_not_consume_an_undo_entry`
- `test_cli_someday_and_scheduled_undate_are_independently_undoable`
- `test_cli_timed_defer_include_done_preserves_closed_lifecycle`
- `test_cli_activate_clears_future_available_from_and_hold_but_preserves_due`
- `test_cli_activate_retains_past_available_from_history`
- `test_cli_activate_preserves_scheduled_only_recurrence_and_undo_restores_date`
- `test_cli_activate_reports_remaining_ancestor_hold`
- `test_cli_list_someday_filters_own_marker_not_inherited_blockers`
- `test_cli_unavailable_rejects_non_open_scopes_and_filter_conflicts_are_clear`
- `test_cli_defer_ambiguous_exits_2`
- `test_cli_done_on_deferred_task_clears_defer_tag`
- `test_cli_list_tags_scheduled_start_with_tilde`
- `test_cli_id_json_includes_canonical_derived_availability`
- `test_cli_agenda_json_sorted_by_date_then_priority`
- `test_cli_due_sets_deadline`
- `test_cli_schedule_repairs_its_own_malformed_date`
- `test_cli_schedule_uses_one_today_for_parse_mutation_and_json`
- `test_cli_due_supports_floating_and_fixed_time_values`
- `test_cli_temporal_flags_validate_dst_gap_and_fold`
- `test_post_mutation_json_uses_the_configured_zone_for_floating_times`
- `test_read_reports_a_safe_error_when_a_zone_change_creates_a_floating_gap`
- `test_cli_capture_supports_independent_temporal_modes`
- `test_cli_undate_removes_a_malformed_date_stamp`
- `test_cli_schedule_sets_scheduled`
- `test_cli_schedule_ambiguous_exits_2`
- `test_cli_undate_removes_deadline`
- `test_cli_undate_kind_flag_removes_only_that_kind`
- `test_cli_undate_nothing_to_remove_exits_1`
- `test_cli_undate_bad_kind_exits_1`
- `test_cli_undate_dry_run_writes_nothing`
- `test_cli_show_human_availability_covers_own_inherited_hold_timed_and_closed_states`
- `test_cli_proposal_decision_uses_one_configured_temporal_context`
- `test_cli_capture_uses_one_today_for_parse_create_log_and_json`
- `test_cli_recurring_completion_uses_one_today_for_advance_log_and_json`
- `test_cli_recur_sets_cookie_from_friendly_word`
- `test_cli_recur_from_schedule_uses_fixed_prefix`
- `test_cli_recur_off_clears`
- `test_cli_recur_undated_task_without_on_exits_1`
- `test_cli_recur_undated_task_with_on_seeds_date`
- `test_cli_recur_bad_interval_exits_1`
- `test_cli_recur_no_match_exits_2`
- `test_cli_recur_dry_run_writes_nothing`
- `test_cli_recur_json_includes_recur_field`
- `test_cli_recur_natural_phrase_lands_a_canonical_calendar_cookie`
- `test_cli_recur_canonical_calendar_input_passes_through`
- `test_cli_recur_one_hop_calendar_prefix_is_kept`
- `test_cli_recur_from_with_a_bare_calendar_schedule_points_at_the_plus_prefix`
- `test_cli_recur_from_with_a_one_hop_calendar_schedule_points_at_dropping_it`
- `test_cli_recur_from_still_switches_a_bare_interval`
- `test_cli_recur_rejects_an_interval_prefix_on_a_calendar_schedule`
- `test_cli_recur_unsatisfiable_schedule_is_refused_with_the_store_reason`
- `test_cli_recur_with_on_refused_schedule_writes_nothing`
- `test_cli_recur_with_on_is_one_undoable_transaction`
- `test_cli_recur_json_write_carries_the_humanized_value_and_next_date`
- `test_cli_recur_without_a_schedule_previews_occurrences_without_writing`
- `test_cli_recur_preview_honors_count_and_json`
- `test_cli_recur_preview_with_count_one_is_just_the_stamp`
- `test_cli_recur_preview_on_a_task_without_recurrence_says_so`
- `test_cli_recur_preview_reports_an_unprojectable_stored_schedule`
- `test_cli_recur_preview_rejects_write_only_flags`
- `test_cli_recur_explain_renders_canonical_human_and_dates`
- `test_cli_recur_explain_json_emits_the_engine_payload`
- `test_cli_recur_explain_rejects_unreadable_input`
- `test_cli_recur_explain_reports_a_schedule_that_never_fires`
- `test_cli_recur_explain_off_reports_the_clearing_form`
- `test_cli_done_rolls_recurring_task_forward`
- `test_cli_done_recurring_parent_prints_roll_and_does_not_cascade`
- `test_cli_capture_recur_lands_scheduled_and_repeating`
- `test_cli_capture_recur_accepts_a_calendar_phrase`
- `test_cli_capture_recur_rejects_unreadable_input`
- `test_cli_capture_recurrence_is_one_undoable_transaction`
- `test_cli_list_recurring_filters`
- `test_cli_list_recurring_shows_the_humanized_schedule`
- `test_cli_state_done_is_recurrence_aware`
- `test_cli_recur_off_on_undated_is_noop_success`
- `test_cli_recur_on_closed_task_rejected`
- `test_cli_capture_recur_with_done_state_rejected`

**C6** (9):
- `test_changeset_result_carries_immutable_post_write_read_snapshot`
- `test_cli_schedule_repair_is_one_journal_entry_undone_to_invalid_bytes`
- `test_cli_reports_a_post_write_rollback_from_the_application_store`
- `test_cli_tag_patch_preserves_legacy_tag_order_and_undo_label`
- `test_cli_undate_both_dates_remains_one_undoable_change_with_legacy_label`
- `test_cli_patch_adapter_maps_vanished_id_to_stale_while_initial_missing_ref_is_exit_two`
- `test_cli_delete_is_undoable_across_processes`
- `test_cli_claim_race_leaves_exactly_one_holder`
- `test_a_failed_write_is_reported_as_rolled_back_with_the_file_unchanged`

**C8** (113):
- `test_cli_done_marks_done_with_closed_stamp`
- `test_cli_done_ambiguous_exits_2_not_1`
- `test_cli_done_synonyms`
- `test_cli_done_dry_run_writes_nothing`
- `test_cli_help_prints_reference`
- `test_cli_unknown_command_exits_1_with_help`
- `test_cli_list_json_shape_and_filters`
- `test_cli_list_json_empty_result_is_empty_array`
- `test_cli_list_unknown_flag_keeps_legacy_clean_error`
- `test_cli_list_json_includes_archived_with_source`
- `test_cli_quadrants_json_adds_quadrant_field`
- `test_cli_quadrants_honors_urgent_days_window`
- `test_cli_inbox_and_next_json`
- `test_cli_json_query_paths_match_reusable_query_results`
- `test_cli_capture_missing_flag_value_exits_1`
- `test_repair_refuses_a_target_on_line_one_where_meta_errors_land`
- `test_repair_still_works_once_a_meta_record_holds_line_one`
- `test_cli_help_and_dispatch_no_longer_carry_a_migrate_command`
- `test_cli_mutation_refuses_when_a_different_record_is_invalid`
- `test_cli_note_with_em_dash_under_non_utf8_locale`
- `test_cli_note_refuses_without_a_readable_append_baseline`
- `test_cli_priority_clears_with_none`
- `test_cli_presentation_updates_correct_a_proposal_and_remain_undoable`
- `test_cli_ref_reports_a_known_proposal_outside_an_open_only_scope`
- `test_cli_state_reopens_done_item`
- `test_cli_ref_no_match_exits_2`
- `test_cli_ref_ambiguous_exits_2`
- `test_cli_unknown_flag_exits_1`
- `test_cli_duplicate_titles_report_the_right_task`
- `test_cli_bad_state_exits_1`
- `test_cli_cancel_marks_cancelled`
- `test_cli_cancel_alias_drop`
- `test_cli_cancel_no_match_exits_2`
- `test_cli_show_human_readable`
- `test_cli_show_prints_notes`
- `test_cli_show_json`
- `test_cli_show_project_skips_closed_ancestors`
- `test_cli_show_closed_item_includes_closed_date`
- `test_cli_show_ref_no_match_exits_2`
- `test_cli_show_ref_ambiguous_exits_2`
- `test_cli_retitle_replaces_title`
- `test_cli_retitle_alias_rename`
- `test_cli_retitle_missing_title_exits_1`
- `test_cli_tag_adds_and_removes`
- `test_cli_tag_removes_context`
- `test_cli_tag_bad_spec_exits_1`
- `test_cli_tag_dry_run_writes_nothing`
- `test_cli_note_appends_line`
- `test_cli_note_dry_run_writes_nothing`
- `test_cli_move_relocates_and_reports_new_headline`
- `test_cli_move_unknown_section_exits_1`
- `test_cli_move_before_infers_anchor_section_and_preserves_subtree`
- `test_cli_move_before_explicit_task_parent_reparents_at_exact_slot`
- `test_cli_move_before_positional_section_reparents_at_exact_slot`
- `test_cli_move_before_dry_run_is_human_readable_and_writes_nothing`
- `test_cli_move_before_noop_writes_no_journal_entry`
- `test_cli_move_before_is_one_undoable_integrity_checked_change`
- `test_cli_move_before_rejects_wrong_parent_anchor_and_contradictory_destinations`
- `test_cli_move_before_rejects_missing_value_section_self_cycle_and_depth`
- `test_cli_move_before_missing_and_ambiguous_task_refs_exit_two`
- `test_cli_move_before_resolves_all_refs_then_checks_cycle_before_anchor_parent`
- `test_cli_move_before_include_done_and_line_refs_resolve_to_stable_ids`
- `test_cli_legacy_move_forms_still_append`
- `test_cli_capture_default_inbox`
- `test_cli_capture_with_flags_lands_processed`
- `test_cli_capture_adds_configured_host_context_and_can_suppress_it`
- `test_cli_capture_dry_run_uses_configured_host_context`
- `test_cli_capture_explicit_state_overrides_date_default`
- `test_cli_capture_unknown_project_exits_1`
- `test_cli_capture_bad_date_exits_1`
- `test_cli_capture_unknown_flag_exits_1`
- `test_cli_capture_dry_run_writes_nothing`
- `test_cli_cancel_accepts_repeatable_note_visible_in_show`
- `test_cli_show_and_delete_resolve_proposals_without_unrelated_flags`
- `test_cli_state_cannot_strand_accepted_child_beneath_new_proposal`
- `test_cli_done_on_parent_prints_every_cascaded_headline`
- `test_cli_list_survives_a_non_string_id`
- `test_cli_mutation_on_invalid_file_hints_at_check`
- `test_cli_capture_bootstraps_an_empty_store`
- `test_cli_capture_under_happy_path`
- `test_cli_capture_under_and_project_conflict_exits_1`
- `test_cli_capture_under_unknown_ref_exits_2`
- `test_cli_capture_under_over_cap_exits_1_with_depth_message`
- `test_cli_move_under_happy_path`
- `test_cli_move_top_happy_path`
- `test_cli_move_two_destinations_exits_1_usage`
- `test_cli_move_under_own_child_exits_1_cycle`
- `test_cli_move_top_refuses_a_rootless_ancestor_chain_without_hanging`
- `test_cli_note_appends_under_a_c_locale_to_a_non_ascii_body`
- `test_cli_fuzzy_ref_tolerates_a_nil_title_record`
- `test_cli_delete_leaf_reports_and_exits_zero`
- `test_cli_delete_refuses_a_parent_without_cascade`
- `test_cli_delete_cascade_reports_every_removed_task`
- `test_cli_delete_dry_run_writes_nothing`
- `test_cli_delete_unresolved_ref_exits_two`
- `test_config_reports_the_sibling_memory_path`
- `test_cli_delegate_hands_work_to_the_agent_pool_and_to_a_person`
- `test_cli_delegate_rejects_bad_usage_and_ineligible_tasks`
- `test_cli_worker_id_falls_back_to_the_environment_and_the_flag_wins`
- `test_cli_release_appends_a_blocker_note_and_the_owner_can_force_it`
- `test_cli_workref_records_replaces_and_clears_one_reference`
- `test_cli_workref_clears_on_both_off_and_none`
- `test_cli_workref_refuses_an_unbounded_reference`
- `test_cli_refuses_control_characters_in_identity_fields`
- `test_cli_delegate_refuses_a_bare_at_prefixed_word_as_a_person`
- `test_cli_delegation_refuses_invalid_utf8_argv_without_a_backtrace`
- `test_cli_tolerates_an_unknown_delegation_key_from_a_newer_binary`
- `test_cli_undelegate_revokes_a_claim_and_repeats_without_burning_history`
- `test_cli_list_delegation_scopes_render_and_compose`
- `test_cli_agent_ready_json_carries_everything_a_heartbeat_needs`
- `test_cli_show_marks_a_delegated_task`
- `test_json_refusal_of_an_invalid_store_reports_that_nothing_was_written`
- `test_a_refusal_without_json_still_prints_nothing_on_stdout`

**PRIOR** (24):
- `test_retitle_replaces_title_only`
- `test_retitle_preserves_headline_without_tags`
- `test_retitle_rejects_stale_line_numbers`
- `test_retitle_is_undoable`
- `test_set_tags_adds_and_removes`
- `test_set_tags_add_is_idempotent`
- `test_set_tags_can_remove_all_leaving_bare_headline`
- `test_set_tags_rejects_stale_line_numbers`
- `test_add_note_appends_body_line`
- `test_add_note_lands_within_the_block_not_the_next_task`
- `test_add_note_rejects_stale_line_numbers`
- `test_add_note_is_undoable`
- `test_add_note_accepts_binary_tagged_utf8`
- `test_move_relocates_block_under_target_section`
- `test_move_carries_the_whole_block`
- `test_move_returns_false_for_unknown_section`
- `test_move_rejects_stale_line_numbers`
- `test_move_is_undoable`
- `test_capture_adds_inbox_item_by_default`
- `test_capture_accepts_binary_tagged_utf8`
- `test_capture_with_date_lands_as_todo`
- `test_capture_under_named_project`
- `test_capture_rejects_an_unknown_project`
- `test_capture_is_undoable`

#### `test/test_config.rb` — 36 unclaimed

**C12** (10):
- `test_theme_defaults_with_no_colors`
- `test_theme_and_colors_from_config_file`
- `test_generated_theme_name_from_config_file`
- `test_tasks_theme_env_beats_config_file`
- `test_no_color_env_selects_mono_unless_theme_is_explicit`
- `test_mouse_defaults_on`
- `test_mouse_off_from_config_file`
- `test_tasks_mouse_env_beats_config_file`
- `test_for_dir_defaults_mouse_on`
- `test_bare_color_dot_key_is_ignored`

**C5** (10):
- `test_timezone_resolution_precedence_and_time_format`
- `test_invalid_timezone_env_falls_through_to_config_zone_with_a_warning`
- `test_timezone_uses_tz_and_detector_reports_utc_fallback`
- `test_date_order_defaults_to_mdy`
- `test_date_order_from_config_file`
- `test_date_order_config_file_value_is_case_insensitive`
- `test_date_order_env_overrides_config_file`
- `test_date_order_env_is_case_insensitive`
- `test_date_order_invalid_config_value_is_dropped`
- `test_date_order_invalid_env_falls_through_to_config_file`

**C8** (1):
- `test_cli_config_reports_resolved_host_context`

**PRIOR** (15):
- `test_for_dir_pins_memory_beside_the_sandbox`
- `test_urgent_days_invalid_config_value_falls_back_to_default`
- `test_urgent_days_invalid_env_falls_back_to_config`
- `test_urgent_days_empty_env_is_ignored`
- `test_for_dir_uses_default_urgent_days`
- `test_max_depth_zero_falls_back_to_default`
- `test_max_depth_negative_falls_back_to_default`
- `test_max_depth_non_numeric_falls_back_to_default`
- `test_max_depth_invalid_env_falls_back_to_config`
- `test_max_depth_empty_env_is_ignored`
- `test_for_dir_uses_default_max_depth`
- `test_host_context_prefers_full_hostname_then_short_label`
- `test_host_context_ignores_unmatched_and_malformed_rows`
- `test_for_dir_has_no_host_context_and_does_not_call_hostname`
- `test_cli_mutation_lands_in_configured_dir`

#### `test/test_delete_task.rb` — 11 unclaimed

**C6** (5):
- `test_stale_when_a_descendant_changed_between_snapshot_and_cascade_delete`
- `test_strict_check_catches_a_sibling_captured_under_the_same_parent`
- `test_nil_expected_revision_skips_the_check`
- `test_matching_revision_deletes_a_leaf`
- `test_malformed_expected_revision_is_invalid`

**PRIOR** (6):
- `test_missing_or_blank_id_is_invalid`
- `test_unknown_id_is_not_found`
- `test_archived_only_id_is_not_found_and_archive_is_left_alone`
- `test_section_id_is_invalid_delete_targets_tasks`
- `test_invalid_file_is_store_invalid_and_writes_nothing`
- `test_application_deletes_through_the_facade_with_a_fresh_store_per_call`

#### `test/test_determinism.rb` — 19 unclaimed

**C12** (2):
- `test_winsize_requires_both_dimensions`
- `test_winsize_ignores_nonsense`

**C6** (4):
- `test_coalesce_scope_pin`
- `test_store_factory_defaults_to_a_random_coalesce_scope`
- `test_store_factory_threads_pins_into_every_store_it_builds`
- `test_two_pinned_runs_produce_identical_stores_and_journals`

**PRIOR** (13):
- `test_now_parses_an_iso8601_instant_as_utc`
- `test_now_converts_an_offset_instant_to_utc`
- `test_blank_pin_is_treated_as_unset`
- `test_seq_token_starts_at_zero`
- `test_id_source_is_memoized_per_spec`
- `test_id_source_rebuilds_when_the_spec_changes`
- `test_report_lists_every_pin_including_unset_ones`
- `test_store_mints_random_ids_when_unpinned`
- `test_store_mints_pinned_ids`
- `test_pinned_ids_still_skip_ids_already_taken`
- `test_config_resolve_honors_the_hostname_pin`
- `test_config_resolve_still_takes_an_explicit_hostname_provider`
- `test_an_unpinned_run_is_untouched`

#### `test/test_lead_matrix.rb` — 8 unclaimed

**C12** (3):
- `test_flat_tree_and_reveal_stay_aligned_on_a_lead_gated_subtree`
- `test_reveal_shows_the_lead_gated_anchor_and_its_rider`
- `test_the_renderer_fallback_agrees_with_the_query_about_hidden_rows`

**C5** (4):
- `test_project_header_counts_agree_with_the_read_model`
- `test_the_window_opens_on_the_derived_date_and_not_the_day_before`
- `test_the_window_opens_at_the_first_instant_of_the_derived_date`
- `test_unknown_writers_round_trip_the_keys_and_no_lead_key_is_untouched`

**C6** (1):
- `test_a_lead_write_conflicts_like_any_other_field`

#### `test/test_links_feature.rb` — 6 unclaimed

**C12** (3):
- `test_tui_open_link_flashes_and_launches`
- `test_tui_open_link_without_links_flashes`
- `test_tui_detail_panel_shows_links`

**PRIOR** (3):
- `test_config_collects_link_and_system_rows`
- `test_config_for_dir_has_empty_link_maps`
- `test_config_strips_inline_comments_but_keeps_url_anchors`

#### `test/test_schema_v2.rb` — 7 unclaimed

**C12** (1):
- `test_the_tui_and_the_store_answer_the_version_question_identically`

**C5** (3):
- `test_time_metadata_is_canonical_and_checked`
- `test_check_rejects_orphans_shapes_zones_and_dst_gaps`
- `test_store_round_trips_and_undoes_atomic_temporal_patch`

**PRIOR** (3):
- `test_delegation_is_additive_and_does_not_bump_the_schema_version`
- `test_an_unreadable_live_file_does_not_suppress_the_archive_half_of_the_gate`
- `test_an_unreadable_file_alone_is_not_reported_as_version_skew`

#### `test/test_store.rb` — 51 unclaimed

**C5** (33):
- `test_reschedule_updates_existing_deadline`
- `test_reschedule_updates_scheduled_when_no_deadline`
- `test_reschedule_adds_deadline_when_item_has_no_stamp`
- `test_reschedule_promotes_inbox_item_to_todo`
- `test_reschedule_does_not_promote_non_inbox_states`
- `test_reschedule_promotion_is_undoable`
- `test_reschedule_does_not_touch_other_items`
- `test_parse_reads_repeater_cookie`
- `test_set_recur_attaches_cookie_preserving_date`
- `test_set_recur_off_removes_cookie`
- `test_set_recur_rides_scheduled_when_no_deadline`
- `test_set_recur_on_undated_task_returns_false`
- `test_set_recur_rejects_stale_line`
- `test_complete_recurring_rolls_forward_and_stays_open`
- `test_complete_recurring_from_completion_uses_today`
- `test_timed_completion_recurrence_uses_the_fixed_values_local_date`
- `test_timed_catch_up_keeps_a_candidate_later_on_the_completion_day`
- `test_catch_up_completion_from_a_stamp_years_stale_still_rolls`
- `test_done_via_set_state_also_rolls_recurring`
- `test_cancel_recurring_truly_closes`
- `test_completing_a_claimed_recurring_task_returns_the_next_occurrence_to_the_pool`
- `test_completing_a_human_delegated_recurring_task_keeps_the_person_not_the_work_ref`
- `test_complete_recurring_is_undoable`
- `test_complete_recurring_parent_does_not_touch_child_stamp`
- `test_set_recur_on_parent_does_not_attach_to_child`
- `test_set_date_preserves_repeater_cookie`
- `test_reschedule_preserves_repeater_cookie`
- `test_recurring_completion_preserves_the_availability_to_due_window`
- `test_recurring_both_date_completion_uses_one_injected_today_for_delta_and_note`
- `test_injected_recurrence_matrix_advances_each_date_shape_and_undoes_byte_exactly`
- `test_recurring_completion_strips_defer_tag`
- `test_zero_count_cookie_is_not_a_repeater`
- `test_complete_cascade_retires_recurring_descendant`

**C6** (13):
- `test_changed_detects_external_writes`
- `test_with_lock_rejects_cross_fiber_contention_and_cleans_up`
- `test_shared_store_reads_stay_coherent_across_threads`
- `test_undo_with_empty_history`
- `test_undo_and_redo_roundtrip_a_complete`
- `test_undo_stacks_multiple_mutations_in_order`
- `test_new_mutation_clears_redo`
- `test_undo_refuses_after_external_edit`
- `test_failed_mutation_records_no_history`
- `test_undo_history_is_capped`
- `test_undelegate_with_an_expected_revision_never_repairs`
- `test_strict_revision_changeset_never_repairs_an_invalid_file`
- `test_move_under_is_undoable_byte_for_byte`

**C9** (1):
- `test_ordinary_read_snapshot_does_not_run_the_api_structural_check`

**PRIOR** (4):
- `test_move_top_on_parentless_task_returns_false_without_hanging`
- `test_move_over_cap_height_subtree_still_moves_to_a_section`
- `test_move_under_and_top_reject_stale_items`
- `test_multiple_notes_accumulate_in_body`

#### `test/test_store_patches.rb` — 36 unclaimed

**C5** (7):
- `test_confirmation_expectations_atomically_guard_coupled_date_recurrence`
- `test_recurrence_confirmation_uses_live_date_availability_not_exact_other_date`
- `test_changed_defer_preserves_interleaved_unowned_tag_placement`
- `test_date_patch_promotes_inbox_and_clearing_final_date_retires_recurrence`
- `test_recurrence_patch_validates_cookie_and_fresh_dates`
- `test_state_patch_advances_recurrence_without_cascade`
- `test_atomic_availability_changeset_rejects_stale_revision_without_overwriting_or_journaling`

**C6** (16):
- `test_patch_can_preserve_an_adapter_supplied_history_label`
- `test_location_fingerprint_conflicts_on_structural_change_but_not_field_change`
- `test_state_conflicts_when_affected_descendant_lifecycle_changes`
- `test_state_adopts_an_unrelated_body_change`
- `test_post_write_check_failure_rolls_back_and_records_no_history`
- `test_writer_failure_rolls_back_and_records_no_history`
- `test_successful_patch_is_one_undoable_checked_write`
- `test_byte_contiguous_patches_with_one_session_key_are_one_undo_step`
- `test_nil_and_mismatched_keys_keep_separate_patch_entries`
- `test_intervening_cli_mutation_breaks_patch_coalescing`
- `test_external_absolute_cli_write_preserves_one_step_segment_and_breaks_the_next_segment`
- `test_undo_redo_breaks_patch_coalescing_even_back_at_exact_tip`
- `test_history_branch_breaks_patch_coalescing`
- `test_new_store_instance_cannot_extend_a_coalesced_segment`
- `test_external_org_bytes_break_coalescing_and_become_the_safe_baseline`
- `test_external_archive_bytes_break_coalescing`

**PRIOR** (13):
- `test_invalid_tag_slice_is_typed_and_atomic`
- `test_body_replacement_preserves_exact_whitespace_and_newlines`
- `test_location_moves_the_whole_subtree_and_reports_summary`
- `test_location_cycle_and_depth_failures_are_typed_and_atomic`
- `test_state_patch_cascades_and_uses_lifecycle_fingerprint`
- `test_cascade_helper_returns_stable_ids_not_file_coordinates`
- `test_malformed_file_is_rejected_before_write`
- `test_unparseable_target_line_is_never_repaired`
- `test_invalid_utf8_live_bytes_are_contained_and_preserved`
- `test_invalid_utf8_proposed_value_is_a_typed_atomic_failure`
- `test_patch_boundary_does_not_swallow_fatal_exceptions`
- `test_location_patch_matches_move_under_cli_semantics`
- `test_lifecycle_patch_matches_set_state_cli_semantics`

#### `test/test_task_changeset.rb` — 11 unclaimed

**C5** (2):
- `test_changeset_is_immutable_and_normalizes_recurrence_aliases`
- `test_changeset_returns_stale_for_a_concurrent_time_or_zone_change`

**C6** (6):
- `test_store_revision_is_semantic_immutable_and_not_a_line_or_mtime_token`
- `test_nil_location_resolves_current_enclosing_section_under_mutation_lock`
- `test_changeset_retains_typed_conflict_results_for_failed_confirmations`
- `test_changeset_returns_stale_for_a_changed_own_semantic_revision`
- `test_title_only_changeset_ignores_a_sibling_change_but_state_guards_lifecycle`
- `test_one_field_task_patch_uses_the_shared_changeset_history_path`

**PRIOR** (3):
- `test_location_accepts_legacy_values_and_rejects_malformed_typed_ids_before_lookup`
- `test_changeset_rejects_ambiguous_composite_fields_before_any_write`
- `test_application_updates_by_values_or_a_typed_changeset_without_exposing_store`

#### `test/test_task_placement.rb` — 9 unclaimed

**C6** (7):
- `test_undo_and_redo_restore_byte_identical_placement_states`
- `test_post_write_check_failure_rolls_back_placement_and_records_no_history`
- `test_revision_own_component_excludes_location_but_order_changes_location_components`
- `test_consecutive_drags_and_unrelated_sibling_churn_use_live_stable_anchors`
- `test_placement_stales_on_own_edit_but_legacy_move_still_stales_on_sibling_order`
- `test_missing_placement_targets_precede_stale_own_revision_but_legacy_order_is_unchanged`
- `test_ordinary_field_edit_ignores_a_concurrent_location_change`

**PRIOR** (2):
- `test_application_applies_multi_field_placement_as_one_atomic_command`
- `test_application_preserves_placement_missing_target_precedence`

#### `test/test_task_queries.rb` — 14 unclaimed

**C5** (8):
- `test_timed_availability_is_inclusive_and_filters_default_and_named_views`
- `test_unavailable_and_someday_filters_distinguish_effective_from_own_hold`
- `test_availability_after_previews_own_fields_with_canonical_ancestor_precedence`
- `test_blocker_precedence_is_hold_then_latest_date_then_nearest`
- `test_closed_ancestors_are_hoisted_but_their_blockers_still_apply`
- `test_task_resource_exposes_own_marker_and_effective_availability_separately`
- `test_date_only_boundary_matrix_is_inclusive_inherited_and_read_only`
- `test_closed_and_archived_scopes_keep_legacy_own_hold_filtering`

**PRIOR** (6):
- `test_operation_context_is_typed_and_immutable`
- `test_delegation_filter_parsing_and_scope_rules`
- `test_delegated_scope_selects_every_marker_and_agent_ready_only_claimable_work`
- `test_agent_ready_excludes_claimed_human_unavailable_and_closed_tasks`
- `test_delegation_scopes_compose_with_the_existing_filters`
- `test_delegation_rides_on_the_canonical_task_resource`

#### `test/test_tree.rb` — 10 unclaimed

**C8** (4):
- `test_cli_links_lists_and_filters_by_system`
- `test_cli_links_empty_message`
- `test_cli_list_body_flag_searches_notes`
- `test_cli_show_reports_project_and_links`

**PRIOR** (6):
- `test_extract_ignores_plain_prose`
- `test_extract_ignores_org_internal_links`
- `test_extract_drops_scheme_only_fragments`
- `test_classify_fallback_is_lowercased`
- `test_classify_confluence_on_atlassian_by_path`
- `test_store_links_includes_title_urls`
