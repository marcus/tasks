# Changelog

## [1.17.0] - 2026-09-02

### Features

- **A project has an open/closed lifecycle of its own.** (#22) Closing a project used to close its tasks and leave the section itself looking open forever. `tasks project complete <ref>` now stamps the section DONE with today's date in the same write that cascades DONE over its open descendants, and two new verbs finish the cycle: `project drop` (alias `project cancel`) does the same with CANCELLED, and `project reopen` clears state and closed date from the section while leaving its tasks untouched. The lifecycle is open, then closed, then archived, each step explicit: a closed project stays in the live file until `project archive` sweeps it. `tasks projects` excludes closed projects by default, `--closed` selects only them, `--all` shows both, and a closed project is never reported as stuck. `--json` carries `state` and `closed` when present, and a project ref resolves against closed projects too. Over the API, `GET /api/v1/projects` takes `?closed=exclude|include|only`, and `POST /api/v1/projects/{id}/drop` and `/reopen` join `/complete`. In the TUI, the Outline and the Projects tab hide closed projects transparently, so depth is unchanged and an open descendant hoists through a closed parent, and the existing session-only `C` toggle reveals them in place with the same muted styling closed tasks use. The detail rail now names the task's project in every view.

## [1.16.0] - 2026-08-27

### Features

- **Delegation stamps read in your timezone.** (#17) Every human-facing print of `delegation.at` — `tasks show`'s `(since …)`, the TUI detail pane, and the claim-conflict message — now renders in the configured `timezone` and `time_format` (`thu 08-27 5:46p`, `17:46` under 24-hour) instead of the raw UTC stamp. The year appears only when it is not the reader's own, so an old handoff can't masquerade as yesterday's. Storage, `--json`, and the API keep the exact UTC instant, and a stamp the validator would reject prints as stored rather than being laundered into a healthy-looking local time. The 12/24 rule now has a single implementation (`temporal.ClockLabel`) shared by `tasks list`.
- **The Outline hides finished work behind `C`.** (#18) DONE and CANCELLED rows no longer bury the tree: the Outline shows open work by default, and `C` (palette: toggle closed view) reveals the closed rows in place with their existing styling. Hidden rows are transparent — an open child hoists through a hidden closed parent — and every trace stays honest: section badges read `4 · 1 closed`, a search or store whose matches are all closed says `N closed hidden · C shows` instead of rendering blank, and malformed states stay visible rather than being miscounted as closed. Reordering and indenting now anchor on the nearest *visible* sibling, so a keypress can never rewrite the file without moving anything on screen. PROPOSED stays excluded, `Z` composes independently, and the toggle is session-only.
- **Projects can show what already shipped.** (#19) The same `C` toggle on the Projects tab includes DONE and CANCELLED children under their project. Toggled on, the stored tree is shown as-is — a closed parent appears with its open child nested beneath it instead of being pruned and hoisted — and a project whose only live children are closed becomes a real foldable heading rather than a line on the dormant tail. A finished project drops the `⚠ stuck` warning, badges count what the current filter shows with a muted `· N closed` leftover, and unfiled work and swept `archive.jsonl` history stay out. Default off: the open-only view, hoisting and all, is unchanged.

## [1.15.0] - 2026-08-26

### Features

- **`N` marks the selected task NEXT.** (#16) One keypress in the list or detail view now does what previously took the edit form or `tasks state <ref> NEXT`: the selected open task becomes a GTD next action and appears on the Next tab, in a single undo step. The key sits in the `?` help and the `:` palette, refuses politely on proposed and done tasks, and is a silent no-op when the task is already NEXT. Lowercase `n` (modal cancel) is untouched, and pressing `N` inside any modal, filter, or form field types or cancels as before — it can never mutate state from an input context.
- **An empty Next tab explains itself.** (#15) When nothing is marked NEXT, the tab no longer renders a blank pane that looks broken next to a full Agenda. It now says there are no explicit next actions, how many dated items are waiting on Agenda (the count comes from the same query the Agenda tab paints, so the two can never disagree), and how to mark one — `N`, or `tasks state <ref> NEXT`. The CLI matches: `tasks next` with no next actions prints a one-line message instead of silence, mentioning Agenda only when dated work actually exists there. `tasks next --json` still prints `[]`, unchanged.
- **The Inbox tab groups intake by project.** (#13) Approvals and accepted Inbox rows are now bucketed under project headings inside their sections, so a triage pass stays inside one theme instead of hopping between projects in file order. Group order follows the Projects tab's sequence, unfiled one-offs collect in a single trailing group, empty groups vanish, and headings are chrome — unselectable, invisible to the a/r approve-reject walk, and counted by task (the section badge's own rule) so folding a subtree never shrinks a heading. Within a group, approvals keep their priority-then-due triage order. CLI `tasks inbox` stays flat.
- **`tasks move` files proposals.** (#14) A PROPOSED task can now be moved into a project — positional section, `--under`, `--before`, or `--top` — instead of the old reject-and-re-propose dance that minted a new id and polluted rejection history. Filing is a location change, not a decision: the task stays PROPOSED, and destination refs may name a proposal only when the moving subtree is itself proposed, so accepted work still can't nest under an undecided proposal. This matches what `PATCH /api/v1/tasks/{id}` always allowed; the API is unchanged.

## [1.14.0] - 2026-08-24

### Features

- **The TUI's modals adopt Sidecar's design language.** The delegate modal is the first surface rewritten on the new chrome primitives: boxed form fields instead of bare lines, buttons as real interactive surfaces, a fixed footer that stays put as content scrolls, and full scrollbars whose thumb shows where the window sits in a long note. The chrome is built from named theme slots (`internal/tui/term/theme`), so an embedding host — Sidecar today, anything else later — projects its own palette into them through the existing `pkg/tui.ThemeOptions` seam rather than having colors baked into the renderer.
- **Scrollbars are draggable.** Grab the thumb and it tracks the pointer, preserving the offset you grabbed it at; click anywhere on the track to jump there. The math is state-free and ported from Sidecar's implementation, so the two behave the same way under the same gesture.

### Bug Fixes

- **Clicking just left of a button no longer invokes it.** Button hit spans were computed one cell left of where the buttons were painted, so the blank column beside a button was live — on the delegate modal that meant Release and Undelegate could fire from a click that visually missed them. At narrow widths the clamp was applied to already-shifted coordinates, so a button truncated off the screen entirely stayed clickable, which is precisely the case the clamp exists to prevent.
- **The ctrl-s hint names what ctrl-s does.** The chip read "newline in note" while ctrl-s submits and Return is what inserts a newline, so following the on-screen hint to break a line delegated the task instead.
- **An upward scrollbar jump lands where you clicked.** `ScrollToRow` always parked the caret on the window's bottom edge, but the renderer derives the offset from whichever edge the caret crossed, so jumping upward settled height-1 rows low and stayed there.

## [1.13.0] - 2026-08-22

### Dependencies

- **Tasks now builds with Go 1.27.** `encoding/json/v2` is the default JSON
  implementation in this release; Tasks' stored task data, config, and API
  payloads round-trip unchanged, and `GOEXPERIMENT=nojsonv2` remains as an
  escape hatch. The `testdata/external-tui-consumer` module moves with it, so
  the embedded-host contract keeps compiling against the same directive as the
  library it consumes.
- This release exists so the td / Tasks / Sidecar trio share one toolchain:
  Sidecar resolves its CI Go version from its own `go.mod`, so it can only pin
  a Tasks that declares a directive its toolchain already satisfies.

## [1.12.0] - 2026-08-20

- **Embedded hosts can compose Tasks' list and detail regions into their own
  focus rings.** The public `pkg/tui` API now exposes stable spatial-focus IDs,
  the exact rectangles from the layout Tasks renders and hit-tests, the current
  region, direct focus control, and `TabOwnsFocus`. The README documents the
  Sidecar capability check and the boundary between spatial focus and Tasks'
  existing interaction contexts.
- Inputs and overlays keep ownership of Tab, including field modals, while
  passive list/detail and response views can yield it to an outer host. Compact
  layouts expose only positive rendered focus stops; hidden, unknown, or
  input-covered targets refuse direct focus without changing selection. Keys
  and clicks follow the same spatial focus, and project-detail panels retain
  list command routing so task-detail actions cannot run against a project row.

## [1.11.0] - 2026-08-18

- **`D` is a three-part modal instead of a one-line prompt.** Delegating now
  asks the three questions a delegation actually has — who (a person or the
  agent pool), which mode, and the note the receiver works from — on a modal
  that is complete by keyboard and by mouse. Re-delegating prefills all three
  from the current marker, and one submit writes them in a single undo step.
  Release and undelegate are explicit affordances, and both confirm before they
  act, which retires the old flat word grammar: `implement`,
  `release` and the clear words used to share one text field, so the shortest
  inputs performed the widest and most destructive actions and a three-letter
  prefix minimum was the only thing standing between a keystroke and a revoked
  claim. Typing a verb into a text box is no longer how work is taken away.

- **The three-part delegation reaches the CLI and HTTP.** `tasks delegate <ref>
  <mode> --note "…"` writes who, in what mode, and the briefing the receiver
  reads in ONE store write and therefore one undo step — no surface composes a
  delegation out of a delegate followed by a note write. `--note-file <path>`
  and `--note-file -` (stdin) exist because a long briefing written by an agent
  should not have to fight shell quoting. A mode is now accepted alongside
  `--to <email>`, so a person can be asked for a refine just as an agent can.
  Omitting the note keeps any existing briefing — restating the mode must not
  silently erase instructions — and `--note off`/`none`/`""` clears it, the
  same two words a work reference clears with and both already reserved mode
  names, so a clear instruction can never be read as a mode. `tasks delegate
  <ref> --note "…"` with no mode and no `--to` rewrites the briefing in place,
  for an owner correcting instructions without re-sending a delegation.
- Delegation reads show all three parts. `show` prints the mode and the
  briefing in full, `list --delegated` and `list --agent-ready` print the mode
  in the headline with a one-line preview of the note beneath it, and `--json`
  carries both as first-class members of the `delegation` object everywhere —
  including `list --agent-ready --json`, which is what an agent heartbeat
  reads.
- **The HTTP delegation writes work.** `POST /api/v1/tasks/{id}/{delegate,
  undelegate,claim,release}`, `PUT …/work_ref`, and the new `PUT
  …/delegation_note` answered `501` because the mandatory `If-Match` could not
  be honoured — the store did not compare a revision inside its delegation
  transaction, and dropping a precondition a client set is worse than refusing.
  It does now, under the same lock the write runs in, so the routes perform the
  operation with their preconditions intact: `200` with the whole canonical
  task and a fresh `ETag`, `412` on a stale revision, and `409` on a lost claim
  race carrying `holder` and `at` as their own fields rather than as prose an
  agent would have to re-parse.

- Answer a proposal for work that is already done in one keystroke. On a
  PROPOSED row `c` now approves the proposal AND completes the accepted task in
  a single write and a single undo step, so `undo` restores `PROPOSED` exactly;
  rejecting was previously the only one-key way to clear such a row, and it
  recorded a decline — the wrong history, and the wrong signal to whoever
  proposed the task. The store still never completes a PROPOSED task: the
  approval lands first inside the same transaction. `tasks approve <ref>
  --done` and `POST /api/v1/tasks/{id}/approve?complete=true` are the CLI and
  HTTP spellings of the same shared command, both `--json`/ETag-honest. The
  proposal row's key hints now describe what the keys actually do.
- Refuse a recurring proposal in `check`. Recurrence is a schedule for accepted
  work — completing a recurring task rolls it forward instead of finishing it —
  and no write path could ever produce the shape, but a hand-edited, repaired,
  or foreign-device file could, and it passed validation. Now `recur` on a
  `PROPOSED` task is a `check` error, and approve+complete refuses it rather
  than reporting a DONE that did not happen.
- A delegation becomes three orthogonal parts — **who** (a person or the agent
  pool), **mode** (`refine`/`research`/`implement`), and a new optional
  **note**, free text briefing the receiver on how to work on the task and
  where the work should land, up to 2000 characters with paragraphs allowed.
  *Store and schema support only in this entry; the CLI, HTTP, and TUI
  spellings follow in the surface work.* The note is stored last in the marker,
  so a store written by an earlier release loads, checks clean, and re-emits
  byte for byte. A mode is now valid on a *human* delegation as well: "Pat,
  this is a refine" is a thing the data model can say. The note travels with
  the delegation it briefs — kept across a mode change or a new assignee of the
  same kind, dropped when a person replaces the pool or the reverse — carries
  its own transition stamp on an unclaimed delegation so the most recent
  briefing wins a multi-device merge (a note on a live claim leaves the claim's
  own stamp alone, so briefing a working agent survives a sync), and merges
  atomically with the rest of the marker, where `undelegate` still always wins.
- The mode vocabulary is carried as a value on the store and checker options
  rather than checked against a literal, so configuring the set later is one
  field, not a hunt through the code.
- **The delegation mode vocabulary is yours to choose.** `delegation_modes =
  triage, ship` in `~/.config/tasks/config` (or `TASKS_DELEGATION_MODES`)
  replaces `refine, research, implement` everywhere at once: what `tasks
  delegate <ref> <mode>` accepts, what a refusal quotes back, the `delegate`
  usage line, `tasks help`, the TUI `?` overlay and delegate modal, the check view,
  and the API's vocabulary document. A mode is a bare lowercase word and
  carries no label — the label is the word. With no config key, behaviour is
  exactly what it was.
- A `delegation_modes` list this binary cannot honour — a word of the wrong
  shape, a duplicate, a reserved word, an empty list — is ignored *whole*, with
  a warning on stderr naming the problem and the set actually in use. Keeping the readable
  half would run you against a vocabulary you never wrote. Nothing about a bad
  list makes the store unreadable or unwritable.
- A record whose `mode` the active vocabulary does not list still loads, shows,
  and checks: `check` reports it as a WARNING, never an error. Which words are
  listed is a setting, and settings change — editing `delegation_modes`, or
  syncing a file from a machine configured differently, can no longer invalidate
  a file your tasks live in. A stored mode of the wrong *shape* is still an
  error, and *writing* an unlisted mode is still refused.
- `tasks config` and `tasks config --json` report the resolved
  `delegation_modes` and where they came from, alongside every other setting.
- `off`, `none`, `clear`, and `release` are reserved and cannot be configured as
  modes: `release` is a delegation verb and the others clear a work reference or
  a note, so a mode spelled like one of them would make an instruction and a
  mode indistinguishable wherever both are written in one place. The collision
  is reported when the config is read, not at the moment the verb is needed.
- Hosts embedding the TUI through `pkg/tui` get `ExportCommandsWith(modes)` to
  describe delegation with their own store's vocabulary; plain `ExportCommands`
  now renders the built-in set rather than an unfilled template.

## [1.10.0] - 2026-08-18

- Accept the relative dates people actually type. Anywhere a date is taken —
  `capture`, `propose`, `due`, `schedule`, `defer`, the TUI edit form, and the
  API — `two weeks`, `in two weeks`, and `two weeks from now` all work, in
  digits or English number words, over seconds, minutes, hours, days, weeks,
  months, or years. Calendar units also take compact forms: `2d`, `2w`, `2m`
  (months), `2y`. Second-, minute-, and hour-relative phrases produce a timed
  value from the current instant; since stored times have minute precision, a
  result carrying seconds rounds *up* so it never lands earlier than the
  boundary that was asked for. Such a phrase already names an exact side of a
  DST overlap, so pairing it with `--fold` is refused rather than silently
  resolved. `next week`/`next month`/`next year` keep working as the one-unit
  spellings.
- Make releasing one command: `BUMP=major|minor|patch make release` derives the
  version from the latest tag, stamps `## [Unreleased]` with the version and
  date, commits `release: prepare vX.Y.Z`, pushes `main`, and publishes — the
  version is stated once. `RELEASE_VERSION=vX.Y.Z make release` and a hand-
  stamped heading still work; `make release-dry-run` prints the plan, verifies
  the Homebrew publication prerequisites, and stops before any mutation. Every
  existing fail-closed publication check is unchanged.

## [1.9.0] - 2026-08-17

- Rank the proposed queue by priority then due date, so a two-minute scan of
  pending proposals hits the ones that matter first. `list --proposed` (text and
  `--json`), the TUI Inbox Approvals section, and
  `GET /api/v1/tasks?scope=proposed` share one core ranking —
  `taskquery.Queries.RankByPriorityThenDue`, which also ranks `--agent-ready`:
  priority A>B>C>none, then the soonest deadline-or-scheduled boundary, undated
  last inside a band, file/DFS order breaking a tie. The default open list,
  agenda, and Outline keep file/DFS order.
- Make context URLs first-class at create time (#10). `tasks capture` and
  `tasks propose` take a repeatable `--link URL`, each optionally followed by
  `--label TEXT`, and store the links in the order given — validated exactly as
  `link add` validates them, and written in the SAME transaction and undo step as
  the task, so filing a Slack/Jira/doc URL is no longer a second command an agent
  can forget. `POST /api/v1/tasks` accepts the equivalent `links` array. A title
  whose last word is already an `http`/`https` URL lifts that URL into a formal
  link and keeps the remaining words as the title.
- Make a declined proposal reviewable and reversible (#9). `reject` now stamps
  the record with a `rejected` date; `tasks list --rejected` reviews proposals
  declined in the last 30 days (newest first, recently archived rows labelled),
  and `tasks unreject` restores one to `PROPOSED` in place — same id, title,
  notes, and links, undoable. The TUI Inbox hides rejects by default and reveals
  them with `R` for a one-key `a` restore; the API adds `scope=rejected` and
  `POST /tasks/{id}/unreject`. `tasks check` and the merge driver understand the
  marker, and default views stay clean.

## [1.8.3] - 2026-08-17

- Align the TUI's tab mouse hit areas with the tabs as drawn, so a click lands
  on the tab under the pointer rather than a neighbour.
- Force a truecolor profile for README screenshot captures. Agent and CI shells
  often run as `TERM=dumb` with `NO_COLOR=1`, which made Bubble Tea render the
  TUI attributes-only; `make screenshots` now stays in color.

## [1.8.2] - 2026-08-14

- Report the module's tagged version from `go install ...@version` builds so
  public source installs have verifiable provenance.
- Add a one-shot Homebrew verification target that activates, updates,
  upgrades, tests, and reports all three released commands.

## [1.8.1] - 2026-08-14

- Fail store lock waits after 5s with the holding pid and acquire time,
  instead of blocking forever with empty stdout. Snapshot reads take a
  shared lock so they no longer serialize with each other.
- Add a reproducible, isolated screenshot workflow and refresh the README's
  CLI and TUI images, alongside a complete feature reference.

## [1.8.0] - 2026-08-13

- Redesign the Projects view: live projects are foldable headings, every project
  with nothing under it collapses into one row, and the section badge names the
  live share.
- Stop listing the reserved GTD lists (Next Actions, Waiting For, Someday /
  Maybe) as areas — membership in each is already derivable from a task's state
  or defer marker. They resolve to kind `list` and keep their outline rows.
- Give every list row an urgency band, move the priority letter beside the title
  it ranks, and right-align `@` contexts into their own shared column.
- Band the outline's plain lists into overdue / today / later, tighten the
  spacing around nested headings, and name the overdue count in the header.
- Paint the selected row as a dark band rather than reverse video, and darken
  every imported theme's selection to match.
- Keep the reserved GTD lists addressable by `project show|rename|complete|
  archive` and the API even though the `projects` listing omits them.

## [1.7.0] - 2026-08-11

- Add ordered, labelled formal task links; CLI `link add`/`link rm`/`link set`;
  API `formal_links` writes; and a shared formal/title/body openable union.
- Let the TUI open one link directly or choose among several through a
  searchable keyboard/mouse picker with labels and bounded digit shortcuts.

## [1.6.0] - 2026-08-11

- Refresh the TUI around a shared visual vocabulary: section rules and counts,
  aligned priority and metadata columns, and consistent selection across every
  list view.
- Rework Agenda as calendar groups with distinct start/deadline treatment, and
  expand the detail rail with state, labels, links, actions, and subtask
  progress.
- Simplify the outer frame, improve palette-aware chrome contrast, and add
  pointer resizing for the detail rail while preserving embedded-host routing
  and layout contracts.

## [1.5.0] - 2026-08-10

- Let embedded hosts suppress numeric view-jump advertisements while retaining
  view names, selection state, mouse targets, and command availability.
- Add standard word-navigation and word-deletion keys across Tasks text inputs.
- Keep approval details synchronized when accepting or rejecting a proposal
  advances the selected item.
- Make shortcut-help search render only matching bindings and section rows.
- Add a comprehensive Sidecar embedding guide covering the public TUI contract,
  key routing, release ordering, and end-to-end verification.

## [1.4.0] - 2026-08-10

- Add the embeddable Tasks TUI package used by Sidecar, including host-owned
  command routing, shortcut metadata, theme projection, queue/context hooks,
  and independently configurable chrome.
- Migrate the standalone TUI to Bubble Tea v2 while preserving shared behavior
  between the embedded and standalone surfaces.
- Harden embedded key routing, modal bindings, exported command invocation,
  and quit handling so host applications can safely compose Tasks with their
  own controls.
- Restore nested, tree-aware project rendering and add confirmed hard-delete
  with undo support.
- Add provenance-aware local installation and Homebrew switching commands for
  reliable development and consumer testing.
- Supersede the unpublished `v1.1.0`, `v1.2.0`, and `v1.3.0` tags, whose
  release workflows failed before publishing artifacts.

## [1.0.0] - 2026-08-04

- Make the completed Go CLI, TUI, API, store, journal, and merge driver the
  sole implementation on `main`.
- Preserve the final Ruby-containing tree as annotated tag
  `ruby-final-2026-08-04`.
- Add standalone versioning, safe explicit configuration, Homebrew packaging,
  guarded releases, and Darwin/Linux archives for arm64 and amd64.
