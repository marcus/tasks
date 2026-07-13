# ADR-0004: Use responsive named widths for the task panel

Status: Accepted and implemented

Date: 2026-07-13

## Context

The current detail panel uses a centrally calculated 40 percent split and a
28-column panel minimum. A multi-field form, long notes, option lists, and a
calendar need more space, but permanently widening the panel would reduce list
context during ordinary reading. The user also wants a hotkey to resize the
panel while form elements remain usable down to a defined minimum.

Persisting a raw number of columns would behave poorly after terminal resize or
across terminals. Letting fields measure the terminal independently would create
competing geometry and accidental clipping.

## Decision drivers

- Reading and editing can favor different amounts of list context.
- A resize action preserves focus and pending text and never triggers blur.
- Layout decisions have one source of truth.
- Fields reflow at documented content widths and do not promise impossible
  behavior below their minimum.
- The preference survives sessions and adapts to a different terminal size.

## Considered options

1. Keep one fixed ratio. Simple, but either forms are cramped or reading wastes
   list space.
2. Store an arbitrary panel column count. Flexible, but fragile across terminal
   sizes and harder to test or explain.
3. Step through named responsive modes that `ScreenLayout` centrally clamps
   against terminal, list, and form constraints.

## Decision

Choose option 3.

The modes are `compact`, `standard`, `wide`, and `focus`:

- compact targets about 32 panel-content cells for short read-only details;
- standard retains the current roughly 40 percent reading split;
- wide targets roughly 58 percent for forms and notes;
- focus gives the panel the available body, preserving a narrow list strip only
  when space permits.

`Ctrl-K` grows and `Ctrl-L` shrinks the panel, stepping through the mode order;
the action palette exposes `Grow task panel` and `Shrink task panel`. Inside
task-edit text fields `Ctrl-K` shadows readline kill-to-end — an accepted
trade; `Ctrl-U`/`Ctrl-W` still kill, and the agent prompt keeps `Ctrl-K`
kill-line. The session persists the named preference, not a raw width.

`ScreenLayout` remains the only geometry authority and returns the final content
width after panel chrome. At 48 or more content cells, labels and short controls
may share a row. From 32 through 47, labels stack and help/errors wrap. Thirty-two
content cells is the initial editable minimum. Editing promotes to a mode that
can supply it; when the terminal itself cannot, the app keeps a usable read view
and reports the required width instead of rendering a broken editor.

The named zero-footer editing minimum is **46×8 terminal cells**: 46 columns
provide the eight-cell list strip plus 32 panel-content cells, while eight rows
provide the frame chrome, panel title and divider, and one compact focused-field
row. Each active footer/help row raises the minimum height by one. Below that
minimum the app stays in read mode, or suspends a live editor into read mode so
normal list keys remain available and the same draft session can resume after a
resize.

Panel-mode and terminal-resize events preserve target ID, focus, buffer, cursor,
errors, picker state, and scroll. They are explicitly not focus-leave events.

## Consequences

Users can trade list context for editing space with predictable states, and
tests can pin exact breakpoints. The named preference continues to make sense on
a terminal with different dimensions.

`ScreenLayout` must reconcile the current 28-column panel minimum, list minimum,
panel chrome, and edit minimum centrally. Renderers receive final dimensions and
must not call `IO.console` or invent separate clamps.
