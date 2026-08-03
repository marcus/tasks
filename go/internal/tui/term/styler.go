// Package term is the seam between the TUI shell and the terminal primitives
// under term/. It provides the one type the shell needs: a Styler that paints
// semantic slots and measures, truncates and wraps text in real terminal cells.
//
// Everything it does is delegated — theme resolution to term/theme, width and
// wrapping to term/ansi and term/charwidth. The value of this file is that the
// shell can depend on four methods instead of four packages.
package term

import (
	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/term/theme"
)

// slotAliases maps the shell's slot vocabulary onto the theme's where the two
// differ. The shell paints an inactive tab as "tab"; the theme (following Ruby)
// calls it "tab_inactive".
var slotAliases = map[string]theme.Slot{
	"tab": "tab_inactive",
}

// Styler paints semantic slots and measures text the way the terminal will draw
// it. It satisfies the shell's Styler interface structurally, so neither
// package needs to import the other's.
type Styler struct {
	theme *theme.Theme
}

// NewStyler resolves a named theme plus per-slot config overrides. An unknown
// theme name, an unknown slot, and an invalid spec all degrade rather than
// fail. Pass the theme name the configuration layer resolved — which is where
// the NO_COLOR convention selects "mono".
func NewStyler(themeName string, overrides map[string]string) *Styler {
	return &Styler{theme: theme.Configure(themeName, overrides)}
}

// NewStylerFromTheme wraps an already-resolved theme.
func NewStylerFromTheme(t *theme.Theme) *Styler {
	if t == nil {
		t = theme.Default()
	}
	return &Styler{theme: t}
}

// Theme exposes the resolved theme for callers that need slot SGRs directly —
// the frame's selection compositing and the border gradient, for instance.
func (s *Styler) Theme() *theme.Theme { return s.theme }

// Paint renders text in the named slot. An unknown slot, or one resolving to
// "none", returns the text unpainted.
func (s *Styler) Paint(slot, text string) string {
	return s.theme.Paint(resolveSlot(slot), text)
}

// Width is the display width of text in terminal cells: escape sequences count
// zero and a wide character counts two.
func (s *Styler) Width(text string) int { return ansi.VisLen(text) }

// Truncate cuts text to at most width cells, preserving styling and never
// splitting a wide character across the boundary. Truncated text is marked with
// an ellipsis.
func (s *Styler) Truncate(text string, width int) string { return ansi.VTrunc(text, width) }

// Wrap folds text to lines of at most width cells, preserving styling and
// grapheme clusters.
func (s *Styler) Wrap(text string, width int) []string { return ansi.Wrap(text, width) }

// SGR is the raw opening sequence for a slot, or "" when the slot is unset.
func (s *Styler) SGR(slot string) string { return s.theme.SGR(resolveSlot(slot)) }

// Pad extends text to exactly width cells (a no-op if it is already wider).
func (s *Styler) Pad(text string, width int) string { return ansi.VPad(text, width) }

// Slice returns the visible cell window [start, start+width) without splitting
// a grapheme cluster, keeping content after the window in its original column.
func (s *Styler) Slice(text string, start, width int) string {
	return ansi.CellSlice(text, start, width)
}

func resolveSlot(slot string) theme.Slot {
	if alias, ok := slotAliases[slot]; ok {
		return alias
	}
	return slot
}
