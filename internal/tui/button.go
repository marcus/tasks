package tui

// Buttons are the modal's primary interactive surfaces, not painted text with
// a column range bolted on. A button knows its variant — which carries the
// whole look as one theme slot — and how wide it paints, which is what a hit
// map needs to answer a click by cell rather than by row.
//
// The variant is a single slot rather than separate fg/bg slots because that
// is the shape the theme table already uses for compound looks
// (form_group_label is "bold black on-cyan"), and because a host projecting a
// palette (the Sidecar embed) then has exactly one key per surface to map.

// ButtonVariant selects a button's look.
type ButtonVariant int

const (
	// ButtonPrimary is the one affirmative action: submit. Filled.
	ButtonPrimary ButtonVariant = iota
	// ButtonDanger outlines a destructive affordance: release, undelegate.
	ButtonDanger
	// ButtonDangerArmed is the destructive affordance after its first press:
	// filled, loud, asking for the second press that acts.
	ButtonDangerArmed
	// ButtonMuted is cancel and other exits. Quiet on purpose.
	ButtonMuted
)

// slot returns the theme slot carrying the variant's full look.
func (v ButtonVariant) slot() string {
	switch v {
	case ButtonPrimary:
		return "button_primary"
	case ButtonDanger:
		return "button_danger"
	case ButtonDangerArmed:
		return "button_danger_armed"
	default:
		return "button_muted"
	}
}

// ModalButton is one clickable affordance in a modal's action row.
type ModalButton struct {
	ID    string
	Label string
	// KeyLabel names the key that also invokes the button, so the mouse path
	// advertises the keyboard path instead of hiding it. Empty for none.
	KeyLabel string
	Variant  ButtonVariant
}

// plain is the button's unpainted text. The padding IS the button: a filled
// variant paints its background across these cells.
func (b ModalButton) plain() string {
	if b.KeyLabel == "" {
		return "  " + b.Label + "  "
	}
	return " " + b.KeyLabel + " " + b.Label + " "
}

// Width is the display width of the button's text in cells.
func (b ModalButton) Width(styler Styler) int {
	return styler.Width(b.plain())
}

// PaintModalButton renders one button in its variant's slot.
func PaintModalButton(styler Styler, b ModalButton) string {
	return styler.Paint(b.Variant.slot(), b.plain())
}

// PaintButtonRow lays buttons out left to right with one-cell gaps and returns
// the line plus each button's [begin,end) column span, so a click resolves to
// an ID without re-derifying layout. Spans index into the RETURNED string
// directly: cell Begin is where PaintModalButton's text starts. A caller that
// splices the row into a bordered box adds its own border offset when it maps
// a pointer column to a span — this function never guesses where the line will
// sit.
func PaintButtonRow(styler Styler, buttons []ModalButton) (string, []ModalButtonSpan) {
	text := ""
	spans := make([]ModalButtonSpan, 0, len(buttons))
	column := 0
	for _, b := range buttons {
		painted := PaintModalButton(styler, b)
		width := b.Width(styler)
		if column > 0 {
			text += " "
			column++
		}
		text += painted
		spans = append(spans, ModalButtonSpan{Begin: column, End: column + width, ID: b.ID})
		column += width
	}
	return text, spans
}

// ModalButtonSpan is one painted button's column range within its row.
type ModalButtonSpan struct {
	Begin, End int
	ID         string
}
