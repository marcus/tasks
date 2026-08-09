# ADR-0014: Bubble Tea v2 with standalone interaction compatibility

Status: Accepted and implemented

Date: 2026-08-09

## Context

Bubble Tea v1 and v2 use incompatible message, command, model, and view types.
An embedded Tasks model could not participate directly in a Bubble Tea v2 host
while Tasks remained on v1. Translating at the host boundary would create a
second input model and make key, paste, mouse, and lifecycle behavior drift.
Packet 0.1 of the
[Tasks-in-Sidecar plan](../plans/active/tasks-in-sidecar.md#01-align-the-terminal-stack)
requires binary message compatibility without making the migration a visual
redesign.

## Decision

Tasks uses the `charm.land/bubbletea/v2` import family (initially v2.0.7) for
both `tasks-tui` and `pkg/tui`. The standalone command and public component are
adapters over the same `internal/tui.NewRuntime` model. A v2 host passes
`tea.Msg` and schedules `tea.Cmd` values directly, with no translation layer.

The v2 event contract is explicit:

- `tea.KeyPressMsg.Text` carries printable input, including space, Unicode, and
  multi-rune text. Shortcut lookup converts the event to the registry's
  canonical terminal sequence; unsupported modifier combinations and key
  releases do not fall through as printable actions.
- Bracketed paste arrives as `tea.PasteMsg.Content` and is always routed as
  input, never replayed as shortcuts.
- Mouse input is handled from v2 `tea.MouseMsg` click and wheel events.
- `tea.WindowSizeMsg` is the coherent sizing event. The embedded `View(width,
  height)` applies the host's allotted size and returns composable text.
- The standalone model returns a v2 `tea.View` whose `AltScreen` and
  `MouseMode` fields own terminal behavior. The embedded wrapper does not take
  alternate-screen or mouse-mode ownership from its host.

The migration carries a compatibility promise: `tasks-tui` keeps its views,
selection, shortcuts, generated help, forms, modals, task editing, agent/model
controls, mouse behavior, fixed-size layout, queue shutdown, and standalone
session location. Changes to those behaviors require an intentional product
decision, not incidental v2 adaptation.

## Proof obligations

Bubble Tea upgrades or input-boundary changes must preserve:

- table tests for printable, Unicode, multi-rune, control, alternate, CSI,
  key-release, paste, and mouse events;
- fixture-driven model behavior and fixed-size rendered-frame comparisons;
- focused and full Go tests, race coverage at the model/store and queue seams,
  vet, formatting, and builds of all three commands; and
- an out-of-module v2 consumer that constructs `pkg/tui`, forwards real v2
  messages, renders at host size, saves and closes its namespaced session, and
  proves provider cleanup without changing copied task data.

## Consequences

- Tasks and a v2 host share one event vocabulary and runtime seam.
- The standalone terminal remains first-class rather than becoming a thin test
  harness for embedding.
- Future Bubble Tea upgrades must satisfy both the standalone compatibility
  promise and the external-consumer proof before landing.
