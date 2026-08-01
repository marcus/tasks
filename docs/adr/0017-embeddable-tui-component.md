# ADR-0017: The TUI is an embeddable component; the sidecar plugin waits

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

Sidecar already embeds td's monitor as a plugin: it imports
`github.com/marcus/td/pkg/monitor`, wraps it in a ~600-line adapter
(`internal/plugins/tdmonitor`), and gets the full monitor UI inside its own
layout, theme, keymap, and command palette. A `tasks` panel in sidecar is one
of the reasons to consider this port at all. A Ruby TUI can never be embedded
that way; a Bubble Tea one can be almost for free — but only if the embedding
contract is adopted from the first TUI slice.

The retrofit cost is what makes this a Phase 0 decision rather than a later
one. Sidecar's contract touches styling, input routing, focus, and lifecycle —
most of a TUI's connective tissue. Adding it afterward is a second rewrite.

One caution from td's own history: the modal-library migration inside td
stalled halfway. Its hardest modals — the form editor, issue details — never
moved off the legacy path, so the section model is **unproven against a really
complex modal**.

## Decision drivers

- The embedding seams must be present from the first Phase 7 slice.
- Most of the contract is architecture the standalone TUI wants anyway; the
  genuinely embedding-only cost should be named honestly and kept small.
- Proving embeddability must not require building sidecar's plugin.
- td's modal and mouse work should be adopted, not reinvented a third time.

## Considered options

1. **Build the standalone TUI; retrofit embedding later.** Defers work into the
   period where it is most expensive, and the retrofit touches the whole
   connective tissue.
2. **Build the sidecar plugin as part of the port.** Proves the contract for
   real, and expands the port's scope into a second repository while the
   standalone TUI has not yet reached parity.
3. **Ship an embeddable component plus a smoke test that mounts it; defer the
   plugin.**

## Decision

Choose option 3.

`pkg/tui` is a public, importable component (ADR-0014); `cmd/tasks-tui` is a
thin shell over it. An `EmbeddedOptions`-style constructor plus `Close()` is the
whole entry surface. Four properties define the contract:

**The component does not own the terminal.** In embedded mode it never touches
terminal-level features: no alt-screen, no mouse-enable, no cursor control, and
no `tea.Quit` that could tear down the host. Sidecar intercepts quit
defensively; the component should not emit it in the first place. `View()`
returns composable content the host constrains, places, and themes.

**Injection points instead of global style.** Base directory, refresh interval,
panel-border renderer, modal-border renderer, markdown theme palette, and a
swappable clipboard function — the seams td's `EmbeddedOptions` already
provides, which are how sidecar matches its theme and fixes WSL clipboard
without patching td. This means **no package-level singleton styles**: every
style derives from an injected theme with a default.

**Shortcuts exported as data.** One keymap registry is the single source of
truth for every binding, with contexts as first-class values. It exposes
`ExportBindings()` and `ExportCommands()` for the host's footer hints, command
palette, and conflict awareness, plus two live queries: `CurrentContextString()`
so the host's palette follows the embedded UI's state, and `ConsumesTextInput()`
so the host stops intercepting printable keys while the user is typing. Raw key
messages still flow to the embedded `Update` for actual handling. The registry
also generates standalone help and enables user rebinding, so it is not
embedding-only cost.

**Mouse events in component coordinates.** Every hit region is computed from
the width and height of the last `WindowSizeMsg`; the component never assumes
it renders at the terminal origin. Sidecar subtracts its header offset before
forwarding events, and re-sends `WindowSizeMsg` on project switches precisely
because td's bounds are only recomputed on receipt. Regions rebuild on every
size change.

**Message discipline a host can live with.** Construction must be cheap, with
heavy work arriving later via a ready message; `Init`/`Start`/`Stop` are
re-entrant and `Stop` releases everything; async results carry an epoch so
stale ones are dropped; `StatusMessage` is exposed so the host can mirror it as
toasts; cross-plugin actions cross as exported typed messages in both
directions. The `tasks` minimum is an inbound open-task-by-ID message and an
exposed status/error signal.

**td's modal and mouse packages are adopted, and extracted.** `pkg/monitor/modal`
(declarative sections reporting measured focusables, viewport-clipped hit
regions, built-in Tab/Enter/Esc navigation) and `pkg/monitor/mouse` (a
rectangular hit-region map with double-click and drag tracking) encode real
fixed bugs and should not be reimplemented a third time. Extract them, plus the
keymap registry/export shape, into a small shared module that `tasks` uses from
its first TUI slice and td migrates to opportunistically; they depend only on
the Charm stack. Importing `github.com/marcus/td/pkg/monitor/...` directly works
but makes `tasks` depend on td's whole module; a private copy guarantees
divergence.

Extraction is the moment to fix five known modal flaws, because each sits on a
seam `tasks` needs and an API break is free while the module has one consumer:

- package-level hardcoded ANSI-256 styling with no setter (and a
  `ModalRenderer` that belongs to td's *older* hand-rolled path, so
  library-based modals ignore the host theme even in sidecar today) becomes an
  injected theme with current values as its default;
- hand-synced chrome hit-region offsets (`border(1) + padding(2)` as a comment)
  become measured, the way sections already are;
- centered-on-screen compositing with absolute hit regions becomes explicit
  position, passed in or returned;
- input routing that offers each message to every section until one responds
  becomes routing only to the section the layout pass knows owns focus; and
- baked-in Esc-means-cancel and an English hint line become overridable policy
  with hints derived from the keymap registry — because `tasks`'s contracts
  (double-Escape, save-on-blur, finish-edit) must be expressible *inside* the
  engine, not by bypassing it. Conformance is against `tasks`, not against td's
  habits.

**The sidecar plugin itself is deferred.** The first release ships the
component and the architecture; `internal/plugins/tasks` in sidecar is a
separate, small project once the standalone TUI has passed parity, and a
natural first exercise of the contract.

Embeddability is proved cheaply: a smoke test mounts the root model inside a
trivial host program — fixed viewport, offset mouse events, injected renderers,
exported bindings consumed. That is a Phase 7 gate criterion.

## What this ADR cannot decide yet

**Whether td's section model survives a complex modal.** It stalled in td
against exactly the modals that resemble the `tasks` editor. The evidence that
would settle it is a working `tasks` editor-equivalent modal built on the
extracted library, in an **early** Phase 7 slice rather than the last one. If
it does not hold, the choice is between deepening the library and keeping a
bespoke editor path — and that decision should be made against a working
attempt, not now. Finishing td's own internal migration is td's backlog, not a
precondition here.

## Consequences

- Real added cost: the injection seams, the exported keymap metadata, the
  shared module coordination, and the modal-library refactor. Modest — the
  section interface and mouse package barely change — but not zero.
- Most of the rest (keymap as data, hit regions from delivered size, no
  terminal globals, cheap construction) is architecture the standalone TUI
  should have regardless, and the existing action registry and cell-based hit
  testing in the Ruby TUI show the shape is already proven in this product.
- A shared UI module means a third repository in the coordination surface, and
  an API break there now affects td as it migrates.
- The smoke test proves the seams exist, not that sidecar is happy. The first
  real plugin will still find something.
- If the sidecar panel never gets built, this ADR's cost was spent on
  architecture that stands on its own — which is the reason it is affordable.

## Related

- [ADR-0016](0016-bubble-tea-v2-tui-foundation.md) — the framework this
  presupposes
- [ADR-0014](0014-go-package-boundary.md) — why the TUI is `pkg/`, not
  `internal/`
- Plan: [`docs/plans/active/tasks-go-port-plan.md`](../plans/active/tasks-go-port-plan.md),
  "Design the TUI to be embeddable in sidecar"
