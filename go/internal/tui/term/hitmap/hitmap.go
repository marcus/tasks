// Package hitmap is pure geometry: it maps a screen (row, col) to a Hit zone
// using the rectangles a layout publishes. It operates on integers and zone
// names only.
//
// Go port of Ruby's lib/tui/hit_map.rb.
package hitmap

import (
	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/term/layout"
)

// Zone names a clickable region of the frame.
type Zone string

const (
	ZoneOutside        Zone = "outside"
	ZoneBorder         Zone = "border"
	ZoneHeader         Zone = "header"
	ZoneTab            Zone = "tab"
	ZoneListRow        Zone = "list_row"
	ZoneCollapseMarker Zone = "collapse_marker"
	ZonePanel          Zone = "panel"
	ZonePanelDivider   Zone = "panel_divider"
	ZoneFooterRow      Zone = "footer_row"
	ZoneModalRow       Zone = "modal_row"
	ZonePopupRow       Zone = "popup_row"
)

// FooterRole classifies a footer line for routing purposes.
type FooterRole string

const (
	RoleChrome   FooterRole = "chrome"
	RoleResponse FooterRole = "response"
	RolePrompt   FooterRole = "prompt"
)

// FooterPayload is the payload a footer hit carries.
type FooterPayload struct {
	Index int
	Role  FooterRole
}

// Hit is one resolved zone. Index carries the row/line payload for list, modal,
// popup and footer zones; Tab carries the tab key; Footer carries the footer
// index and role.
type Hit struct {
	Zone   Zone
	Index  int
	Tab    string
	Footer FooterPayload
}

// Outside is the never-nil answer for coordinates off the frame.
var Outside = Hit{Zone: ZoneOutside}

// TabSpan is one clickable tab: a key and the half-open column range it paints.
type TabSpan struct {
	Key        string
	Start, End int
}

// MarkerSpan is the half-open column range of a row's collapse marker, measured
// inside the row text (after the two-cell cursor prefix).
type MarkerSpan struct{ Start, End int }

// Options builds a HitMap.
type Options struct {
	Layout      *layout.Layout
	TabSpans    []TabSpan
	RowCount    int
	Modal       *layout.Modal
	Popup       *layout.Popup
	Panel       bool
	MarkerSpans map[int]MarkerSpan
	FooterRoles []FooterRole
}

// HitMap answers zone queries for one sampled frame.
type HitMap struct {
	opts Options
}

// Build constructs a hit map.
func Build(opts Options) *HitMap { return &HitMap{opts: opts} }

// At resolves a screen coordinate. It never fails: unknown coordinates are
// ZoneOutside.
func (m *HitMap) At(row, col int) Hit {
	l := m.opts.Layout
	if row < 0 || col < 0 || row >= l.Height || col >= l.Width {
		return Outside
	}
	if hit, ok := m.hitPopup(row, col); ok {
		return hit
	}
	if hit, ok := m.hitModal(row, col); ok {
		return hit
	}
	if hit, ok := m.hitBody(row, col); ok {
		return hit
	}
	if hit, ok := m.hitHeader(row, col); ok {
		return hit
	}
	if hit, ok := m.hitFooter(row, col); ok {
		return hit
	}
	// Outer ring, header/footer divider rules, and anything else on-frame.
	return Hit{Zone: ZoneBorder}
}

func (m *HitMap) hitPopup(row, col int) (Hit, bool) {
	popup := m.opts.Popup
	if popup == nil || len(popup.Lines) == 0 {
		return Hit{}, false
	}
	originRow, originCol := m.opts.Layout.BodyOrigin()
	widths := make([]int, len(popup.Lines))
	maxW := 0
	for i, line := range popup.Lines {
		widths[i] = ansi.VisLen(line)
		if widths[i] > maxW {
			maxW = widths[i]
		}
	}
	screenRow := originRow + popup.Row
	screenCol := originCol + popup.Col
	if row < screenRow || row > screenRow+len(popup.Lines)-1 {
		return Hit{}, false
	}
	if col < screenCol || col > screenCol+maxW-1 {
		return Hit{}, false
	}
	localRow := row - screenRow
	if col >= screenCol+widths[localRow] {
		return Hit{Zone: ZoneBorder}, true
	}
	return Hit{Zone: ZonePopupRow, Index: localRow}, true
}

func (m *HitMap) hitModal(row, col int) (Hit, bool) {
	if m.opts.Modal == nil {
		return Hit{}, false
	}
	l := m.opts.Layout
	originRow, originCol := l.BodyOrigin()
	placed := *m.opts.Modal
	if !placed.Placed {
		placed = l.PlaceModal(placed)
	}
	// Mirror the renderer: the box is lines plus top/bottom borders, its width
	// pinned by Modal.Width or derived the same way the renderer derives it.
	boxW, boxH := m.modalBoxSize(placed)
	screenRow := originRow + placed.Row
	screenCol := originCol + placed.Col
	if row < screenRow || row > screenRow+boxH-1 {
		return Hit{}, false
	}
	if col < screenCol || col > screenCol+boxW-1 {
		return Hit{}, false
	}
	localRow := row - screenRow
	// Interior content rows are 1..(boxH-2); borders are chrome.
	if localRow > 0 && localRow < boxH-1 {
		return Hit{Zone: ZoneModalRow, Index: localRow - 1}, true
	}
	return Hit{Zone: ZoneBorder}, true
}

func (m *HitMap) modalBoxSize(placed layout.Modal) (width, height int) {
	l := m.opts.Layout
	if l.BodyHeight < 3 || l.BodyWidth < 4 {
		content := len(placed.Lines)
		if placed.Title != "" {
			content++
		}
		if content > l.BodyHeight {
			content = l.BodyHeight
		}
		return l.BodyWidth, content
	}
	return l.ModalBoxSize(placed)
}

func (m *HitMap) hitBody(row, col int) (Hit, bool) {
	l := m.opts.Layout
	if !l.BodyRows().Covers(row) {
		return Hit{}, false
	}
	if m.opts.Panel && l.HasPanel() {
		if col == l.PanelDividerCol() {
			return Hit{Zone: ZonePanelDivider}, true
		}
		if l.PanelCols().Covers(col) {
			return Hit{Zone: ZonePanel, Index: row - l.BodyRows().Begin}, true
		}
	}
	if !l.ListCols().Covers(col) {
		return Hit{}, false
	}

	vis := row - l.BodyRows().Begin
	abs := l.ViewportOffset + vis
	if abs >= m.opts.RowCount {
		return Hit{Zone: ZoneBorder}, true
	}

	// Cursor/prefix columns occupy the first two list cells; marker spans are
	// measured inside the row text (after that prefix).
	textCol := col - l.ListCols().Begin - 2
	if textCol >= 0 {
		if span, ok := m.opts.MarkerSpans[abs]; ok {
			if textCol >= span.Start && textCol < span.End {
				return Hit{Zone: ZoneCollapseMarker, Index: abs}, true
			}
		}
	}
	return Hit{Zone: ZoneListRow, Index: abs}, true
}

func (m *HitMap) hitHeader(row, col int) (Hit, bool) {
	l := m.opts.Layout
	if row != l.HeaderRow() {
		return Hit{}, false
	}
	for _, span := range m.opts.TabSpans {
		if col >= span.Start && col < span.End {
			return Hit{Zone: ZoneTab, Tab: span.Key}, true
		}
	}
	return Hit{Zone: ZoneHeader}, true
}

func (m *HitMap) hitFooter(row, col int) (Hit, bool) {
	l := m.opts.Layout
	rows := l.FooterRows()
	if !rows.Covers(row) {
		return Hit{}, false
	}
	if col < 1 || col > l.Width-2 {
		return Hit{Zone: ZoneBorder}, true
	}
	index := row - rows.Begin
	role := RoleChrome
	if index < len(m.opts.FooterRoles) && m.opts.FooterRoles[index] != "" {
		role = m.opts.FooterRoles[index]
	}
	return Hit{Zone: ZoneFooterRow, Footer: FooterPayload{Index: index, Role: role}}, true
}
