package tui

import tea "charm.land/bubbletea/v2"

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
//
// Bubble Tea v2 delivers mouse as an interface (MouseClickMsg, MouseWheelMsg,
// MouseReleaseMsg, MouseMotionMsg). Only click and wheel are acted on.

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
	switch e := event.(type) {
	case tea.MouseWheelMsg:
		mouse := e.Mouse()
		switch mouse.Button {
		case tea.MouseWheelUp:
			return m.wheel(layout, mouse, -1)
		case tea.MouseWheelDown:
			return m.wheel(layout, mouse, 1)
		}
	case tea.MouseClickMsg:
		mouse := e.Mouse()
		if mouse.Button == tea.MouseLeft {
			return m.click(layout, mouse)
		}
	}
	return false
}

// mouseEnabled honours the `mouse` config setting, the same one the Ruby TUI
// reads. A user who turned the mouse off did so to get their terminal's own
// selection back, and the Go build must not quietly take it away again.
func (m *Model) mouseEnabled() bool { return m.paths.Mouse }

func (m *Model) wheel(layout ScreenLayout, event tea.Mouse, direction int) bool {
	footerRole := m.footerRole(layout, event.Y)
	if footerRole == "response" {
		m.ScrollResponse(direction * 3)
		return true
	}
	if footerRole != "" {
		return true
	}
	if m.panel != nil && m.inPanel(layout, event.X) {
		m.panel.ScrollLine(direction*3, layout.BodyHeight)
		return true
	}
	m.blurPrompt()
	m.move(direction * 3)
	return true
}

func (m *Model) click(layout ScreenLayout, event tea.Mouse) bool {
	if m.footerRole(layout, event.Y) == "prompt" {
		m.FocusPrompt()
		return true
	}
	if event.Y == layout.HeaderRow() {
		m.blurPrompt()
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
		m.blurPrompt()
		m.selectRow(index)
		if row.Item != nil && m.collapsed[row.Item.ID] {
			m.ExpandSelected()
		} else {
			m.CollapseSelected()
		}
		return true
	}
	m.blurPrompt()
	m.selectRow(index)
	return true
}

func (m *Model) blurPrompt() {
	if m.mode == ModePrompt {
		m.mode = ModeList
	}
}

// footerRole classifies the fitted footer from the same ordered blocks Footer
// emitted. Content sniffing alone is unsafe: the key hint follows the prompt,
// and flash/filter/context chrome can sit between a response and that prompt.
func (m *Model) footerRole(layout ScreenLayout, row int) string {
	start, end := layout.FooterRows()
	if row < start || row >= end {
		return ""
	}
	index := row - start
	roles := m.footerRoles()
	if len(roles) > len(layout.Footer) {
		roles = roles[len(roles)-len(layout.Footer):]
	}
	if index < 0 || index >= len(roles) {
		return "chrome"
	}
	return roles[index]
}

func (m *Model) footerRoles() []string {
	// Mirror Footer exactly: a suppressed footer has no rows to classify, and a
	// suppressed key hint means the last row is the prompt, not chrome.
	if m.suppressFooter {
		return nil
	}
	roles := []string{}
	agentLines := m.agentFooter()
	agentRole := "chrome"
	if m.queue != nil && !m.queue.Active() && m.respOpen && len(m.resp) > 0 {
		agentRole = "response"
	}
	for range agentLines {
		roles = append(roles, agentRole)
	}
	if m.readErr != nil {
		roles = append(roles, "chrome")
	}
	if m.flash != "" {
		roles = append(roles, "chrome")
	}
	if m.mode == ModeFilter || (m.filter != "" && m.mode != ModeFilter) {
		roles = append(roles, "chrome")
	}
	if len(m.contextFilters) > 0 && m.mode != ModeContextPalette {
		roles = append(roles, "chrome")
	}
	switch m.mode {
	case ModeFilter, ModeForm, ModePalette, ModeContextPalette, ModeTaskEdit:
	default:
		for range m.PromptLines(m.width - 2) {
			roles = append(roles, "prompt")
		}
	}
	if m.mode != ModeTaskEdit && !m.suppressKeyHints {
		roles = append(roles, "chrome")
	}
	return roles
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
	mouse := event.Mouse()
	inside := mouse.Y >= box.Row && mouse.Y < box.Row+len(box.Lines)
	switch e := event.(type) {
	case tea.MouseWheelMsg:
		direction := -1
		if e.Button == tea.MouseWheelDown {
			direction = 1
		} else if e.Button != tea.MouseWheelUp {
			return false
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
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft || !inside {
			return false
		}
		offset := mouse.Y - box.Row
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
