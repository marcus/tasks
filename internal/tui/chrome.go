package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/tui/term/border"
)

// Modal chrome: the border treatment and the backdrop dimming that make a
// popup read as one object rather than as text drawn over text.
//
// The neutral variant paints borders unpainted — exactly what FieldModal does
// today — so adopting the chrome package changes nothing for a modal until it
// asks for a variant. Variants exist because a border is information: accent
// says "ordinary dialog", warning says "a destructive action is armed", danger
// says "this whole surface is about destruction". Danger deliberately shares
// the existing error slot rather than adding a fourth border color to restyle:
// a danger modal IS an error-shaped situation, and it inherits whatever the
// user already told color.error means.

// BoxVariant selects a modal's border look.
type BoxVariant int

const (
	BoxNeutral BoxVariant = iota
	// BoxAccent frames an ordinary dialog.
	BoxAccent
	// BoxWarning frames an armed destructive confirmation.
	BoxWarning
	// BoxDanger frames a fully destructive surface.
	BoxDanger
)

// BorderSlot maps a variant to its theme slot. Neutral returns "" — no paint —
// so existing callers keep their current look until they opt in.
func (v BoxVariant) BorderSlot() string {
	switch v {
	case BoxAccent:
		return "modal_border_accent"
	case BoxWarning:
		return "modal_border_warning"
	case BoxDanger:
		return "error"
	default:
		return ""
	}
}

// PaintChrome renders one border piece in its variant's slot. A neutral (or
// unknown) variant returns the piece untouched, which is the same string the
// un-chromed renderers emit today. Glyphs come from border.Round so the
// corners match every other rounded surface in the TUI.
func PaintChrome(styler Styler, variant BoxVariant, piece string) string {
	slot := variant.BorderSlot()
	if slot == "" {
		return piece
	}
	return styler.Paint(slot, piece)
}

// ChromeHorizontal runs horizontal border of width n, painted for variant.
func ChromeHorizontal(styler Styler, variant BoxVariant, n int) string {
	if n <= 0 {
		return ""
	}
	return PaintChrome(styler, variant, strings.Repeat(border.Round.H, n))
}

// PaintBorderSlot paints one border piece in a NAMED slot — the per-field box
// treatment (field_border / field_border_focused) and any other border that is
// chrome but not the modal frame. An empty slot passes the piece through
// untouched, the same passthrough a neutral BoxVariant gets.
func PaintBorderSlot(styler Styler, slot, piece string) string {
	if slot == "" || piece == "" {
		return piece
	}
	return styler.Paint(slot, piece)
}

// ApplyModalBackdrop wraps the frame cells OUTSIDE a modal box on the rows the
// box spans in the backdrop slot, returning both margins for the caller to
// splice around the box line:
//
//	left + boxLine + right
//
// The seam takes already-sliced strings because the frame carries escape
// sequences and every cut here must go through the cell-aware slicer — the
// same rule overlay.go's composite follows. mono deliberately covers this slot
// with "dim": attribute-only dimming is consistent with mono styling
// everything else, and keeps the float readable under NO_COLOR. A user or host
// who sets the slot to "none" gets the old flat look back.
func ApplyModalBackdrop(styler Styler, leftOfBox, rightOfBox string) (string, string) {
	if leftOfBox != "" {
		leftOfBox = styler.Composite("modal_backdrop", leftOfBox)
	}
	if rightOfBox != "" {
		rightOfBox = styler.Composite("modal_backdrop", rightOfBox)
	}
	return leftOfBox, rightOfBox
}
