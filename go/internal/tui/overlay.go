package tui

import (
	"strings"

	"tasks-go/internal/tui/term/ansi"
)

// Overlays are painted as whole lines composited over the finished frame rather
// than by cursor addressing.
//
// That is the same rebuild-not-port decision the rest of the rendering made,
// and it buys the property the tests depend on: a frame is a string, so an
// assertion about what the user sees is an assertion about that string. Ruby
// moves the cursor and writes boxes in place, which is only observable by
// scraping a terminal.

// OverlayBox is a popup positioned in the frame.
type OverlayBox struct {
	Lines []string
	Row   int
	Col   int
	// FocusedContentRow is the row inside Lines carrying the caret, or -1.
	FocusedContentRow int
}

// Overlay is the popup the current mode wants painted, or nil.
func (m *Model) Overlay() *OverlayBox {
	layout := m.layout()
	switch m.mode {
	case ModeModal, ModeModalFilter:
		return m.modalOverlay(layout)
	case ModeForm:
		return m.formOverlay(layout)
	case ModePalette:
		return m.paletteOverlay(layout, m.actionPalette.Picker())
	case ModeContextPalette:
		return m.paletteOverlay(layout, m.contextPalette.Picker())
	}
	return nil
}

func (m *Model) modalOverlay(layout ScreenLayout) *OverlayBox {
	if m.modal == nil {
		return nil
	}
	filterLine := ""
	if m.mode == ModeModalFilter {
		filterLine = m.styler.Paint("accent", "/ "+m.ModalFilterInput()) +
			m.styler.Paint("muted", "  enter keeps · esc clears")
	}
	view := m.modal.View(m.styler, layout.BodyHeight, filterLine)
	width := min(view.Width, max(layout.Width-4, 8))
	inner := max(width-4, 1)
	lines := []string{"┌" + padBorder(m.styler.Truncate(" "+view.Title+" ", inner), width-2) + "┐"}
	for _, line := range view.Lines {
		lines = append(lines, "│ "+padTo(m.styler, m.styler.Truncate(line, inner), inner)+" │")
	}
	lines = append(lines, "└"+strings.Repeat("─", max(width-2, 0))+"┘")
	return m.center(layout, lines, -1)
}

func (m *Model) formOverlay(layout ScreenLayout) *OverlayBox {
	if m.form == nil {
		return nil
	}
	render := m.form.Popup(m.styler, max(layout.Width-4, 8), max(layout.BodyHeight, 1))
	return m.center(layout, render.Lines, render.FocusedContentRow)
}

func (m *Model) paletteOverlay(layout ScreenLayout, picker *ChoicePicker) *OverlayBox {
	if picker == nil {
		return nil
	}
	lines := picker.Popup(m.styler, max(layout.Width-4, 8), max(layout.BodyHeight, 1), m.inlineInput)
	return m.center(layout, lines, -1)
}

// inlineInput draws a caret inside a one-line input, since an overlay composed
// as text has no terminal cursor to move.
func (m *Model) inlineInput(text string, cursor int) string {
	clusters := ansi.Clusters(text)
	position := clamp(cursor, 0, len(clusters))
	before := strings.Join(clusters[:position], "")
	at, after := " ", ""
	if position < len(clusters) {
		at = clusters[position]
		after = strings.Join(clusters[position+1:], "")
	}
	return before + m.styler.Paint("form_cursor", at) + after
}

// center places a box in the body area, clamped so it never hangs off an edge.
func (m *Model) center(layout ScreenLayout, lines []string, focusedRow int) *OverlayBox {
	if len(lines) == 0 {
		return nil
	}
	width := 0
	for _, line := range lines {
		width = max(width, m.styler.Width(line))
	}
	bodyBegin, bodyEnd := layout.BodyRows()
	row := bodyBegin + max((bodyEnd-bodyBegin-len(lines))/2, 0)
	col := max((layout.Width-width)/2, 1)
	return &OverlayBox{Lines: lines, Row: row, Col: col, FocusedContentRow: focusedRow}
}

// composite paints a box over already-rendered frame lines.
//
// It splices in CELLS, not bytes: the frame lines carry escape sequences, and
// slicing them by byte offset would cut a color code in half and spill styling
// across the rest of the screen.
func (m *Model) composite(frame []string, box *OverlayBox) []string {
	if box == nil {
		return frame
	}
	out := append([]string{}, frame...)
	for offset, line := range box.Lines {
		row := box.Row + offset
		if row < 0 || row >= len(out) {
			continue
		}
		width := m.styler.Width(line)
		left := ansi.CellSlice(out[row], 0, box.Col)
		right := ansi.CellSliceToEnd(out[row], box.Col+width)
		out[row] = left + line + right
	}
	return out
}
