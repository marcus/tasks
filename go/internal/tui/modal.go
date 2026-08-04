package tui

import (
	"fmt"
	"strings"

	"tasks-go/internal/tui/term/ansi"
)

// ModalKind names what an overlay is for. The kind decides which keys the
// modal answers, so a confirmation cannot be dismissed by the key that scrolls
// help — and, more importantly, `y` cannot mean "archive" while a help modal is
// open.
type ModalKind string

// The modal kinds this build has.
const (
	ModalHelp                   ModalKind = "help"
	ModalArchiveConfirm         ModalKind = "archive_confirm"
	ModalArchiveBlocked         ModalKind = "archive_blocked"
	ModalProjectCompleteConfirm ModalKind = "project_complete_confirm"
	ModalProjectArchiveConfirm  ModalKind = "project_archive_confirm"
	ModalUnsupportedSchema      ModalKind = "unsupported_schema"
	ModalTaskDraftQuitConfirm   ModalKind = "task_draft_quit_confirm"
	ModalAgentActivity          ModalKind = "agent_activity"
	ModalAgentQueueCancel       ModalKind = "agent_queue_cancel_confirm"
)

// modalMinInner is the inner row count kept even in a degenerate body.
const modalMinInner = 3

// Modal is one open overlay: content, scroll position, and an optional live
// line filter.
//
// Three invariants it keeps for itself, all of them about NOT MOVING:
//   - the box width comes from the FULL content, title included, so scrolling
//     or filtering never resizes it;
//   - View never yields more rows than fit — the border takes two rows of the
//     body and the status line one more;
//   - while a filter is active the box keeps its full unfiltered height, so
//     neither the box nor its centered position jumps as matches come and go.
//
// A modal that resized while you typed into it would be unusable for the one
// thing filtering is for: reading a long list while narrowing it.
type Modal struct {
	title      string
	all        []string
	kind       ModalKind
	filterable bool
	// filterGroups ties each line to a group id. A match on any line in a group
	// keeps the WHOLE group, which is what makes filtering a shortcut list show
	// the section heading a matched binding lives under.
	filterGroups []string
	scroll       int
	filter       string
	filtered     []string
	haystack     []string
	width        int
}

// ModalOptions builds a Modal.
type ModalOptions struct {
	Title        string
	Lines        []string
	Kind         ModalKind
	Filterable   bool
	FilterGroups []string
}

// NewModal builds one. A filter-group slice that does not align with the lines
// is dropped rather than honoured, because a misaligned group map would hide
// the wrong lines — silently.
func NewModal(options ModalOptions) *Modal {
	groups := options.FilterGroups
	if groups != nil && len(groups) != len(options.Lines) {
		groups = nil
	}
	return &Modal{
		title:        options.Title,
		all:          append([]string{}, options.Lines...),
		kind:         options.Kind,
		filterable:   options.Filterable,
		filterGroups: groups,
	}
}

// Title, Kind, Scroll and Filter are the read accessors.
func (m *Modal) Title() string   { return m.title }
func (m *Modal) Kind() ModalKind { return m.kind }
func (m *Modal) Scroll() int     { return m.scroll }
func (m *Modal) Filter() string  { return m.filter }

// Filterable reports whether `/` opens a line filter on this modal.
func (m *Modal) Filterable() bool { return m.filterable }

// AllLines is the unfiltered content.
func (m *Modal) AllLines() []string { return append([]string{}, m.all...) }

// Replace swaps live content without discarding the user's filter or scroll
// intent. The next View clamps scroll against the refreshed match set.
func (m *Modal) Replace(title string, lines []string, filterGroups []string) {
	m.title = title
	m.all = append([]string{}, lines...)
	if filterGroups != nil && len(filterGroups) != len(lines) {
		filterGroups = nil
	}
	m.filterGroups = filterGroups
	m.filtered = nil
	m.haystack = nil
	m.width = 0
}

// SetFilter applies a query. An unchanged query keeps the memo AND the scroll
// position, so a repeated keystroke does not throw the reader back to the top.
func (m *Modal) SetFilter(query string) {
	normalized := query
	if strings.TrimSpace(query) == "" {
		normalized = ""
	}
	if normalized == m.filter {
		return
	}
	m.filter = normalized
	m.filtered = nil
	m.scroll = 0
}

// Lines is the content after the filter, or all of it when none is active.
func (m *Modal) Lines() []string {
	if m.filter == "" {
		return m.all
	}
	if m.filtered != nil {
		return m.filtered
	}
	needle := strings.ToLower(m.filter)
	matches := []int{}
	for index, line := range m.haystackLines() {
		if strings.Contains(line, needle) {
			matches = append(matches, index)
		}
	}
	if m.filterGroups != nil {
		groups := map[string]bool{}
		for _, index := range matches {
			groups[m.filterGroups[index]] = true
		}
		kept := []string{}
		for index, line := range m.all {
			if groups[m.filterGroups[index]] {
				kept = append(kept, line)
			}
		}
		m.filtered = kept
		return m.filtered
	}
	kept := []string{}
	for _, index := range matches {
		kept = append(kept, m.all[index])
	}
	m.filtered = kept
	return m.filtered
}

// haystackLines is the stripped, downcased content, built once. A keystroke
// then costs one substring scan of pre-stripped text rather than a fresh
// escape-sequence strip of every line.
func (m *Modal) haystackLines() []string {
	if m.haystack != nil {
		return m.haystack
	}
	m.haystack = make([]string, 0, len(m.all))
	for _, line := range m.all {
		m.haystack = append(m.haystack, strings.ToLower(ansi.Strip(line)))
	}
	return m.haystack
}

// Width is the box width from the full, unfiltered content. The frame clamps it
// to the terminal.
func (m *Modal) Width(styler Styler) int {
	if m.width > 0 {
		return m.width
	}
	widest := 0
	for _, line := range m.all {
		if measured := styler.Width(line); measured > widest {
			widest = measured
		}
	}
	m.width = max(max(widest, styler.Width(m.title)+6), 30) + 4
	return m.width
}

// Viewport is how many content rows are visible at once inside body rows. A
// filter pins the query line to the top and the count to the bottom, so two
// fewer content rows are available while one is active.
func (m *Modal) Viewport(bodyHeight int) int {
	if m.filter != "" {
		return max(m.lockedRows(bodyHeight)-2, 0)
	}
	inner := m.innerBudget(bodyHeight)
	if m.hasStatus(inner) {
		return inner - 1
	}
	return inner
}

// ScrollLine, ScrollHalf and ScrollPage are the three scroll gestures.
func (m *Modal) ScrollLine(delta, bodyHeight int) { m.scrollBy(delta, bodyHeight) }

func (m *Modal) ScrollHalf(direction, bodyHeight int) {
	m.scrollBy(direction*max(m.Viewport(bodyHeight)/2, 1), bodyHeight)
}

func (m *Modal) ScrollPage(direction, bodyHeight int) {
	m.scrollBy(direction*m.Viewport(bodyHeight), bodyHeight)
}

func (m *Modal) scrollBy(delta, bodyHeight int) {
	limit := max(len(m.Lines())-m.Viewport(bodyHeight), 0)
	m.scroll = clamp(m.scroll+delta, 0, limit)
}

// ModalView is the window the frame draws.
type ModalView struct {
	Title string
	Lines []string
	Width int
}

// View is the window at the current scroll, at most bodyHeight-2 lines so the
// boxed result fits the body. A non-empty filterLine — or an active filter —
// switches to the fixed-height filtered view.
func (m *Modal) View(styler Styler, bodyHeight int, filterLine string) ModalView {
	if filterLine != "" || m.filter != "" {
		return m.filteredView(styler, bodyHeight, filterLine)
	}
	lines := m.Lines()
	viewport := m.Viewport(bodyHeight)
	m.scroll = clamp(m.scroll, 0, max(len(lines)-viewport, 0))
	shown := sliceLines(lines, m.scroll, viewport)
	out := append([]string{}, shown...)
	if m.hasStatus(m.innerBudget(bodyHeight)) {
		out = append(out, m.statusLine(styler, lines, len(shown)))
	}
	return ModalView{Title: m.title, Lines: out, Width: m.Width(styler)}
}

// filteredView is the fixed-height view: the query line on top, matched
// (scrolled) content padded to the RETAINED height, and the count at the
// bottom. The row count equals what the unfiltered box shows, so the box size
// and its centered position stay put across keystrokes.
func (m *Modal) filteredView(styler Styler, bodyHeight int, filterLine string) ModalView {
	lines := m.Lines()
	rows := m.lockedRows(bodyHeight)
	// In a degenerate short modal the status row would crowd out the content
	// itself — drop it before dropping matches.
	status := rows > 2
	contentRows := rows - 1
	if status {
		contentRows--
	}
	contentRows = max(contentRows, 0)
	m.scroll = clamp(m.scroll, 0, max(len(lines)-contentRows, 0))
	shown := sliceLines(lines, m.scroll, contentRows)

	content := append([]string{}, shown...)
	if len(shown) == 0 && m.filter != "" && contentRows > 0 {
		content = []string{styler.Paint("muted", fmt.Sprintf("no lines match “%s”", m.filter))}
	}
	for len(content) < contentRows {
		content = append(content, "")
	}
	header := filterLine
	if header == "" {
		header = styler.Paint("prompt", "/ "+m.filter)
	}
	out := append([]string{header}, content...)
	if status {
		out = append(out, m.statusLine(styler, lines, len(shown)))
	}
	return ModalView{Title: m.title, Lines: out, Width: m.Width(styler)}
}

func (m *Modal) innerBudget(bodyHeight int) int {
	return max(bodyHeight-2, modalMinInner)
}

// lockedRows is the inner row count with no filter applied — also the height
// the box retains while filtering.
func (m *Modal) lockedRows(bodyHeight int) int {
	return min(len(m.all), m.innerBudget(bodyHeight))
}

// hasStatus: a status line appears whenever the unfiltered content overflows
// the body. The filtered view always shows one, for the match count.
func (m *Modal) hasStatus(inner int) bool { return len(m.all) > inner }

func (m *Modal) statusLine(styler Styler, lines []string, shown int) string {
	parts := []string{fmt.Sprintf("%d/%d", min(m.scroll+shown, len(lines)), len(lines))}
	if len(lines) > shown {
		parts = append(parts, "↑↓ scroll")
	}
	return styler.Paint("muted", "── "+strings.Join(parts, " · ")+" ──")
}

func sliceLines(lines []string, start, count int) []string {
	if start < 0 || start >= len(lines) || count <= 0 {
		return []string{}
	}
	end := min(start+count, len(lines))
	return append([]string{}, lines[start:end]...)
}
