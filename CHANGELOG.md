# Changelog

## [Unreleased]

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
