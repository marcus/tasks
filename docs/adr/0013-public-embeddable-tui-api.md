# ADR-0013: A Tasks-owned public API for embedding the TUI

Status: Accepted and implemented

Date: 2026-08-09

## Context

The Tasks interaction model is useful inside another Bubble Tea application,
initially the Tasks tab planned for Sidecar. Copying the model or shortcut table
would split behavior, while exposing `internal/tui` would couple hosts to storage,
rendering, and queue implementation details. The cross-repository sequence and
ownership split are recorded in the
[Tasks-in-Sidecar plan](../plans/active/tasks-in-sidecar.md#packet-0-make-the-tasks-tui-embeddable).

## Decision

`github.com/marcus/tasks/pkg/tui` is the supported host boundary. It contains
only Tasks-owned types and Bubble Tea v2 types; it does not expose internal
models or import Sidecar types.

Construction is `NewEmbedded(EmbeddedOptions) (*Model, error)`. The final
options are:

- required `SessionNamespace`;
- presentation-only `InitialView` and `InitialContexts`;
- `SuppressFooter` and `SuppressQuit` host-ownership switches;
- semantic `ThemeOptions`; and
- `Environment`, primarily for deterministic consumers and tests.

Construction uses normal Tasks configuration resolution and refuses an
unconfigured store. Initial view and context filters do not mutate task records.
The namespace is a validated path component and isolates host state at
`$XDG_STATE_HOME/tasks/hosts/<namespace>/tui.json`; it can never overwrite the
standalone `tasks-tui` session.

### Host lifecycle

The host calls `Init`, schedules its returned `tea.Cmd`, forwards Bubble Tea v2
messages through `Update`, schedules returned commands, and renders with
`View(width, height)`. `Close` is required at host shutdown: it saves the
namespaced session and shuts down the Tasks-owned agent queue and provider
processes. It is idempotent.

Tasks owns configuration, task reads and mutations, rendering, prompt/provider
selection, queue state, and task-specific overlays. The host owns placement,
available size, surrounding header/footer, event-loop scheduling, and its own
application lifecycle. `SuppressFooter` lets the host render shared chrome; the
standalone footer remains the default.

Embedded quit never returns `tea.Quit` to terminate the host. With
`SuppressQuit`, Tasks refuses the quit action. Otherwise it records a request;
the host observes `QuitRequested`, may acknowledge it with `ClearQuitRequest`,
and decides when to call `Close`.

### Focus and commands

`FocusContext` and `ConsumesTextInput` tell the host where keys belong. Stable
contexts are exported as `FocusList`, `FocusDetail`, `FocusTaskEdit`,
`FocusModal`, `FocusModalFilter`, `FocusForm`, `FocusPicker`,
`FocusContextPicker`, `FocusFilter`, `FocusPrompt`, `FocusResponse`,
`FocusResponseDetail`, `FocusAgentActivity`, and
`FocusAgentActivityFilter`.

`ExportBindings`, `ExportCommands`, and `ExportContexts` are projections of the
single Tasks shortcut registry. A host filters them by the current focus, then
calls `CommandAvailable` before displaying or invoking a command. Multiple
conditional commands may share a default key; availability selects the exact
semantic action. `Invoke` executes that stable command ID without replaying an
ambiguous key and returns any `tea.Cmd` for the host to schedule. Direct key
updates, exported availability, and invocation must remain parity-tested.

`CurrentView`, `Contexts`, and `Warnings` expose the remaining read-only host
state. These names and their semantic behavior are the public compatibility
surface; new implementation details stay behind the package.

## Consequences

- Another Go host can embed the complete Tasks experience without importing
  `internal/tui`, reading JSONL, or owning provider cleanup.
- Standalone and embedded modes share one runtime and shortcut source of truth.
- Hosts must manage commands, sizing, focus routing, quit acknowledgement, and
  `Close`; omission of `Close` is a lifecycle bug.
- The package is intentionally a Go/Bubble Tea component, not a general remote
  API. A service boundary should be added only for a journey that requires one.
