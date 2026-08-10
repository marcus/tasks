package tui

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/tui/term/ansi"
)

// Rendering is a REBUILD, not a port.
//
// Ruby's Frame paints a bordered box by emitting escape sequences and cursor
// moves itself, and its correctness depends on measured width agreeing with
// painted width in a dozen places. Here the frame is composed as plain lines
// and each line is padded and truncated through the styler's own width
// function, so the one thing that has to agree — measurement — has exactly one
// implementation. The plan explicitly does not ask for pixel identity, and this
// is where that permission is spent.

// Layout is the current frame's geometry.
func (m *Model) layout() ScreenLayout {
	return m.layoutForEditing(m.taskEditor != nil)
}

// layoutForEditing lets the entry path test the exact geometry the editor
// would receive before constructing or activating a session.
func (m *Model) layoutForEditing(editing bool) ScreenLayout {
	footer := m.Footer()
	if editing {
		footer = m.footerForMode(ModeTaskEdit)
	}
	return NewScreenLayout(m.styler, LayoutRequest{
		Width:        m.width,
		Height:       m.height,
		Footer:       footer,
		Selected:     m.selected,
		HasSelection: len(m.rows) > 0,
		// An open editor occupies the panel column even when no detail panel
		// is open, so the geometry the hit map and the renderer both read
		// agrees about where the list ends.
		Panel:       m.panel != nil || editing,
		PanelMode:   m.panelMode,
		PanelOffset: m.panelOffset,
		Editing:     editing,
	})
}

// Layout exposes the geometry for tests and for the hit map the rendering
// packet builds.
func (m *Model) Layout() ScreenLayout { return m.layout() }

// Render composes one frame at the model's current size.
func (m *Model) Render() string {
	layout := m.layout()
	lines := []string{
		m.border(layout.Width, "┌", "┐"),
		m.boxed(layout.Width, m.Header(layout.Width-2)),
		m.rule(layout.Width),
	}
	lines = append(lines, m.bodyLines(layout)...)
	lines = append(lines, m.rule(layout.Width))
	for _, footer := range layout.Footer {
		lines = append(lines, m.boxed(layout.Width, footer))
	}
	lines = append(lines, m.border(layout.Width, "└", "┘"))
	return strings.Join(m.composite(lines, m.Overlay()), "\n")
}

// bodyLines paints the list, and the panel beside it when one is open.
func (m *Model) bodyLines(layout ScreenLayout) []string {
	visible := layout.VisibleRows(m.rows)
	var panelView PanelView
	if m.panel != nil {
		panelView = m.panel.View(m.styler, layout.BodyHeight, layout.PanelContentWidth)
	}
	panelLines := m.panelColumn(layout, panelView)
	hasPanel := m.panel != nil
	if m.taskEditor != nil {
		panelLines = m.editorColumn(layout)
		hasPanel = true
	}

	out := make([]string, 0, layout.BodyHeight)
	for row := 0; row < layout.BodyHeight; row++ {
		text, gutter := "", " "
		if row < len(visible) {
			text = visible[row].Text
			if row+layout.ViewportOffset == layout.Selected && layout.HasSelection {
				// A gutter glyph AND the selection slot. Ruby marks the cursor
				// with reverse video alone; that survives NO_COLOR but not a
				// terminal or theme that renders the slot as nothing, and a
				// cursor you cannot find is the one bug in a task list that
				// costs you the wrong completed task. The glyph is one cell of
				// insurance that no styler can take away.
				gutter = "›"
				text = m.styler.Paint("selection",
					m.styler.Truncate(text, max(layout.ListWidth-1, 0)))
			}
		}
		line := gutter + m.fit(text, max(layout.ListWidth-1, 0))
		if hasPanel {
			panelText := ""
			if row < len(panelLines) {
				panelText = panelLines[row]
			}
			line += " │ " + m.fit(panelText, max(layout.PanelContentWidth, 0))
		}
		out = append(out, m.boxed(layout.Width, " "+line))
	}
	return out
}

// panelColumn is the panel's title, divider, and content — the two chrome rows
// RightPanel budgets for.
func (m *Model) panelColumn(layout ScreenLayout, view PanelView) []string {
	if m.panel == nil {
		return nil
	}
	lines := []string{
		m.styler.Paint("detail_label", view.Title),
		strings.Repeat("─", max(layout.PanelContentWidth, 0)),
	}
	return append(lines, view.Lines...)
}

// editorColumn paints the open task editor into the panel column.
//
// The editor lives in the panel rather than in a centered popup on purpose: it
// is a long-lived surface, and the list beside it has to stay readable while a
// field is being edited — that is what makes "save on blur, keep working" a
// usable workflow rather than a modal interruption.
func (m *Model) editorColumn(layout ScreenLayout) []string {
	editor := m.taskEditor
	title := "edit task"
	hint := "tab saves and moves · ctrl-s saves · ctrl-o finishes · esc discards a field"
	message := m.editorMessage
	if editor.Missing() {
		message = "Task no longer exists — y copies the field, esc discards"
	}
	if confirmation := editor.PendingConfirmation(); confirmation != nil {
		message = confirmation.Message + " (y / n)"
	}
	if conflict := editor.Conflict(); conflict != nil {
		message = "“" + normalizeEditField(conflict.Field) + "” changed externally — reload, revert, or keep for copy"
	}
	if pending := editor.PendingRevert(); pending != "" {
		message = "Press Escape again to discard " + normalizeEditField(pending)
	}
	render := RenderForm(m.styler, FormRenderRequest{
		Model: editor.RenderModel(), Width: max(layout.PanelContentWidth, 0),
		Height: max(layout.BodyHeight, 0), Title: title, Hint: hint, Error: message,
	})
	return render.Lines
}

// headerCount is the right-hand side of the header: the open count, the
// unavailable-shown note, and the help hint.
func (m *Model) headerCount() string {
	styler := m.styler
	deferredNote := ""
	if m.showDeferred {
		deferredNote = styler.Paint("warning", "unavailable shown") + styler.Paint("muted", " · ")
	}
	return styler.Paint("muted", fmt.Sprintf("%d open · ", m.OpenTaskCount())) + deferredNote +
		styler.Paint("muted", "? help")
}

// Header is the tab strip plus the counts.
func (m *Model) Header(width int) string {
	styler := m.styler
	count := m.headerCount()
	tabWidth := max(width-styler.Width(count)-3, 1)
	tabs := m.TabStrip(tabWidth)
	gap := max(width-styler.Width(tabs)-styler.Width(count)-2, 1)
	return " " + tabs + strings.Repeat(" ", gap) + count + " "
}

// TabStrip renders the tab cells, degrading labels when they do not fit.
//
// Ruby's version drops tabs one at a time from the far end and re-measures at
// each of five degradation steps. This one degrades the LABELS in three steps
// (full, compact, minimum) and keeps every tab, because a tab that disappears
// from the strip is a view the user cannot see exists, and the number keys that
// jump to it keep working regardless. That is a genuine user-visible
// difference; it is recorded in the archived migration compatibility decisions.
func (m *Model) TabStrip(width int) string {
	variant := m.tabVariant(width)
	cells := make([]string, 0, len(Tabs))
	for _, tab := range Tabs {
		cells = append(cells, m.tabCell(tab, variant))
	}
	return strings.Join(cells, " ")
}

// tabVariant is which label size fits the given budget. Rendering and mouse hit
// testing both call it, so a click can never land on a cell that was measured
// at a different label size than the one painted.
func (m *Model) tabVariant(budget int) int {
	for variant := range 3 {
		total := 0
		for index, tab := range Tabs {
			if index > 0 {
				total++
			}
			total += m.styler.Width(m.tabCell(tab, variant))
		}
		if total <= budget {
			return variant
		}
	}
	return 2
}

// tabCell paints one tab. Under suppressViewKeyHints the numeric prefix is
// dropped from every label size — the HOST owns the number row — while the tab
// name, the Inbox badge, and the active-tab highlight are untouched, and the
// number keys keep jumping views.
func (m *Model) tabCell(tab Tab, variant int) string {
	label, compact, minimum := tab.Label, tab.Compact, tab.Minimum
	if m.suppressViewKeyHints {
		label, compact, minimum = tab.PlainLabel, tab.PlainCompact, tab.PlainMinimum
	}
	switch variant {
	case 1:
		label = compact
	case 2:
		label = minimum
	}
	if tab.Key == ViewInbox && m.read != nil {
		counts := m.intakeCounts(m.filteredItems())
		if counts.Approvals > 0 {
			label += fmt.Sprintf(" %d!", counts.Approvals)
		} else if counts.Inbox > 0 {
			label += fmt.Sprintf(" %d", counts.Inbox)
		}
	}
	if tab.Key == m.view {
		return m.styler.Paint("tab_active", label)
	}
	return m.styler.Paint("tab", label)
}

// Footer is the status rows under the list, in Ruby's order.
//
// The order is the contract, not an accident. A running agent's transcript
// takes the top because it is the only thing on screen that is still changing;
// the prompt takes the bottom because it is where the caret is. And the prompt
// is SKIPPED in the modes that render their own input in an overlay, so a short
// terminal never shows two carets.
//
// suppressFooter drops the whole stack; suppressKeyHints drops only the last
// row of it. See footerForMode.
func (m *Model) Footer() []string {
	if m.suppressFooter {
		return nil
	}
	return m.footerForMode(m.mode)
}

func (m *Model) footerForMode(mode Mode) []string {
	lines := []string{}
	lines = append(lines, m.agentFooter()...)
	if m.readErr != nil {
		lines = append(lines, m.styler.Paint("error", " ⚠ cannot read the task store: "+m.readErr.Error()))
	}
	if message := m.flash; message != "" {
		lines = append(lines, " "+message)
	}
	if mode == ModeFilter {
		lines = append(lines, m.styler.Paint("accent", " /"+m.filterEditor().Text())+
			m.styler.Paint("muted", "  enter applies · esc cancels"))
	} else if m.filter != "" {
		lines = append(lines, m.styler.Paint("muted", fmt.Sprintf(" filter /%s · esc clears", m.filter)))
	}
	if len(m.contextFilters) > 0 && mode != ModeContextPalette {
		lines = append(lines, m.styler.Paint("context", " "+strings.Join(m.contextFilters, " ")))
	}
	switch mode {
	case ModeFilter, ModeForm, ModePalette, ModeContextPalette, ModeTaskEdit:
	default:
		lines = append(lines, m.PromptLines(m.width-2)...)
	}
	// The key hint is the one footer element a host can plausibly re-paint in its
	// own chrome, so it is the one element SuppressKeyHints removes. Everything
	// above it — transcript, banner, flash, filters, prompt — is Tasks state the
	// host cannot render on Tasks' behalf, and it stays.
	if mode != ModeTaskEdit && !m.suppressKeyHints {
		lines = append(lines, m.styler.Paint("muted", m.keyHint()))
	}
	return lines
}

// spinner is the running-request tick. It is the only animated thing in the
// frame, and it exists so a long request cannot be mistaken for a hung one.
var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// agentFooter is the running request's live transcript, or the last result.
func (m *Model) agentFooter() []string {
	if m.queue == nil {
		return nil
	}
	if active, running := m.queue.ActiveRequest(); running {
		queued := ""
		if pending := m.pendingCount(); pending > 0 {
			queued = fmt.Sprintf(" · %d queued", pending)
		}
		lines := []string{m.styler.Paint("muted", fmt.Sprintf(
			" %s #%d %s is working%s · A activity · esc cancels",
			spinner[m.spinnerTick()%len(spinner)], active.ID, active.Entry.UILabel(), queued))}
		// The last three transcript lines only: a streaming chunk can end
		// mid-character, and the footer is a status row rather than a pager.
		transcript := strings.Split(ansi.Strip(ansi.Normalize(active.Output)), "\n")
		for _, line := range transcript[max(len(transcript)-3, 0):] {
			lines = append(lines, m.styler.Paint("muted", "   "+line))
		}
		return lines
	}
	if !m.respOpen || len(m.resp) == 0 {
		return nil
	}
	lines := []string{m.styler.Paint("muted", fmt.Sprintf(
		" result #%d · A opens all agent activity", m.respRequestID))}
	visible := m.ResponseLines()
	for _, line := range visible {
		lines = append(lines, "   "+line)
	}
	hint := "esc dismiss"
	if len(m.resp) > respMax {
		hint = fmt.Sprintf("%d/%d · pgup/pgdn scrolls · esc dismiss",
			m.respScroll+len(visible), len(m.resp))
	}
	return append(lines, m.styler.Paint("muted", "   ── "+hint+" ──"))
}

// spinnerTick advances with the clock rather than with a frame counter, so the
// animation is the same on two runs over the same pinned time.
func (m *Model) spinnerTick() int {
	return int(m.now().UnixNano() / int64(TickInterval))
}

// keyHint degrades with the terminal rather than being cut mid-word. A hint
// truncated to "enter d" teaches nothing; a shorter hint that ends on a word
// still does.
func (m *Model) keyHint() string {
	candidates := []string{
		" j/k move · 1-6 views · h/l fold · enter details · v unavailable · / search · q quit",
		" j/k · 1-6 views · h/l fold · enter details · / search · q quit",
		" j/k · 1-6 · h/l · enter · / · q",
		" ?",
	}
	// A host that owns quit advertises it in its own chrome. Tasks still acts
	// on the key — it latches a request — but it stops claiming the affordance.
	if m.suppressQuit {
		candidates = []string{
			" j/k move · 1-6 views · h/l fold · enter details · v unavailable · / search",
			" j/k · 1-6 views · h/l fold · enter details · / search",
			" j/k · 1-6 · h/l · enter · /",
			" ?",
		}
	}
	for _, hint := range candidates {
		if m.styler.Width(hint) <= m.width-2 {
			return hint
		}
	}
	return ""
}

// -- line composition -------------------------------------------------------

func (m *Model) fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = m.styler.Truncate(text, width)
	if pad := width - m.styler.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return text
}

func (m *Model) boxed(width int, text string) string {
	return "│" + m.fit(text, max(width-2, 0)) + "│"
}

func (m *Model) border(width int, left, right string) string {
	return left + strings.Repeat("─", max(width-2, 0)) + right
}

func (m *Model) rule(width int) string {
	return "├" + strings.Repeat("─", max(width-2, 0)) + "┤"
}
