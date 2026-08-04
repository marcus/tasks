package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Mouse handling in this packet is deliberately the COARSE half: click a row to
// select it, click a fold marker to fold it, click a tab to switch, and scroll
// the pane under the pointer. All four are answered from ScreenLayout's
// rectangles, which this packet owns.
//
// The fine half — the hit map, SGR sequence decoding, drag and press/release
// pairing — belongs to the rendering packet's `term/` and will replace this
// dispatch at integration. Until then this is a small, correct subset rather
// than a stub: enabling mouse reporting and then ignoring every click is the
// exact "silently wrong" failure this packet is not allowed to ship.

// HandleMouse routes one mouse event. It reports whether the event was
// consumed, so the integration owner can layer a hit map above it without
// double-handling.
func (m *Model) HandleMouse(event tea.MouseMsg) bool {
	if !m.mouseEnabled() {
		return false
	}
	// An open overlay owns the pointer. Without this, a click meant for a
	// palette row would fall through to the list underneath and move the
	// selection the palette was opened for — which is how a palette action runs
	// against the wrong task.
	if box := m.Overlay(); box != nil {
		return m.overlayMouse(box, event)
	}
	layout := m.layout()
	switch event.Type {
	case tea.MouseWheelUp:
		return m.wheel(layout, event, -1)
	case tea.MouseWheelDown:
		return m.wheel(layout, event, 1)
	case tea.MouseLeft:
		return m.click(layout, event)
	}
	return false
}

// mouseEnabled honours the `mouse` config setting, the same one the Ruby TUI
// reads. A user who turned the mouse off did so to get their terminal's own
// selection back, and the Go build must not quietly take it away again.
func (m *Model) mouseEnabled() bool { return m.paths.Mouse }

func (m *Model) wheel(layout ScreenLayout, event tea.MouseMsg, direction int) bool {
	if m.panel != nil && m.inPanel(layout, event.X) {
		m.panel.ScrollLine(direction*3, layout.BodyHeight)
		return true
	}
	m.move(direction * 3)
	return true
}

func (m *Model) click(layout ScreenLayout, event tea.MouseMsg) bool {
	if event.Y == layout.HeaderRow() {
		return m.clickTab(layout, event.X)
	}
	begin, end := layout.BodyRows()
	if event.Y < begin || event.Y >= end {
		return false
	}
	if m.panel != nil && m.inPanel(layout, event.X) {
		return false
	}
	index := layout.ViewportOffset + (event.Y - begin)
	if index < 0 || index >= len(m.rows) {
		return false
	}
	row := m.rows[index]
	if !row.Selectable() {
		return false
	}
	// The list reserves one gutter column for the cursor glyph, so a marker's
	// screen column is its row-relative span shifted by the list origin plus one.
	listBegin, _ := layout.ListCols()
	markerOrigin := listBegin + 1
	if row.HasMarker() && event.X >= markerOrigin+row.MarkerBegin &&
		event.X < markerOrigin+row.MarkerEnd {
		m.selectRow(index)
		if row.Item != nil && m.collapsed[row.Item.ID] {
			m.ExpandSelected()
		} else {
			m.CollapseSelected()
		}
		return true
	}
	m.selectRow(index)
	return true
}

func (m *Model) clickTab(layout ScreenLayout, column int) bool {
	// The strip starts one column in from the border, matching Header's leading
	// space. Cells are separated by a single space.
	cursor := 2
	variant := m.tabVariant(m.tabBudget(layout))
	for _, tab := range Tabs {
		width := m.styler.Width(m.tabCell(tab, variant))
		if column >= cursor && column < cursor+width {
			m.SwitchView(tab.Key)
			return true
		}
		cursor += width + 1
	}
	return false
}

// tabBudget is the width Header hands the tab strip. Hit testing has to
// reproduce it exactly, so it is derived here rather than guessed.
func (m *Model) tabBudget(layout ScreenLayout) int {
	width := layout.Width - 2
	return max(width-m.styler.Width(m.headerCount())-3, 1)
}

func (m *Model) inPanel(layout ScreenLayout, column int) bool {
	begin, end := layout.PanelCols()
	return begin < end && column >= begin-2 && column < end
}

// overlayMouse routes a click or a wheel tick that landed on an open overlay.
//
// The row offset is taken against the box's OWN top row, which is the same
// number the picker recorded when it last painted. Both sides agree because the
// layout the hit test reads is produced by the paint, not re-derived from the
// option list — a palette that re-derived it would act on the wrong row the
// moment the query narrowed the list between paint and click.
func (m *Model) overlayMouse(box *OverlayBox, event tea.MouseMsg) bool {
	inside := event.Y >= box.Row && event.Y < box.Row+len(box.Lines)
	switch event.Type {
	case tea.MouseWheelUp, tea.MouseWheelDown:
		direction := -1
		if event.Type == tea.MouseWheelDown {
			direction = 1
		}
		switch m.mode {
		case ModeModal, ModeModalFilter:
			m.modalMove(direction * 3)
		case ModePalette:
			m.resolvePaletteOutcome(m.actionPalette, m.actionPalette.Move(direction))
		case ModeContextPalette:
			m.applyContextOutcome(m.contextPalette.Move(direction))
		default:
			return false
		}
		return true
	case tea.MouseLeft:
		if !inside {
			return false
		}
		offset := event.Y - box.Row
		switch m.mode {
		case ModePalette:
			m.resolvePaletteOutcome(m.actionPalette, m.actionPalette.Hit(offset))
			return true
		case ModeContextPalette:
			m.applyContextOutcome(m.contextPalette.Hit(offset))
			return true
		}
		return true
	}
	return false
}

// applyContextOutcome is the shared tail of a context-palette key and click.
func (m *Model) applyContextOutcome(outcome ContextOutcome) {
	switch outcome.Kind {
	case PickerCancelled:
		m.CloseContextPalette()
	case PickerAccepted:
		if !outcome.Apply {
			return
		}
		m.ApplyContextFilter(outcome.Contexts)
		m.CloseContextPalette()
	}
}
