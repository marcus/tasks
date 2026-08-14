# Changelog

## [Unreleased]

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
