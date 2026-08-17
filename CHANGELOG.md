# Changelog

## [Unreleased]

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
