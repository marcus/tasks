# Sidecar-style modals plan

> **Progress (2026-08-22):** Packets 0–3 are code-complete on branch `modals`.
> Packet 0 slots live in `internal/tui/term/theme/theme.go`; Packet 1
> primitives are `internal/tui/{chrome,button,keychips,scrollregion,
> statusline}.go`; Packet 2 rewrote `fieldmodalrender.go` on them; **Packet 3
> (draggable scrollbars) is implemented**: thumb-grab drag with grab-offset
> preservation, track jump-to-spot via `OffsetAtRow`, live params rebuilt each
> motion tick, motion/release routed through `fieldModalMouse`, and
> `TextArea.ScrollToRow` in termform for caret-coupled absolute scroll. The
> documented wheel-preview asymmetry is RESOLVED-as-legible: preview semantics
> stay, and the painted thumb makes the window's position visible.
>
> Deliberately staged, not forgotten (per review):
> - `BoxAccent` / `modal_border_accent` has no production caller until a host
>   (or config) opts in — the slot contract ships first.
> - `PaintScrollbar`, `RowForOffset`, `ClampScroll`, `ChromeHorizontal` are
>   exported for the Modal/RightPanel follow-ups.
> - Plain choice fields no longer echo the live query next to the selection
>   (prefix-jump makes it transient); the filtered list itself is the feedback.
>
> Evidence: before/after betamax captures of the delegate modal (open + armed)
> in `docs/plans/active/evidence/`. Remaining: Packet 4 sidecar adapter edit
> (separate repo task).

- **Plan date:** 2026-08-21
- **Repos:** `~/code/tasks-modals` (this checkout), reference implementations in
  `~/code/sidecar` and the `~/code/sidecar-scroll` worktree
- **Design mockup:** `~/code/tui/mockups/tasks-delegate-modal.tui.yaml`
  (authored with the TUI design skill from
  `~/code/clara-home/skills/tui-design`; render with
  `node ~/code/tui/bin/tui.js render <file> --state "<state>"`)
- **Reference screenshot:** Sidecar "Switch Project" modal (2026-08-21)

## Goal

Adopt Sidecar's modal design language in the Tasks TUI: richer, fully featured
form fields; buttons as primary interactive surfaces; full scrollbars with
mouse-drag support; cleaner overall chrome. The **delegate modal** is the first
redesign target. Two forward-looking constraints are planned for up front:

1. Modals will eventually inherit their themes from Sidecar the way td does —
   Sidecar projects its normalized palette into each embedded app's public
   semantic theme contract. Tasks already has this seam (`pkg/tui.ThemeOptions`,
   sidecar adapter `internal/plugins/tasks/theme.go`); new chrome must be added
   as named slots so the adapter can grow with it.
2. Mouse-scrollable scrollbars are landing in sidecar main from the
   `sidecar-scroll` worktree (`internal/scroll/thumb.go`, geometry-with-state
   rendering). Tasks will port the state-free math and the gesture contract,
   adapted to Bubble Tea v2 mouse messages.

## Current state

### Tasks

- Three overlay systems share `internal/tui`: line-list `Modal` (`modal.go`),
  single-field `QuickForm` (`quickform.go`), and multi-field `FieldModal`
  (`fieldmodal.go` + `fieldmodalinput.go` + `fieldmodalrender.go` +
  `fieldmodalhost.go`). Rendering is custom line painting composited by cell
  slicing (`overlay.go`); paint and click hit map are produced in one pass.
- The delegate flow (`D` → `DelegateSelected()`, `delegation.go:99`) builds a
  FieldModal with three fields: Assignee (`FieldChoice`, free text, recent
  assignees + "agent pool"), Mode (`FieldChoice`, vocabulary read at paint
  time), Note (`FieldTextArea`). Release/Undelegate are `FieldModalAction`s
  with two-press arming; submit is one `application.DelegationCommand`.
- Primitives live in `internal/tui/termform/fields.go` (Input, TextArea,
  ChoiceField/Select/MultiSelect). There is no general button component — the
  button row is painted text with recorded column spans. Geometry is frozen at
  construction.
- Styling goes through the `Styler` seam (`internal/tui/style.go`) with
  semantic slots defined in `internal/tui/term/theme` (slot defaults
  `theme.go:42-134`, mono fallback, generated themes, config overrides). No
  hardcoded colors in components — keep it that way.
- Scrolling is four independent implementations (modal body, right panel,
  textarea, choice-list window). Wheel over a choice list is preview-only;
  nothing renders a scrollbar thumb/track.

### Sidecar (design source)

- Declarative framework `internal/modal`: `Modal` + `Section`s (`Text`,
  `Spacer`, `Custom`, `ScrollingCustom`, `Btn`/`Buttons` with focus/hover/
  danger/disabled styles, `Checkbox`, `List`, `Input`/`Textarea` in bordered
  boxes that track focus/hover, `Combo` input + floating filtered dropdown).
  Options cover variants, primary action, initial focus, fixed footer,
  backdrop-click dismiss. Layout engine slices a viewport, renders a scrollbar
  column, registers hit rects, keeps focused elements visible.
- Theme inheritance is per-host projection: sidecar maps its normalized
  palette into each embed's public semantic contract (td monitor:
  `internal/plugins/tdmonitor/theme.go`; tasks: `internal/plugins/tasks/
  theme.go` → `tasksui.ThemeOptions.Colors` overlay) and re-applies live on
  `app.ThemeChangedMsg`. Sidecar never passes theme names or shares registries.

### sidecar-scroll (inbound)

- State-free math in `internal/scroll/thumb.go`: `ThumbLocFor`,
  `OffsetAtRow`, `RowForOffset` (+ `Bounds` helpers) over plain ints.
- `ui.RenderScrollbarWithGeometry` / `WithState` add hover/drag emphasis via
  intensity modulation; idle output unchanged.
- Gesture contract per surface: cache params+geometry at render, register
  1-col track/thumb hit regions after content regions, thumb press captures
  grab offset, track press jumps so the thumb top anchors at the click
  (macOS jump-to-spot), drag recomputes offset via `OffsetAtRow` with live
  params rebuilt each tick.

## Design decisions (settled)

1. **Evolve Tasks' own primitives; do not import Sidecar code.** Sidecar's
   packages are `internal/` and its modal framework assumes its own mouse
   handler. Tasks adopts the design language and the small state-free math
   functions, implemented behind Tasks' existing seams.
2. **Everything new is a styler slot.** Buttons, field borders, chips,
   scrollbars, backdrop/shadow get semantic slot names in
   `internal/tui/term/theme` with defaults, mono fallback, generated-theme
   coverage, and config overrides. Components never see hex.
3. **New slots join the public embed contract** (`pkg/tui.ThemeOptions.Colors`)
   as an overlay, so standalone config still wins for unspecified slots and
   the Sidecar adapter can project them later without a breaking change.
4. **Behavior of the delegate modal is frozen** during the visual rebuild:
   validation rules, esc double-press discard latch, `ctrl-s` newline in note,
   two-press arming for Release/Undelegate, refusal messages rendered inline,
   single command write + undo step, shortcut registry wiring.
5. **Scrollbar math is vendored-adapted, not imported:** copy the ~80 lines of
   state-free thumb geometry into `internal/tui` (new file, attribution
   comment), keeping Tasks free of a dependency on sidecar internals; gestures
   are re-implemented on Bubble Tea v2 mouse messages.

## Mockup

`~/code/tui/mockups/tasks-delegate-modal.tui.yaml`, theme `sidecar`, states:

1. **Assignee dropdown open** — task context row under the title; labeled
   bordered combo ("DELEGATE TO") with prompt, value, block cursor, and a
   "+ new" chip beside it; attached dropdown with cursor column, option
   metadata (reserved pool / recent / free text), and a thumb+track mini
   scrollbar; MODE select with hint; NOTE box with char counter and scrollbar;
   status/hint slot; buttons `⏎ Delegate` (filled primary), `ctrl-r Release`
   and `ctrl-x Undelegate` (danger outline), `esc Cancel` (muted chip);
   footer keybinding chips.
2. **Release armed** — warning variant border, armed warning line in the
   status slot, Release button inverted to filled danger, footer switches to
   "disarm & close".

This mockup is the visual acceptance target for Packet 2, adjusted for real
terminal widths. Vector/PNG exports of both states live beside it
(`delegate-state1.svg/.png`, `delegate-state2.svg/.png`).

## Work sequence

### Packet 0 — theme slots and tokens

- Add slots to `internal/tui/term/theme`: `modal_border_accent`,
  `modal_border_warning` (or reuse existing error/warning slots where they
  exist), `button_primary_bg/fg`, `button_danger_fg`, `button_danger_armed_bg/fg`,
  `button_muted_bg/fg`, `chip_bg/fg`, `field_border`, `field_border_focused`,
  `scrollbar_thumb`, `scrollbar_track`, `modal_backdrop`.
- Extend defaults table, mono theme, generated themes, and config override
  path; extend the public `pkg/tui.ThemeOptions.Colors` overlay keys to match.
- Tests: slot coverage parity test (every slot has default + mono value),
  overlay-precedence test in `pkg/tui`.

### Packet 1 — shared modal primitives (library layer, `internal/tui`)

- **Box/chrome painter**: rounded border with variant color, title in border,
  drop shadow, dimmed backdrop (extend `overlay.go` compositing).
- **Button component**: label + key hint, variants primary/danger/muted,
  focus + hover states, arming state, cell-accurate hit regions (replace
  FieldModal's painted-span approximation).
- **ScrollRegion**: clamp/window/status logic extracted once, rendering
  thumb+track columns for any line list (feeds dropdowns, textareas, modal
  bodies; later adoptable by Modal and RightPanel).
- **Combo field**: bordered input + floating filtered dropdown overlay with
  its own ScrollRegion and free-text mode (generalize FieldModal's local
  choice control; reconcile with `termform.Select`'s Return-opens-list
  behavior rather than duplicating semantics silently).
- **Footer key-chip row** and **status/error slot** components.
- All painters are pure functions of (state, width, styler) — headless-testable.

### Packet 2 — delegate modal rebuild on the new primitives

- Rebuild the delegate FieldModal to match the mockup within real terminal
  sizes; keep every behavior contract listed above.
- Update binding-parity tests, `delegatemodal_test.go`, `fieldmodal_test.go`,
  and the `cmd/tasks` journey test; add click-target tests for buttons and
  scrollbar regions.
- Capture before/after screenshots of the running TUI (betamax) as review
  evidence.

### Packet 3 — mouse-scrollable scrollbars (after sidecar-scroll merges)

- Vendor the thumb math into `internal/tui/scrollbar.go` (or `internal/tui/
  scroll/`): `ThumbLocFor`, `OffsetAtRow`, `RowForOffset` with round-trip
  property tests lifted conceptually from `internal/scroll/thumb_test.go`.
- Render with hover/drag state; wire wheel, thumb-grab drag (with grab-offset
  preservation), and track jump-to-spot using Bubble Tea v2 mouse messages in
  `fieldmodalinput.go` / the new ScrollRegion.
- Resolve the documented asymmetry where wheel-over-choice-list is currently
  preview-only.

### Packet 4 — Sidecar theme inheritance readiness

- Extend sidecar's tasks adapter (`internal/plugins/tasks/theme.go`) to
  project the new slots from its normalized palette, following the td-monitor
  pattern; confirm live re-theming via `app.ThemeChangedMsg` updates all new
  chrome.
- Verify unspecified-slot behavior: standalone Tasks config remains the base;
  host colors overlay; `ReplaceColors` semantics untouched.
- This packet lands in both repos; the tasks-side work is only the contract
  surface (done in Packets 0–3), so the sidecar change stays an adapter edit.

### Follow-ups (out of scope here)

Roll QuickForm, confirms, help modal, and the right panel onto the same
primitives; collapse the duplicated scrolling implementations onto
ScrollRegion.

## Dependencies and gates

- Packet 3 depends on `sidecar-scroll` merging to sidecar main; Packets 0–2
  can proceed now (keyboard/wheel scrolling ships first, drag added after).
- Per repo gates: `go test ./...`, `go test -race ./...`, `go vet ./...`,
  `gofmt -l`, builds of all three commands. Every packet gets independent
  review before completion. After merging in canonical `main`,
  `make install-local` (this checkout is a worktree — use `make
  install-worktree` only if it should become active).

## Acceptance evidence

- Mockup states render clean at 100×40 and at 160×45 (no border drift).
- Delegate modal behavior tests pass unchanged in intent (same validation,
  arming, submit, undo); new click/drag tests pass.
- Theme slot tests prove defaults, mono fallback, config override, and
  `ThemeOptions` overlay precedence.
- Before/after betamax screenshots reviewed; Sidecar screenshot used as the
  style reference.

## Open questions

- Backdrop dimming + shadow cost in the cell compositor on large terminals —
  measure during Packet 1; drop shadow if it complicates the diff-based
  repaint path.
- Whether Combo should reuse `termform.Select`'s open/close keyboard model or
  formalize the FieldModal-local one as the standard; decide in Packet 1 with
  the editor form in mind.
- Exact timing of Packet 4's sidecar-side edit (needs a matching sidecar
  checkout task once slots stabilize).
