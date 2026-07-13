# ADR-0001: Build the form system as an embedded, extractable library

Status: Accepted and implemented

Date: 2026-07-13

## Context

The TUI has a reusable `Tui::Form`, but it is a single-field popup tied to the
tasks application's ANSI renderer, theme, mode transitions, and submit
callbacks. Editing a task in the right panel needs a multi-field form with
focus traversal, text areas, selections, date entry, validation, and properties
that react to other field values.

The same components should support future capture and settings forms. They may
also become useful outside this repository. The project currently uses only the
Ruby standard library, and publishing a gem before the API has been exercised by
a real form would make early design mistakes expensive.

## Decision drivers

- Form state, focus, validation, and field behavior must be testable without the
  tasks Store or a running terminal.
- Task rules must stay in `Tasks` or the TUI adapter, not in generic fields.
- The existing date and recurrence popups must keep working during migration.
- The library must remain stdlib-only and easy to extract after its API settles.
- The tasks TUI must retain control of terminal geometry, theming, and the main
  event loop.

## Considered options

1. Grow `Tui::Form` in place. This has the smallest initial diff, but it keeps
   task callbacks, rendering, field state, and application modes coupled.
2. Create and publish a separate gem first. This establishes a hard boundary,
   but it commits to packaging and public API compatibility before the task
   editor has tested the design.
3. Add a neutral library under `lib/term_form/`, with a tasks-owned adapter and
   renderer. Prove extraction with dependency tests and a standalone example,
   then consider moving it to a gem.

## Decision

Choose option 3.

The reusable namespace will be `TermForm`. Code under `lib/term_form/` may use
the Ruby standard library, but it may not require `tasks/*`, `tui/app`,
`tui/store`, or task-specific theme constants. It will own:

- form, group, field, focus, draft, dirty, and validation state;
- input, text-area, select, multi-select/token, date-input, and confirm fields;
- static or callable field properties such as label, hint, visibility,
  enabled state, and options;
- normalized input events and configurable key maps;
- semantic render output, including the focused row and virtual cursor.

The tasks application will own `Tui::FormRenderer`, `Tui::TaskEditForm`, and
`Tui::TaskEditorSession`. The renderer maps semantic form roles onto
`Tui::Theme` and `Tui::Ansi`. The task adapter maps a Store edit snapshot to
fields, validators, suggestions, and semantic patch requests. The editor
session coordinates persistence and accepts or rejects focus-leave requests.

The public boundary is behavioral: a form accepts an event and returns a typed
transition; rendering reads state but performs no effects; and the host accepts
or rejects commit requests after validation. Fields never call the Store, write
files, inspect application modes, or print to the terminal. This protocol does
not require a save-on-blur host: another consumer may persist in memory, batch
changes, or offer an explicit submit action.

`Tui::Form` will remain as a compatibility wrapper while the existing date and
recurrence popups move onto `TermForm`. It can be removed or reduced to an alias
after those paths pass their current tests.

The first release is an internal API. Gem packaging, versioning guarantees, and
a name suitable for RubyGems wait until the task editor and at least one
standalone example use the same core without exceptions.

## Consequences

The form engine can evolve independently and can be tested with plain Ruby
objects. Task rules stay close to the Store and can change without teaching
generic fields about GTD concepts.

The repository will have an adapter layer and a semantic render contract that a
single in-place implementation would not need. That cost is deliberate: it is
the seam that prevents `TermForm` from becoming another name for tasks TUI
internals.

Extraction is possible but not automatic. `examples/term_form_demo.rb` now
exercises the semantic model through a plain renderer without loading task or
TUI constants, and the dependency-boundary test executes both the require
smoke and demo. A future gem would still need its own terminal renderer,
documentation, versioning, and accessibility story; this proof does not make
the embedded API stable.
