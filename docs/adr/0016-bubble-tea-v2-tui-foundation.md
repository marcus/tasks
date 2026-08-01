# ADR-0016: Bubble Tea v2 as the Go TUI foundation

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

The Ruby TUI is roughly 12,000 lines, and a large fraction of it is terminal
plumbing rather than product: manual `IO.select` loops, frame painting, cell
measurement for wide characters, color downsampling, mouse decoding, clipboard
handling. ADR-0001 already took the position that generic terminal machinery
should live in a neutral, extractable library; `lib/term_form*.rb` is that
position in Ruby.

A Go TUI needs the same separation, and Go has an incumbent for it. The port
also has a second consumer that Ruby never could serve: sidecar embeds td's
monitor as an in-process Bubble Tea component, and a `tasks` panel in sidecar is
one of the reasons to consider this port at all (ADR-0017). That constrains the
choice — an embeddable component must speak the host's framework.

## Decision drivers

- Replace hand-written terminal plumbing rather than reimplement it in Go.
- Be importable as a component by a Bubble Tea host (sidecar), which is a hard
  constraint, not a preference.
- Preserve the interaction contracts users already know; the framework changes,
  the product does not.
- Keep the dependency's license and maintenance profile acceptable for a tool
  Marcus depends on daily.

## Considered options

1. **Port the existing renderer line by line to Go.** Preserves every behavior
   by construction and carries 12,000 lines of plumbing forward, most of which
   the framework would otherwise provide. It also forecloses embedding, because
   a hand-rolled event loop cannot be mounted inside another program's `Update`.
2. **tcell or termbox directly.** Lower-level and stable, but it is option 1
   with a different bottom layer, and sidecar cannot host it.
3. **Bubble Tea v1.** Mature and widely deployed, but the v2 line is where the
   cell renderer, keyboard/mouse improvements, and declarative view
   configuration live, and starting on v1 means paying the v2 migration during
   the port rather than before it.
4. **Bubble Tea v2 with the current Charm v2 stack.**

## Decision

Choose option 4. The stack is:

- **Bubble Tea v2** for the Elm-style update loop and terminal lifecycle;
- **Bubbles v2** for text inputs, text areas, viewports, lists, and help;
- **Lip Gloss v2** for layout and semantic styles; and
- **Huh v2** *selectively*, for self-contained forms or its screen-reader
  friendly prompt mode — never as a second application architecture alongside
  Bubble Tea's.

All four are MIT-licensed. Bubble Tea v2 currently provides a cell-based
renderer, keyboard and mouse input, clipboard support, color downsampling, and
declarative view configuration.

Model the TUI as explicit child models rather than one enormous `Update`:
navigation and current view; task list and stable selection; right-side
details/editor; a modal and action-palette stack; the agent prompt and FIFO
activity queue; the file-change watcher; and theme, dimensions, and mouse hit
regions.

Every asynchronous command returns a message carrying the stable task ID and
the store revision it started from, so the receiving model can discard, merge,
or surface a stale result instead of applying it to whatever row happens to be
selected.

Port user-visible behavior, not the renderer. `Model`/`Update`/`View` replaces
manual `IO.select` and frame painting; it does not wrap them. The contracts
that must survive unchanged:

- selection follows stable identity across refreshes;
- the right panel preserves scroll for the same task and resets for another;
- save-on-blur commits before focus moves (ADR-0002);
- contextual Tab, Shift-Tab, double-Escape, and finish-edit remain coherent;
- clipboard and paste messages reach the focused field or modal;
- queued agent requests stay FIFO, preserve per-request history, and cancel
  their whole process tree;
- mouse hit testing uses terminal cells, including wide characters; and
- `NO_COLOR`, 16-color, 256-color, truecolor, narrow-terminal, and accessible
  modes remain deliberate outputs, not accidents of the renderer.

Verification is deterministic model tests plus rendered terminal fixtures at
fixed sizes, exercising real key, paste, mouse, resize, file-change, and
process-completion messages.

## Consequences

- The port takes a substantial third-party dependency on a stack that moves.
  Charm's v1-to-v2 transition is itself evidence that major versions land; a
  future v3 migration is a cost this decision accepts, offset against not
  maintaining a terminal renderer.
- Reusing ADR-0004's responsive named widths, the stable-ID selection model,
  the editor-session design (ADR-0003), and the action registry is expected.
  Those are product decisions and survive the framework change.
- Huh's presence is a standing temptation to build a second architecture. The
  rule — self-contained forms only — needs enforcing in review, because the
  failure is gradual.
- Accessible and constrained-terminal modes are easy to lose when a framework
  owns rendering. They stay explicit gate criteria (72-column, narrow,
  `NO_COLOR`, themed, wide-character proof), not assumed properties.
- The TUI is the last phase to port and the least mechanically verifiable:
  frame comparison is weaker evidence than byte comparison. Expect the TUI to
  be where residual behavior differences hide.

## Related

- [ADR-0001](0001-embedded-terminal-form-library.md) — the extractable-library
  posture this continues
- [ADR-0002](0002-save-task-fields-on-blur.md),
  [ADR-0003](0003-task-edit-session-undo-coalescing.md),
  [ADR-0004](0004-responsive-task-panel-layout.md) — interaction contracts to
  preserve
- [ADR-0017](0017-embeddable-tui-component.md) — the embedding constraint that
  makes this a hard requirement rather than a preference
- Plan: [`docs/plans/active/tasks-go-port-plan.md`](../plans/active/tasks-go-port-plan.md),
  "Rebuilding the TUI with Bubble Tea"
