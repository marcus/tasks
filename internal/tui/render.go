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
	m.reconcileRowWidth(layout)
	lines := []string{m.boxed(layout.Width, m.Header(layout.Width-2)), m.blank(layout.Width)}
	lines = append(lines, m.bodyLines(layout)...)
	lines = append(lines, m.blank(layout.Width))
	for _, footer := range layout.Footer {
		lines = append(lines, m.boxed(layout.Width, footer))
	}
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
		text, gutter, selected := "", strings.Repeat(" ", CursorField), false
		width := max(layout.ListWidth-CursorField, 0)
		if row < len(visible) {
			text = visible[row].Text
			// A section rule is chrome, not a row: it is painted flush to the
			// pane edge, where a row's cursor field sits, so its label starts on
			// the same column the cursor does. Its right edge still lands on the
			// shared meta column — see metaColumns.
			if visible[row].Chrome {
				gutter, width = "", max(layout.ListWidth, 0)
			}
			// A gutter glyph AND the selection slot. Ruby marks the cursor with
			// reverse video alone; that survives NO_COLOR but not a terminal or
			// theme that renders the slot as nothing, and a cursor you cannot
			// find is the one bug in a task list that costs you the wrong
			// completed task. The glyph is one cell of insurance that no styler
			// can take away.
			if row+layout.ViewportOffset == layout.Selected && layout.HasSelection {
				gutter, selected = Cursor, true
			}
		}
		line := gutter + m.fit(text, width)
		if selected {
			// Fit FIRST, composite second. The highlight has to cover the row's
			// own field colours and the padding out to the edge of the list, and
			// both of those are things `fit` produces — highlighting before it
			// leaves a half-lit row that stops at the first coloured field.
			line = m.styler.Composite("selection", line)
		}
		if hasPanel {
			panelText := ""
			if row < len(panelLines) {
				panelText = panelLines[row]
			}
			line += " " + m.railGlyph(layout, row) + " " +
				m.fit(panelText, max(layout.PanelContentWidth, 0))
		}
		out = append(out, m.boxed(layout.Width, " "+line))
	}
	return out
}

// panelColumn is the panel's title, divider, and content — the two chrome rows
// RightPanel budgets for, or none at all for a panel that heads its own
// sections. See RightPanel.Bare.
func (m *Model) panelColumn(layout ScreenLayout, view PanelView) []string {
	if m.panel == nil {
		return nil
	}
	if m.panel.Bare() {
		return view.Lines
	}
	lines := []string{
		m.styler.Paint("detail_label", view.Title),
		strings.Repeat("─", max(layout.PanelContentWidth, 0)),
	}
	return append(lines, view.Lines...)
}

// The split rule is a GRIP, not a border: a dotted rail with a solid handle at
// its middle, which the pointer can drag to resize the panel. The keys that
// resize it are still there and still authoritative; this is the affordance for
// the hand that is already on the mouse, and it is drawn the way it behaves —
// dotted where it is only a rule, solid where it is a handle.
//
// It lights up while a drag is in progress so the pointer's owner can see which
// of the two panes is following it.
const (
	railRule = "┆"
	railGrip = "┃"
	// railGripRows is the height of the handle, in body rows.
	railGripRows = 4
)

// railGlyph is the split rule's cell on one body row.
func (m *Model) railGlyph(layout ScreenLayout, row int) string {
	slot, glyph := "outline_thread", railRule
	begin := max((layout.BodyHeight-railGripRows)/2, 0)
	if row >= begin && row < begin+railGripRows {
		slot, glyph = "muted", railGrip
	}
	if m.railDrag != nil {
		slot = "accent"
	}
	return m.styler.Paint(slot, glyph)
}

// reconcileRowWidth rebuilds the rows when the list has changed width since
// they were built. The agenda aligns a date column against the list width, and
// the width changes for reasons the row builders never see — a resize, a panel
// opening, a drag on the rail. Reconciling here rather than at each of those
// call sites means there is one place that can be wrong, not a dozen.
func (m *Model) reconcileRowWidth(layout ScreenLayout) {
	if width := max(layout.ListWidth-CursorField, 0); width != m.rowWidth {
		m.RefreshRows()
	}
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

// headerCount is the right-hand side of the header: the open count, the notes
// naming any reveal that is currently on, and the help hint.
func (m *Model) headerCount() string {
	styler := m.styler
	// A reveal has to be visible in the chrome or the list is lying about what
	// it is: rows nothing on screen accounts for are how a reader concludes the
	// filter is broken.
	reveals := ""
	if m.showDeferred {
		reveals += styler.Paint("warning", "unavailable shown") + styler.Paint("muted", " · ")
	}
	// The closed note is view-scoped where the unavailable one is not, because
	// the toggle behind it is: saying "closed shown" over an Agenda that cannot
	// show a closed row either way would describe a list that is not there.
	if m.showClosed && closedToggleViews[m.view] {
		reveals += styler.Paint("warning", "closed shown") + styler.Paint("muted", " · ")
	}
	// The date leads the count because the agenda groups rows into OVERDUE and
	// TODAY: "today" is the frame of reference for the whole list, and a TUI
	// left open overnight must not quietly move that reference without saying
	// which day it moved to.
	// `? help` used to live here. It moved to the key-hint row, where every
	// other key the TUI advertises already is — the header's right side is for
	// what the list currently IS, not for what can be pressed.
	// Overdue leads the open count and is the only loud thing in the header,
	// because it is the one number that is a claim on you rather than a
	// description of the list. It is omitted at zero: a header that says
	// "0 overdue" every day teaches the eye to skip the place the real number
	// will appear.
	overdue := ""
	if count := m.OverdueTaskCount(); count > 0 {
		overdue = styler.Paint("due_overdue", fmt.Sprintf("%d overdue", count)) +
			styler.Paint("muted", " · ")
	}
	return styler.Paint("muted", m.todayStamp()+" · ") + reveals + overdue +
		styler.Paint("muted", fmt.Sprintf("%d open", m.OpenTaskCount()))
}

// todayStamp is the header's date: `mon 10 aug`.
func (m *Model) todayStamp() string {
	today := m.today
	if today.Zero() {
		return ""
	}
	return strings.ToLower(fmt.Sprintf("%s %02d %s",
		today.Weekday().String()[:3], today.Day, today.Month.String()[:3]))
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

// TabGap is the space between tab names. Three cells, not one: with the jump
// numbers gone the strip is six bare words, and one space between them reads as
// a sentence rather than as a set of tabs.
const TabGap = "   "

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
	return strings.Join(cells, TabGap)
}

// tabVariant is which label size fits the given budget. Rendering and mouse hit
// testing both call it, so a click can never land on a cell that was measured
// at a different label size than the one painted.
func (m *Model) tabVariant(budget int) int {
	for variant := range 3 {
		total := 0
		for index, tab := range Tabs {
			if index > 0 {
				total += len(TabGap)
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
	// Names only. The jump keys are advertised once, in the footer's `1-6
	// views` hint, rather than stamped onto every tab: six numbers in the strip
	// is six pieces of the same one fact, and they made a row of six names read
	// as twelve words.
	label := [3]string{tab.Label, tab.Compact, tab.Minimum}[variant]
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
	case ModeFilter, ModeForm, ModeFieldModal, ModePalette, ModeContextPalette, ModeLinkPicker, ModeTaskEdit:
	default:
		lines = append(lines, m.PromptLines(m.width-2)...)
	}
	// The key hint is the one footer element a host can plausibly re-paint in its
	// own chrome, so it is the one element SuppressKeyHints removes. Everything
	// above it — transcript, banner, flash, filters, prompt — is Tasks state the
	// host cannot render on Tasks' behalf, and it stays.
	if mode != ModeTaskEdit && !m.suppressKeyHints {
		// keyHint paints its own key/word pairs; wrapping the whole row in one
		// more slot would just be an SGR the first pair immediately resets.
		lines = append(lines, m.keyHint())
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
// The hint row is key-then-word pairs in the same idiom the detail panel's
// ACTIONS row uses: the key in the accent slot, what it does in the muted one.
// The old row spelled the same thing as one muted sentence joined by `·`, which
// made the keys — the only part you can act on — the hardest part to find.
//
// Rank is the order pairs are given up in as the terminal narrows, worst first.
// It is not the display order: `q quit` and `? help` are the last to go because
// they are the two ways out, and a hint row that has dropped everything else is
// exactly when someone needs them.
var keyHints = []struct {
	Key, Does string
	Rank      int
}{
	{"j/k", "move", 6},
	{"1-6", "views", 4},
	{"h/l", "fold", 5},
	{"enter", "details", 3},
	{"v", "unavailable", 7},
	{"/", "search", 2},
	{"q", "quit", 0},
	{"?", "all keys", 1},
}

func (m *Model) keyHint() string {
	pairs := make([]int, 0, len(keyHints))
	for index, hint := range keyHints {
		// A host that owns quit advertises it in its own chrome. Tasks still
		// acts on the key — it latches a request — but it stops claiming the
		// affordance.
		if m.suppressQuit && hint.Key == "q" {
			continue
		}
		// The view jump keys are advertised HERE and nowhere else, so this is
		// the one line SuppressViewKeyHints removes. A host that owns the number
		// row must not have Tasks teach its users a binding the host answers.
		if m.suppressViewKeyHints && hint.Key == "1-6" {
			continue
		}
		pairs = append(pairs, index)
	}
	for len(pairs) > 0 {
		line := " "
		for position, index := range pairs {
			if position > 0 {
				line += m.styler.Paint("muted", "   ")
			}
			line += m.styler.Paint("accent", keyHints[index].Key) +
				m.styler.Paint("muted", " "+keyHints[index].Does)
		}
		if m.styler.Width(line) <= m.width-2 {
			return line
		}
		pairs = dropWorstHint(pairs)
	}
	return ""
}

// dropWorstHint removes the highest-ranked (most expendable) pair, keeping the
// rest in display order.
func dropWorstHint(pairs []int) []int {
	worst := 0
	for position, index := range pairs {
		if keyHints[index].Rank > keyHints[pairs[worst]].Rank {
			worst = position
		}
	}
	return append(append([]int{}, pairs[:worst]...), pairs[worst+1:]...)
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

// The frame is UNDRAWN. Ruby's TUI, and this port until now, painted a box:
// corners, side rails, and a horizontal rule above and below the body.
//
// Four of the terminal's rows and two of its columns went to saying "the
// application is here", which the alternate screen already says. Worse, the
// rules cut the one continuous thing on screen — the list and the detail rail
// beside it — into three stacked boxes, so the eye crossed a border to get from
// a task to its own details.
//
// What replaces them is space: a blank row under the header and another above
// the footer. The body keeps the two columns of left padding the border and its
// inner space used to occupy, so every screen coordinate in ScreenLayout is
// unchanged — only the rows the chrome consumed are given back to the list.
//
// The one thing a border did carry is gone with it: the border and
// border_gradient theme slots no longer paint anything here. They remain in the
// theme vocabulary for term/frame, which still draws bordered surfaces.

func (m *Model) boxed(width int, text string) string {
	return " " + m.fit(text, max(width-2, 0)) + " "
}

func (m *Model) blank(width int) string {
	return strings.Repeat(" ", max(width, 0))
}
