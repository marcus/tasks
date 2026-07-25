# Plan: initial scroll-wheel and click support in the TUI

Status: implemented

Date: 2026-07-24

## Outcome

`bin/tasks-tui` gains a small, conventional set of pointer gestures:

- The wheel scrolls whatever is under the pointer — the task list, the right
  panel, an open modal, or the agent response pane in the footer.
- A left click on a task row selects it; a click on the already-selected row
  opens or closes its detail panel (the same thing `return` does).
- A left click on a header tab switches to that view.
- A left click on the footer prompt line focuses the agent prompt.
- Everything else (right/middle button, drag, motion) is decoded and ignored.

Every gesture resolves to an action the keyboard already has. Mouse support adds
no new capability and no new mutation path — it is a second way to reach the
existing handlers.

## Design constraint: mouse code must not know about tasks

The whole feature lands as three self-contained files plus geometry accessors on
`ScreenLayout`. `lib/tui/app.rb` grows one decode branch in the input pipeline
and one `case` that maps semantic intents onto handlers it already has.

```
bytes ──▶ Tui::Mouse        pure decoder: SGR sequence → Mouse::Event
                            (row, col, button, action, modifiers)

Event ──▶ Tui::HitMap       pure geometry: (row, col) → Hit(zone, payload)
                            built from a ScreenLayout, knows rectangles only

Hit   ──▶ Tui::MouseRouter  pure mapping table: (mode, zone, button, action)
                            → intent, e.g. [:select_row, 12]

intent ─▶ App#apply_mouse_intent   the only mouse-aware application code;
                                   calls existing select_row / open_detail /
                                   switch_view / scroll_* handlers
```

None of the three new files reference `App`, `Store`, `Views`, tasks, contexts,
or shortcuts. All three are testable with plain integers and strings.

## Inspiration from the Charm libraries

Four ideas worth borrowing, and one worth declining:

- **Bubble Tea** decodes mouse reports at the edge of the input reader and hands
  the program an ordinary message (`MouseMsg` with `X`, `Y`, `Button`, `Action`,
  `Shift/Alt/Ctrl`), rather than exposing escape bytes. Wheel directions are
  *buttons* (`MouseButtonWheelUp`), not a separate event kind, and press/release
  are *actions*. Copy that event shape: it makes a future `:motion` action a new
  enum value rather than a rework.
- **Bubble Tea is opt-in** (`WithMouseCellMotion`), because mouse tracking takes
  terminal text selection away from the user. Ours is opt-out via config, and
  never enabled on a non-tty.
- **`bubbles/viewport`** owns its own scroll offset and exposes
  `MouseWheelDelta` (default 3 lines). The component that owns the offset
  handles the wheel; the app only routes. `Tui::Modal` and `Tui::RightPanel`
  already expose exactly the right method — `scroll_line(delta, height)` — so
  routing is a one-line call, and 3 rows is the delta we adopt too.
- **`bubblezone`** resolves a click to a component id instead of making the app
  compute rectangles inline. `HitMap` is the same idea adapted to a line-based
  renderer: instead of marking rendered spans, we derive rectangles from the
  same `ScreenLayout` the renderer already consumes.
- **Declined:** Bubble Tea also enables the legacy X10 encoding
  (`\e[?1000h` reports as `\e[M` + three offset bytes) for old terminals. We
  enable SGR (`\e[?1006h`) only — see below.

## Why SGR-only

The existing reader decodes stdin as UTF-8 before dispatch
(`App#drain_utf8_input`, `lib/tui/app.rb:801`), scrubbing invalid bytes. Legacy
X10 mouse reports encode coordinates as `32 + n` raw bytes, so any click past
column 95 emits bytes ≥ 0x80 that are not valid UTF-8 and would be mangled
before dispatch. Supporting X10 would mean a byte-level side channel through the
UTF-8 assembler — a large change to the most delicate code in the TUI, for
terminals that predate 2012.

SGR reports are pure ASCII (`\e[<Cb;Cx;CyM` for press, `…m` for release), have
no column ceiling, and are supported by every terminal this TUI targets
(iTerm2, Terminal.app, kitty, ghostty, alacritty, WezTerm, tmux, screen). A
terminal that ignores `\e[?1006h` simply sends no reports, which is exactly the
degradation we want — no fallback path to test.

## Enabling and disabling

`App#run` (`lib/tui/app.rb:198`) already owns the terminal mode string. Mouse
tracking joins it, gated on `mouse_enabled?`:

```ruby
print "\e[?1049h\e[?2004h\e[?25l"
print Mouse::ENABLE if mouse_enabled?   # "\e[?1000h\e[?1006h"
...
ensure
  print Mouse::DISABLE if mouse_enabled? # "\e[?1006l\e[?1000l"
  print "\e[?2004l\e[?1049l\e[?25h"
```

- `\e[?1000h` — button press and release reports. Deliberately **not** `1002`
  (cell motion / drag) or `1003` (all motion): press and wheel are all the
  initial feature needs, and motion tracking multiplies both the report volume
  and the ways a stray event can reach a mutation handler.
- `\e[?1006h` — SGR extended coordinates.
- Disable runs **before** leaving the alt screen, mirroring the existing
  ordering rationale in that `ensure`: a raw terminal still reporting mouse
  bytes into a shell is worse than a lost view.

`mouse_enabled?` is false when: `$stdin` is not a tty, `TERM` is unset or
`dumb`, or the user opted out.

### Config

Add `mouse` to `Tasks::Config` beside `theme` (`lib/tasks/config.rb:40`,
`Paths` struct + `pick_*` + sources reporting), with the established precedence:
`TASKS_MOUSE` env > `mouse = off` in `~/.config/tasks/config` > default on.

Opting out is a real need, not a formality: while tracking is on, click-drag
selects nothing and the terminal's own copy gesture requires a modifier (shift
in Terminal.app/xterm/kitty, option in iTerm2). Document that in the README so
the first "I can't select text anymore" is self-served.

## Input pipeline plumbing

Mouse sequences ride the same stdin chunks as keys and must be split out in
`drain_key_data` (`lib/tui/app.rb:848`) **before** the generic CSI branch:

```ruby
elsif (seq = @key_data[Mouse::SEQUENCE])   # /\A\e\[<[0-9;]*[Mm]/
  handle_mouse(seq)
  @key_data = @key_data[seq.length..] || +""
```

Two details that are bugs if missed:

1. The generic CSI pattern is `/\A\e\[[0-9;?]*[A-Za-z~]/` — `<` is not in that
   class, so without a dedicated branch an SGR report falls through to
   `seq ||= "\e"` and dispatches **Escape followed by literal text**. In list
   mode that closes the detail panel and types `<0;12;34M` into nothing; in
   prompt mode it inserts garbage into the agent prompt. So this branch is
   required even if we only wanted to *ignore* mouse input.
2. `incomplete_escape_sequence?` (`lib/tui/app.rb:877`) must learn
   `/\A\e\[<[0-9;]*\z/`. A report split across two `read_nonblock` chunks is
   ordinary at 4096 bytes with fast scrolling; without this, the partial is
   flushed as a lone Escape after `ESCAPE_WAIT`.

`handle_key` stays keyboard-only. Nothing in the key path changes shape, so the
existing key tests keep their meaning.

**Coalescing** needs no new machinery: `loop_once` sets `dirty` once per readable
chunk and painting is deferred to `paint_if_needed`, so a 12-report wheel flick
already costs one repaint. Each report is applied in order; only the paint is
batched.

## Geometry: one owner for the arithmetic

`Frame.build` (`lib/tui/frame.rb:24`) computes the frame's row and column
arithmetic inline. `HitMap` must agree with it exactly, and duplicated arithmetic
drifts. So move the rectangles into `ScreenLayout`, which is already the
"pure geometry for one sampled frame" value object, and have both consumers read
them:

```ruby
layout.header_row          # 1
layout.body_rows           # 3...(3 + body_height)
layout.list_cols           # 2...(2 + list_width)
layout.panel_divider_col   # 2 + list_width
layout.panel_cols          # (2 + list_width + 2)...(2 + body_width)
layout.footer_rows         # (body_height + 4)...(body_height + 4 + footer_size)
layout.body_origin         # [3, 2] — modal/popup coords are body-relative
```

Today those derive from `FIXED_ROWS`, `body_height`, `list_width`, and
`render_panel!`'s `"│ "` divider. The values are already correct in
`ScreenLayout`; this exposes them and lets `Frame` consume them, so there is one
definition of "row 3 is the first body row."

The `HitMap` is resolved against the **last painted** layout, not a fresh
`terminal_size` read: a resize between paint and click must not move the target
out from under the pointer. `paint` already stores `@last_paint_size`
(`lib/tui/app.rb:326`); it additionally stores `@last_layout`, and
`handle_mouse` ignores events when that is nil (no frame drawn yet).

### `Tui::HitMap`

```ruby
HitMap.build(layout:, tab_spans:, row_count:, modal: nil, popup: nil, panel: false)
map.at(row, col)  # → Hit(zone:, payload:) — never nil
```

Zones, resolved outermost-overlay first (popup, then modal, then panel, then
list, then chrome):

| Zone | Payload | Source rectangle |
|---|---|---|
| `:popup_row` | visible line index | placed popup rect |
| `:modal_row` | visible line index | `layout.place_modal` rect |
| `:panel` | panel-relative row | `layout.panel_cols` × `body_rows` |
| `:panel_divider` | — | `layout.panel_divider_col` |
| `:list_row` | absolute row index (`viewport_offset + i`) | `list_cols` × `body_rows` |
| `:tab` | view key | tab spans in the header row |
| `:header` | — | rest of header row |
| `:footer_row` | footer line index | `footer_rows` |
| `:border` | — | outer ring and the two rules |
| `:outside` | — | anything beyond `width`/`height` |

`Hit` is a `Data.define`. `at` returns `:outside` rather than nil so no caller
has to nil-check coordinates from a terminal we do not control.

**Tab spans.** `App#header` (`lib/tui/app.rb:594`) builds the tab strip by
joining painted `" label "` cells with a space, after one leading space, so the
first tab starts at screen column 2. Rather than re-deriving that offset,
extract `Views.tab_spans(active:)` returning `[[key, start_col, end_col], …]`
measured with `A.vislen`, and have `header` render *from* the spans. The strip
and the hit spans then cannot disagree, including when a theme gives active tabs
different padding.

**Anti-drift test.** Beyond unit tests per zone, one cross-check in
`test/test_frame.rb`: build a frame from sentinel content (row *n* text is
`"ROW#{n}"`, panel lines `"PANEL#{n}"`), then for every cell a `HitMap` labels
`:list_row n`, assert the rendered glyph at that cell comes from row *n*'s span.
This is the gate that catches a future `Frame` change that shifts a column.

## Routing: `Tui::MouseRouter`

A pure table from `(mode, zone, button, action)` to an intent. It receives the
mode symbol and a hit — never the App:

```ruby
MouseRouter.intent(event, hit, mode: :list, panel: true) # → [:scroll_panel, 3]
```

Intents for the first release:

| Gesture | Where | Intent | Existing handler |
|---|---|---|---|
| wheel up/down | `:list_row` | `[:scroll_list, ±3]` | `move(delta)` |
| wheel up/down | `:panel` | `[:scroll_panel, ±3]` | `RightPanel#scroll_line` |
| wheel up/down | `:modal_row` | `[:scroll_modal, ±3]` | `Modal#scroll_line` |
| wheel up/down | `:footer_row` in the response pane | `[:scroll_response, ±3]` | `scroll_resp` |
| left press | `:list_row` (selectable, not current) | `[:select_row, n]` | `select_row` |
| left press | `:list_row` (already selected) | `[:activate_row, n]` | `open_detail` |
| left press | `:tab` | `[:switch_view, key]` | `switch_view` |
| left press | footer prompt line | `[:focus_prompt]` | `focus_prompt` |
| anything else | anywhere | `:ignored` | — |

**Wheel direction.** macOS natural scrolling — the default, and what Apple mice
and trackpads ship with — reports a *downward* gesture as wheel-**up**, so
taking the report at face value moves the list cursor opposite to the user's
hand. Deltas therefore follow the gesture, not the report name: wheel-up
advances, wheel-down goes back. All four targets share the one sign so no two
panes disagree about which way a flick goes. `MouseRouter.wheel_intent` holds the
only ternary; swapping its terms inverts everything.

Deliberate non-actions, each for a reason:

- **Release events (`m`) are ignored.** Acting only on press matches Bubble
  Tea's action split and prevents every click firing twice.
- **Right and middle press are ignored.** A context menu is a design question,
  not a plumbing one, and terminals disagree about which button reports what.
- **While an overlay owns input (`:modal`, `:modal_filter`, `:palette`,
  `:context_palette`, `:form`), only that overlay's own zones route** — the
  router's `OVERLAY_MODES` guard. A modal is a blocking box, so a click beside
  it must not move the selection or open a detail panel behind it, and clicking
  outside does not dismiss it either: the context picker stages a selection, and
  an accidental click discarding staged toggles is worse than reaching for
  Escape.
- **A pointer gesture aimed at the list blurs the agent prompt** (App's
  `LIST_FOCUS_INTENTS`), keeping the typed draft exactly as Escape does. Paint
  hides the row cursor while the prompt has focus, so without the blur a click
  would move an invisible selection and read as doing nothing.
- **In `:task_edit` mode, only wheel-over-panel routes; every click is
  ignored.** The editor saves on blur, so a click that moved focus would
  validate and write a field. Mouse editing gets its own plan.
- **Non-selectable rows (section headers, blank spacers) are ignored** —
  `Views::Row#selectable?` already answers this, and it is checked by
  `apply_mouse_intent` in App, not by the router, so the router stays free of
  row knowledge (the payload is an index; App owns what an index means).

### Why wheel-over-list moves the selection

There is no independent list scroll offset:
`ScreenLayout#viewport_offset` is derived from `selected`
(`lib/tui/screen_layout.rb:46`), so the viewport is always a function of the
cursor. "Scroll the list" therefore means "move the cursor by 3 selectable
rows," which is what `move(3)` does.

This is the right initial behavior — it never leaves the cursor off-screen, and
it needs no new state — but it is not what a mouse user expects from a long
list. A detached list viewport (cursor stays put, view scrolls, selection
clamps into view on the next key) is a real change to `ScreenLayout`,
`clamp_selection`, and the session state, and belongs in its own plan. Note the
limitation in the docs rather than half-implementing it.

## App integration surface

The entire application-side footprint:

```ruby
def handle_mouse(seq)
  return unless (event = Mouse.decode(seq))
  return unless @last_layout

  hit = hit_map.at(event.row, event.col)
  apply_mouse_intent(MouseRouter.intent(event, hit, mode: @ui.mode, panel: !@ui.panel.nil?))
end

def apply_mouse_intent(intent)
  case intent
  when :ignored then nil
  in [:select_row, n]     then select_row(n) if @rows[n]&.selectable?
  in [:activate_row, n]   then open_detail if @rows[n]&.selectable?
  in [:switch_view, key]  then switch_view(Views::TABS.index { |_, k| k == key } + 1)
  in [:scroll_list, d]    then move(d)
  in [:scroll_panel, d]   then panel_scroll_available? && @ui.panel.scroll_line(d, panel_body_h)
  in [:scroll_modal, d]   then @ui.modal&.scroll_line(d, screen_layout(...).body_height)
  in [:scroll_response, d] then scroll_resp(d)
  in [:focus_prompt]      then focus_prompt
  end
  @paint_dirty = true
end
```

`hit_map` is memoized per painted frame and invalidated in `paint`. No handler
signature changes; no new mutation path exists.

## Help and discoverability

`Shortcuts::REGISTRY` already carries an entry with `sequences: []` for a
palette-only action (`lib/tui/shortcuts.rb:79`), so mouse gestures can be
registered as registry entries with empty sequences, `key: "click"` / `"wheel"`,
and `palette: false`. The `?` modal then documents them through the existing
generated help path, with no second help mechanism and no fake key bindings.

## Implementation phases

Each phase is independently shippable and independently reviewable.

1. **Decode and swallow.** `lib/tui/mouse.rb`, `Mouse::ENABLE`/`DISABLE`, the
   `drain_key_data` branch, the `incomplete_escape_sequence?` addition, and the
   config toggle. Behavior: mouse input does nothing — and specifically no
   longer risks stray Escapes or literal text if a terminal is already sending
   reports. Full decoder unit tests.
2. **Geometry.** `ScreenLayout` rectangle accessors, `Frame` consuming them,
   `Views.tab_spans` with `header` rendering from it, `lib/tui/hit_map.rb`, the
   exhaustive zone sweep, and the `Frame`/`HitMap` cross-check.
3. **Routing and the first gestures.** `lib/tui/mouse_router.rb`,
   `handle_mouse`/`apply_mouse_intent`, tab clicks, row select/activate, and the
   four wheel targets.
4. **Component-level hits.** `ChoicePicker#hit(row_offset)` so a click lands on
   an option row in the context picker and action palette (the picker owns its
   own internal layout — border, query, blank, options, hint — so App keeps
   knowing nothing about it), and a `Views::Row` marker span so a click on the
   `▸`/`▾` cells toggles collapse. Both need row-relative column data the
   components own; neither is worth faking from App.
5. **Docs and proof.** README TUI section (including the text-selection
   caveat and the `mouse = off` escape hatch), `docs/cli-spec.md` config table,
   help entries, and the manual terminal matrix below.

## Test matrix

### Decoder (`test/test_mouse.rb`)

- Press/release/wheel for left, middle, right, wheel-up/down/left/right.
- `Cb` bit decoding: shift (+4), alt (+8), ctrl (+16), motion (+32), wheel
  (+64), extra buttons (+128).
- 1-based terminal coordinates convert to 0-based screen coordinates.
- Coordinates past 223 round-trip (the reason for SGR).
- `M` is press, `m` is release.
- Malformed and truncated sequences return nil rather than raising.
- Sequences split across two chunks reassemble and never surface as Escape
  (asserted through `read_key_chunk`/`drain_key_data`, not just the regex).
- A wheel burst in one chunk applies every report in order.

### Hit map (`test/test_hit_map.rb`)

- Every cell of a 24×80 frame and a degenerate 6×8 frame maps to exactly one
  zone (exhaustive sweep; cheap and catches off-by-ones at every edge).
- Panel present/absent, each `PANEL_MODES` width, and a non-zero
  `panel_offset`.
- Scrolled list: `viewport_offset` is added to the payload index.
- Modal and popup rectangles take precedence over the list beneath them, and
  their body-relative coordinates translate through `body_origin`.
- Footer with the agent response pane, the flash line, and the prompt.
- Tab spans match `header`'s rendered strip under a theme that pads active tabs
  differently.

### Router (`test/test_mouse_router.rb`)

- The full mode × zone × button table, including every `:ignored` case above.
- `:task_edit` ignores all clicks; wheel-over-panel still routes.
- Release events produce `:ignored`.

### App integration (`test/test_app.rb`)

- `handle_mouse("\e[<0;5;7M")` selects the row under the pointer; a second
  identical click opens the detail panel.
- Wheel over the panel scrolls the panel and does not move the selection;
  wheel over the list moves the selection and does not scroll the panel.
- A click on a section header row changes nothing.
- A click on a tab switches the view and preserves the selected id where it
  survives.
- With mouse disabled by config, `run` prints no tracking sequence and
  `handle_mouse` is never reached.
- A mouse event arriving before the first paint is ignored without raising.

### Manual terminal proof

Betamax drives keystrokes, not a pointer, so the mouse gestures get a manual
checklist instead of a recorded proof — capture the checklist result in
`docs/proofs/tui-mouse.md`:

- iTerm2, Terminal.app, kitty, ghostty, and tmux: click, wheel, and wheel over
  each scrollable region.
- Text selection still works with the terminal's bypass modifier; `mouse = off`
  restores unmodified selection.
- Quit, `ctrl-c`, and a crash all leave the terminal with tracking off (check
  with `cat -v` afterwards — no `\e[<` reports on click).
- Resize mid-scroll, and click immediately after a resize, hit the intended
  target.
- A narrow terminal where the panel is suppressed routes wheel events to the
  list rather than a zero-width panel.

## Review gates

1. `Mouse`, `HitMap`, and `MouseRouter` contain no reference to tasks,
   contexts, shortcuts, `App`, or `Store` — grep-verifiable.
2. No new mutation path: every intent lands on a handler that a documented key
   already reaches.
3. `Frame` and `HitMap` derive every rectangle from `ScreenLayout`, with the
   cross-check test present.
4. Terminal state is restored on every exit path, verified for normal quit,
   `ctrl-c`, and an exception inside the loop.
5. `ruby test/all.rb` and `git diff --check` clean.

## Explicit non-goals

- Motion/drag tracking (`\e[?1002h`, `\e[?1003h`) and anything built on it:
  drag to reorder a subtree, drag the panel divider, hover highlights or
  tooltips.
- A detached list scroll offset (wheel-over-list moves the cursor for now).
- Mouse input inside the task editor / `TermForm` fields.
- Double-click, click-and-hold repeat, and gesture chords.
- Right-click context menus.
- Legacy X10 mouse encoding and urxvt (`1015`) encoding.
- Clicking to place the text cursor in the agent prompt or the `/` filter.
- Focus-in/out reporting (`\e[?1004h`), which is unrelated but often bundled
  with mouse work.
