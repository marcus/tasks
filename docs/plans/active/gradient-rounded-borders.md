# Plan: angled gradient borders with rounded corners

Status: implemented; pending visual + independent review

Date: 2026-07-23

## Implementation notes (what shipped)

- New `lib/tui/border.rb` + `test/test_border.rb` — gradient projection (with a
  2.0 vertical aspect correction), plain-sRGB stop interpolation, a 24-step
  quantized palette that coalesces adjacent equal-color cells into single SGR
  runs, rounded/square charsets, and truecolor gating (`Border.truecolor`).
- `Theme`: `:border` solid slot + a separate `:border_gradient` spec parsed via
  `Border.parse_gradient`, exposed through `Theme.gradient(:border)`; both are
  config-overridable (`color.border`, `color.border_gradient`). `mono`/`NO_COLOR`
  disable the sweep.
- `frame.rb` paints the whole chrome (outer ring + both divider rules) through
  one Painter; modals, the two palettes, and the form box all render via
  `Border.box`.
- Generator emits per-scheme `border`/`border_gradient` (blue→cyan accents);
  all 36 generated themes regenerated. Default theme sweeps `#5aa2f7 → #56d3c9
  @45`; the solid `:border` default is `none` so non-truecolor terminals keep
  today's uncolored look.
- Tests: border assertions made SGR-tolerant and updated to rounded corners; a
  suite-wide `Border.truecolor = false` keeps chrome deterministic, with an
  explicit truecolor-on integration test in `test_frame.rb`. Full suite green.
- Deferred as unnecessary: per-size memoization of painted border strings. The
  Painter rebuilds a 24-entry palette per repaint (negligible) and the paint
  path already gates on dirtiness; revisit only if a timing regression appears.

## Outcome

Every container in the TUI — the main window, modals, the form box, and the
context/action palettes — is drawn with a border whose color is an **angled
color gradient** (a smooth truecolor sweep from one corner toward another) and
whose outer corners are **rounded** (`╭ ╮ ╰ ╯` instead of `┌ ┐ └ ┘`). The whole
frame *chrome* participates: the outer ring **and** the header/footer divider
rules (`├─┤`) share one continuous gradient, so the frame reads as lit from a
single corner.

All box-drawing lives behind one reusable `Tui::Border` module, so any future
container gets the same look for free. Themes carry the gradient definition, and
config can override it. Terminals without truecolor, `NO_COLOR`, and the `mono`
theme degrade cleanly to a single solid border color (and, if desired, square
corners).

## Current state (what exists today)

- `lib/tui/frame.rb` hand-writes the glyphs: `"┌#{"─" * w}┐"`, `"│…│"`,
  `"├─┤"`, `"└─┘"`. **No color is applied to the border at all** — it renders in
  the terminal's default foreground. Corners are square. `overlay_modal!` builds
  a second, independent square box the same way.
- `lib/tui/form_renderer.rb`, `lib/tui/context_palette.rb`,
  `lib/tui/action_palette.rb` each hand-roll their *own* square box. Four
  independent copies of the same box code — the reason a shared module is worth
  it.
- `lib/tui/theme.rb` already resolves `#rrggbb` specs to truecolor SGR
  (`38;2;r;g;b`), has a clean slot system, named themes overlaying `DEFAULTS`,
  and per-slot config overrides. There is no `border` slot.
- `lib/tui/ansi.rb` has truecolor + width-aware helpers (`vpad`, `vtrunc`,
  `cell_slice`, `composite`).
- 38 themes: 2 builtin (`default`, `mono`) + 36 in `generated_themes.rb`,
  produced by `scripts/generate-tui-themes` from the iTerm2 schemes repo.
- Painting: `App#paint` calls `Frame.build` every dirty frame and prints the
  joined rows. Rows are memoized upstream via a fingerprint; the border glyphs
  themselves never change between frames unless size/theme changes.

## Design

### 1. Gradient color model

A gradient spec = an **angle** (degrees) + an ordered list of **stops** (hex
colors), evaluated per border cell:

- Direction vector `d = (cos θ, sin θ)`. Terminal cells are ~2× taller than
  wide, so `y` is scaled by a configurable **aspect factor** (default `2.0`) so
  that "45°" looks like 45° on screen rather than skewed.
- For a cell at `(x, y)` compute the scalar projection `p = x·cosθ +
  (y·aspect)·sinθ`. Normalize against the projections of the four box corners:
  `t = (p − p_min) / (p_max − p_min)`, clamped to `[0, 1]`.
- `color = lerp(stops, t)`. Interpolate in a perceptual-ish space (sRGB→linear
  lerp→sRGB, or OkLab) so mid-tones don't go muddy; the exact space is an
  internal detail with a unit test pinning endpoints and midpoint.
- Emit `38;2;r;g;b`. `t=0` at one corner, `t=1` at the diagonally opposite
  corner selected by the angle.

Two stops is the common case; the list form allows 3+ later with no API change.

### 2. Rounded corners

Outer corners map `┌→╭ ┐→╮ └→╰ ┘→╮`... i.e. `╭ ╮ ╰ ╯`. Edges (`│ ─`) and the
divider tees (`├ ┤`) are unchanged. A `corners:` option (`:round` default,
`:square` fallback) lets a theme or capability check opt out for fonts that
render the rounded glyphs poorly.

### 3. The reusable `Tui::Border` module (new — `lib/tui/border.rb`)

The single owner of box-drawing + gradient + corner charset. Surface:

- `Border::Painter.new(width:, height:, gradient:, corners:)` — precomputes the
  per-cell colorizer for a box of that size. Exposes `#at(x, y, glyph)` →
  glyph wrapped in the truecolor SGR for that cell (plus reset). The frame maps
  each *border glyph's* coordinate through the painter; interior content is left
  untouched.
- `Border.box(width:, height:, title:, lines:, gradient:, corners:, title_slot:)`
  — renders a complete box (rounded corners, gradient edges, optional title
  strip) and returns styled row strings. Modals, the form box, and both palettes
  call this instead of hand-rolling glyphs.
- Degradation: when `gradient` is nil / truecolor unavailable, the painter falls
  back to a single solid SGR (the `border` slot) and, if configured, square
  corners — same call sites, no branching at the call site.

**Coalescing + quantization (performance):** adjacent cells whose quantized
color is equal share one SGR run instead of one per cell. A `steps:` bound
(default e.g. 24) caps the number of distinct colors along the ring, so a
200-cell perimeter emits a handful of SGR runs, not 200.

**Caching:** the painted border strings depend only on
`(width, height, gradient, corners, steps)`. `Border` memoizes the top row,
bottom row, each rule row, and the colored left/right edge pair per body-row
index, keyed by that tuple. Frame wraps changing body content between two cached
colored edges; full-border rows are fully cached. Cache invalidates on
resize/theme change (both already re-enter `Frame.build` / `Theme.configure!`).

### 4. Theme integration (`lib/tui/theme.rb`)

Two new slots plus a gradient accessor:

- `border` — an ordinary slot spec (`"gray"`, `"#565f89"`, `none`): the **solid
  fallback** used when gradient is off/unsupported. Flows through the existing
  `parse`/`paint` pipeline unchanged.
- `border_gradient` — a **new spec kind**, stored and parsed separately from the
  SGR pipeline (a gradient is not a single SGR). Grammar:
  `"#7aa2f7 #bb9af7 @60"` = two+ hex stops then `@<angle>`. Add
  `Theme.gradient(:border)` → `{ stops: [...], angle: 60 }` or `nil`. Config
  overrides work like any slot: `color.border = #565f89`,
  `color.border_gradient = #7aa2f7 #bb9af7 @45`. Invalid specs drop to `nil`
  (fall back to solid) — config can never crash the TUI, matching the existing
  contract.
- `mono` / `NO_COLOR`: `border: "dim"`, no gradient.
- Truecolor capability: detect via `COLORTERM` (`truecolor`/`24bit`); when
  absent, ignore `border_gradient` and use solid `border`. Centralized in
  `Border` so every call site degrades identically.

### 5. Theme updates ("update themes to use it")

- `DEFAULTS`: add a tasteful default — e.g. `border: "gray"`,
  `border_gradient: "#5aa2f7 #56d3c9 @45"` (blue→cyan sweep).
- `scripts/generate-tui-themes`: extend `convert_scheme` to emit `border`
  (a muted stop, e.g. `bright_black`) and `border_gradient` derived from two
  scheme accents (e.g. `bright_blue → bright_cyan @45`, or `blue → purple`).
  Regenerate all 36 themes by rerunning the generator against the iTerm2 clone
  (avoids hand-editing 36 entries). Note in the plan: regeneration needs the
  `~/code/iTerm2-Color-Schemes` clone the script already expects.

### 6. Wiring the call sites

- `frame.rb`: build the outer ring + both `├─┤` rules through `Border::Painter`
  at their real `(x, y)`; wrap each body row's content between the cached colored
  `│` edges. `overlay_modal!` → `Border.box(...)`.
- `form_renderer.rb`, `context_palette.rb`, `action_palette.rb` → replace their
  hand-rolled boxes with `Border.box(...)`, passing their existing title/lines.

### 7. Edge cases

- Tiny frames (`App::MIN_WIDTH` 8 / `MIN_HEIGHT` 6) and the compact-modal branch:
  Border must handle small `w`/`h`; keep the plain/square fallback there.
- Border glyphs are width-1, so `cell_slice`/`overlay!`/wide-grapheme handling is
  unaffected; modal boxes overlaid via `overlay!` keep their gradient SGR because
  `cell_slice` already preserves SGR.
- Selection compositing is body-only and runs before edges are attached — no
  interaction with border color.

## Phased implementation

1. **`Border` module + gradient math** — `lib/tui/border.rb`: projection,
   lerp, quantize/coalesce, rounded charset, capability degradation, caching.
   Unit tests (`test/test_border.rb`): endpoint/midpoint colors, angle
   direction, corner glyphs, solid fallback, `steps` coalescing.
2. **Theme slots + gradient parsing** — `border` / `border_gradient` slots,
   `Theme.gradient`, config overrides, `mono`/`NO_COLOR` behavior. Extend
   `test/test_theme.rb`.
3. **Main frame** — route `frame.rb` outer ring + rules through `Border`; update
   frame tests to assert rounded corners + gradient SGR presence and that plain
   (`NO_COLOR`) output stays intact.
4. **Modals + palettes + form box** — convert `overlay_modal!`,
   `context_palette.rb`, `action_palette.rb`, `form_renderer.rb` to `Border.box`.
5. **Theme generation** — extend `scripts/generate-tui-themes`, regenerate
   `generated_themes.rb`, update `DEFAULTS`/`mono`.
6. **Docs** — document the `color.border` / `color.border_gradient` config keys
   (cli-spec / config reference); a short ADR is optional given the module's
   reach.

## Files

- New: `lib/tui/border.rb`, `test/test_border.rb`.
- Edit: `lib/tui/theme.rb`, `lib/tui/frame.rb`, `lib/tui/form_renderer.rb`,
  `lib/tui/context_palette.rb`, `lib/tui/action_palette.rb`,
  `scripts/generate-tui-themes`, `lib/tui/generated_themes.rb` (regenerated),
  `test/test_theme.rb`, frame/palette tests, config docs.

## Open questions / defaults chosen

- Aspect factor default `2.0`, `steps` default ~24, perceptual lerp — all
  internal, tunable, pinned by tests.
- Default gradient colors are subjective; the blue→cyan default above is a
  starting point to iterate on visually once wired.
