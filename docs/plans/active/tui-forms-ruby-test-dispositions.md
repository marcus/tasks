# Ruby test dispositions — the TUI editor/forms/modals packet

Written for the integration owner. One row per Ruby suite this packet is
answerable for, saying where each contract went and — where a contract did not
come across — what is missing and why.

Counts are `grep -c 'def test_'` on the Ruby file. "Covered" means a Go test
asserts the same product contract, not that the assertion is transliterated:
several Ruby cases collapse into one Go table test, and several Go tests cover
a contract Ruby only exercises incidentally.

The judgment gate for this packet is `porting/compare/tui-interaction-diff`,
which drives 77 keystroke scenarios through **both** implementations and
compares mode, selection, status message, overlay state, panel kind, identity
and scroll position, agent queue
lifecycle, response text and scroll ownership, quit state, the resulting
`tasks.jsonl` AND `archive.jsonl` bytes, and the undo journal's cursor and
labels. It is at **77/77 GATE PASS**.

Both sides run against scripted fakes for anything external: a fake agent
adapter on each side, driven by a shared `<<tick>>` token that drains the queue
once so a FINISHED request can be compared without depending on wall-clock
timing. A second synthetic token advances the mutable injected clock across
midnight without touching either store file, so archive confirmation carries
the date captured at preview. No provider is invoked and no browser is opened.

Separately, `go/cmd/tasks-tui/main_test.go` tests the SHIPPING constructor —
that `buildModel` returns a model with real ordered `llm.Entries`, a non-nil
queue and a wired opener, that the availability probe does not read
`agent-memory.md`, and that the factory does. Those tests exist because a model
built with injected fakes proves nothing about the real binary.

## Summary

| Ruby suite | Cases | Disposition |
| --- | ---: | --- |
| `test_term_form.rb` | 30 | Covered — `termform/form_test.go` |
| `test_term_form_text_fields.rb` | 18 | Covered — `termform/fields_test.go` |
| `test_term_form_choice_date_fields.rb` | 17 | Covered — `termform/fields_test.go`, `temporalpicker_test.go` |
| `test_term_form_require_boundary.rb` | 2 | Covered — `termform/fields_test.go` |
| `test_form.rb` | 11 | Covered — `formrender_test.go` (quick form) |
| `test_form_renderer.rb` | 19 | Covered — `formrender_test.go` |
| `test_modal.rb` | 19 | Covered — `modal_test.go` |
| `test_modals.rb` | 15 | **Not this packet** — see below |
| `test_choice_picker.rb` | 10 | Covered — `palettes_test.go` |
| `test_action_palette.rb` | 8 | Covered — `palettes_test.go` |
| `test_context_palette.rb` | 13 | Covered — `palettes_test.go` |
| `test_ui_state.rb` | 13 | Covered — `uistate_test.go` |
| `test_task_editor_session.rb` | 29 | Covered — `taskedit_test.go`, `temporalpicker_test.go` |
| `test_app_modals.rb` | 85 | Covered — `appmodes_test.go` |
| `test_app_agent_queue.rb` | 12 | Covered — `agentprompt_test.go` |
| `test_links_feature.rb` | 19 | Link ACTION covered — `appmodes_test.go`; extraction is `internal/links` |
| `test_text_input.rb` | 8 | Already covered by `term/input` (previous packet) |

## Notes per suite

**`test_modals.rb` is misleadingly named.** Every one of its 15 cases is
`detail_*`: it tests the right-panel task detail renderer, which the shell
packet already ported into `details.go` and `details_test.go`. The help overlay
that `Tui::Modals.help` builds is exercised by `TestHelpIsGeneratedFromTheRegistry`
and by the `modal-escape-and-focus` differential scenario.

**`test_term_form.rb`** — the engine's focus/commit lifecycle. Ruby's 30 cases
group into the contracts `form_test.go` asserts: construction refusals,
traversal skipping hidden/disabled fields, validation scoped to reachable
fields, the two-phase dirty departure, accept/reject/refresh reconciliation,
pending single-flight, and the render model. Two Ruby cases pin
`Support.frozen_copy` deep-freezing; Go has no freeze, and the equivalent
property — a caller cannot mutate the form through a returned value or a
committed transition — is bought by `copyValue` and asserted by
`TestTemporalValuesNeverAliasThroughFormReadOrCommitSurfaces` and
`TestTemporalInputValueReturnsADefensiveCopy`.

**`test_app_modals.rb`** — 85 cases across the whole app surface, including the
two archive-clock regressions this packet added. Every family is now answered:
modals and popups, the palettes, the editor panel, archive, history, ordering,
delegation, the agent queue and its activity modal, and the response pane.

**`test_app_agent_queue.rb`** — the queue's FIFO/serial/max-pending contract,
full-queue prompt preservation, activity refresh and cancellation. Covered by
`agentprompt_test.go` against a scripted fake adapter; no test in this packet
invokes a provider or spawns a process.

## Gaps

**None.** `unbuiltHandlers` is empty, and
`TestNoBoundKeyStillRefusesAsUnimplemented` fails on any handler that either
refuses as unimplemented or has no implementation at all. Every capability the
previous revision of this document listed as a gap is now built:

1. **The structured temporal picker** — date / time / mode / zone / fold, with
   arrow stepping, an IANA zone search restricted to storable identifiers, the
   fold row appearing only for a genuinely ambiguous local time, and a forward
   time step that jumps a spring-forward gap to the first valid local time
   after it. The recorded difference
   `tui-temporal-picker-is-typed-not-stepped` is retired, not accepted.
2. **The list-wide archive sweep** — preview, confirmation, blocked modal,
   stale-preview refusal, cancel, and the sweep itself, through a new
   `ArchiveSweeper` capability on the application facade.
3. **Undo and redo** — through a new `HistoryStepper` capability, with Ruby's
   three distinct refusals kept distinct.
4. **Delegation and work reference** — through the application's own delegation
   commands, so the WAITING default and the blocker note stay composed in one
   undo step.
5. **Subtree ordering** — up, down, indent and outdent, through a new `Placer`
   capability over the store's own changeset.
6. **Open link** — through `internal/links`, with an injected `Opener`. No test
   opens a browser.
7. **The agent prompt surface** — prompt focus/edit/submit, quoted reference
   paste, bracketed paste from the list and from a modal, cell-accurate
   wrapping with a drawn caret, model cycling, FIFO/serial queueing with
   max-pending, full-queue prompt preservation, the activity modal with live
   in-place refresh that keeps the reader's filter and scroll, queued-request
   cancellation behind a confirmation, and the response pane with scrolling.

## Review defects found and fixed

The initial five included four found by running the two implementations against
each other, not by reading code and not by unit tests.

1. **`cmd/tasks-tui` never set the journal's coalescing scope.** The journal
   only extends a keyed tip when the scope matches too, and an empty scope is
   not persisted — so the editor's "one session is one undo step" contract
   silently did not hold in the shipping binary: every field save opened its
   own undo step. Fixed in `cmd/tasks-tui/main.go`, which now derives the token
   exactly as `cmd/tasks` does.

2. **The Go build was chattier than Ruby on unavailable keys.** Ruby's
   `unavailable_action` speaks for exactly two families — ordering and
   delegation — and consumes the rest in silence, because the reason for those
   is visible on the row. The first draft flashed for every one. Caught by the
   `unavailable-action-refuses-by-name` scenario and aligned.

3. **The Ruby TUI stamped archives from the wall clock.**
   `Tui::App#archive_sweep` called `@store.archive_preview` and
   `@store.archive_swept!` with no `today:`, so both fell back to `Date.today`
   — the process's LOCAL calendar day — while every other TUI write stamps
   `current_date`, which honours the configured timezone and the injected
   provider. A user whose configured zone had rolled over got an `archived` day
   that disagreed with the `closed` days the same session had just written, and
   a confirmation prepared either side of local midnight could stamp a
   different day than its preview described.

   FIXED AT ITS CAUSE rather than ported: `archive_sweep` now captures
   `current_date` once into `@archive_today`, carries it to the sweep, and
   `close_modal` clears it with the rest of the modal state. The identical
   defect had already been fixed for `archive_plan` and `archive_project_impl`,
   so porting it would have reintroduced a bug the project had decided against.
   Ruby regression coverage:
   `TestAppModals#test_archive_stamps_the_session_date_not_the_wall_clock` and
   `#test_archive_preview_and_sweep_share_one_captured_date`, both verified to
   fail on the pre-fix source.

4. **The shipping entry point never wired the agent seams or the opener.**
   `cmd/tasks-tui` constructed `tui.New` without `Entries`, `Queue` or
   `Opener`, so the real binary answered "no agent is configured" to every
   prompt key and "no browser launcher found" to every valid link — while the
   fake-injected model tests stayed green, because they injected exactly the
   seams the binary did not.

   Found by integration review of the shipping path, not by the differential:
   the harness drives `internal/tui` directly and so shared the same blind
   spot. Fixed by resolving `llm.LoadConfig`/`llm.Entries` from the same env,
   constructing the `term/agent` queue in `cmd/tasks-tui` with a context-free
   availability probe and a factory that builds `agentcontext` fresh at request
   START, adding `tui.AgentAdapter` (async start with streaming, non-blocking
   pump via Done, cancellation, output, exit and signal status), and wiring
   `tui.SystemOpener`. `cmd/tasks-tui/main_test.go` now fails on the unwired
   constructor — verified by reverting the wiring.

5. **The Go archive confirmation read the clock twice.** The preview and the
   confirmed sweep each minted an operation from a fresh temporal context. Since
   the preview fingerprint includes the local day, an unchanged task list was
   refused as "task list changed" when its confirmation modal remained open
   across midnight. The TUI now captures one temporal context at preview and
   carries it into a distinct confirmation operation, while all ordinary TUI
   operations use the model's injected session clock. The
   `archive-preview-crosses-midnight` differential scenario advances only that
   clock between `x` and `y`; Ruby and Go then agree on observable state and the
   exact live/archive store bytes.

A further divergence was found the same way and is behavior this build had
simply not ported: after a save moves a task out of the current view (putting it
on hold removes it from Next), Ruby closes the editor and says where the
selection went. Now ported, and pinned by the
`edit-deferred-toggle-writes-the-marker` scenario.

The independent closeout review then exercised the paths the first pass did not:
Bubble Tea paste messages; persistent dirty-draft and agent-work quit questions;
active cancellation and failed-start queue advancement; stale proposal decisions,
forms, editors, and action palettes; recurrence previews; mouse routing;
responsive editor suspension; markdown export; and temporal pointer ownership.
Those findings are pinned by focused Go tests and twenty-five added differential
scenarios, including both yes and no confirmation branches, a stale palette
write attempt after its original target is deleted, responsive editor
suspension and resumption, active-editor target deletion, paste-safe quit
ownership, and structural mouse hit-testing that proves response-adjacent
footer chrome cannot scroll either the response or the list. The final scenario
also makes same-identity panel scroll preservation part of the shared observed
state; the accepted cross-editor panel difference is recorded separately.
