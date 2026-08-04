# Ruby test dispositions — the TUI editor/forms/modals packet

Written for the integration owner. One row per Ruby suite this packet is
answerable for, saying where each contract went and — where a contract did not
come across — what is missing and why.

Counts are `grep -c 'def test_'` on the Ruby file. "Covered" means a Go test
asserts the same product contract, not that the assertion is transliterated:
several Ruby cases collapse into one Go table test, and several Go tests cover
a contract Ruby only exercises incidentally.

The judgment gate for this packet is `porting/compare/tui-interaction-diff`,
which drives 15 keystroke scenarios through **both** implementations and
compares mode, selection, status message, overlay state and the resulting
`tasks.jsonl` bytes. It is at **15/15 GATE PASS**.

## Summary

| Ruby suite | Cases | Disposition |
| --- | ---: | --- |
| `test_term_form.rb` | 30 | Covered — `termform/form_test.go` |
| `test_term_form_text_fields.rb` | 18 | Covered — `termform/fields_test.go` |
| `test_term_form_choice_date_fields.rb` | 17 | Covered except the temporal picker — see gaps |
| `test_term_form_require_boundary.rb` | 2 | Covered — `termform/fields_test.go` |
| `test_form.rb` | 11 | Covered — `formrender_test.go` (quick form) |
| `test_form_renderer.rb` | 19 | Covered — `formrender_test.go` |
| `test_modal.rb` | 19 | Covered — `modal_test.go` |
| `test_modals.rb` | 15 | **Not this packet** — see below |
| `test_choice_picker.rb` | 10 | Covered — `palettes_test.go` |
| `test_action_palette.rb` | 8 | Covered — `palettes_test.go` |
| `test_context_palette.rb` | 13 | Covered — `palettes_test.go` |
| `test_ui_state.rb` | 13 | Covered — `uistate_test.go` |
| `test_task_editor_session.rb` | 29 | Covered except the temporal picker — see gaps |
| `test_app_modals.rb` | 83 | Partially covered — `appmodes_test.go`; see gaps |
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
property — a caller cannot mutate the form through a returned value — is
bought by `copyValue` and asserted by
`TestFormRefreshKeepsADirtyBufferAndMovesEveryBaseline`.

**`test_app_modals.rb`** — 83 cases across the whole app surface. This packet
answers the modal, popup, palette and editor-panel families. The remainder
belong to surfaces this packet does not own and does not claim: the agent queue
and its activity modal, the agent response pane, delegation, and subtree
ordering. Each of those refuses out loud by name at the key (see
`unbuiltHandlers`), and `TestUnimplementedKeysSayWhatIsMissing` pins that.

## Gaps, stated plainly

1. **The structured temporal picker is not ported.** Ruby's
   `TaskEditForm::TemporalInput` opens a five-row control (date / time / mode /
   zone / fold) with an IANA zone search over `TZInfo.all_identifiers`. Six
   Ruby cases across `test_task_editor_session.rb` and
   `test_term_form_choice_date_fields.rb` exercise it.

   What this build has instead: the date field parses the **same expression
   grammar** — `2026-08-09 17:30 Europe/Berlin`, `tomorrow 9am`, `fri noon`,
   with an optional `fold=later` — and opens the base calendar picker on
   Return. Nothing is unreachable: every value the Ruby picker can produce can
   be typed, and `TestATimedDateKeepsItsWallTimeAndZoneThroughTheSave` proves
   the wall time and zone survive to the stored bytes. What is missing is the
   arrow-driven *editing affordance*, and the DST-specific behaviors that only
   the arrows reach (stepping onto the first valid local time after a spring-
   forward gap; exposing the fold row only when the local time is ambiguous).
   Recorded in `porting/intentional-differences.md`.

2. **`x` — the list-wide archive sweep — refuses by name.** The application
   facade publishes no archive capability (`store.ArchivePreviewFor` and
   `ArchiveSweep` exist but are not on `application.Store`), and adding one is
   outside this packet's extended ownership. `x` on a **project header** is
   fully implemented, because `Application.ArchiveProject` does exist. The
   refusal names the missing seam.

3. **`u` / ctrl-r — undo and redo — refuse by name.** Same shape:
   `store.HistoryStep` exists, `application` does not expose it. The editor's
   own undo contract — one editing session is one undo step — is nonetheless
   implemented and asserted
   (`TestOneSessionCoalescesItsSavesIntoOneHistoryStep`), because it is a
   property of the *write*, not of the undo key.

4. **The agent prompt stays deferred**, as scoped.

## Two defects found and fixed

Both were found by running the two implementations against each other, not by
reading code or by unit tests.

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

A third divergence was found the same way and is behavior this build had simply
not ported: after a save moves a task out of the current view (putting it on
hold removes it from Next), Ruby closes the editor and says where the selection
went. Now ported, and pinned by the
`edit-deferred-toggle-writes-the-marker` scenario.
