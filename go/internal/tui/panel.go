package tui

import "fmt"

// Panel kinds. A panel's kind decides which refresh path keeps it current and
// which keys it answers.
const (
	PanelDetail        = "detail"
	PanelProjectDetail = "project_detail"
)

// RightPanel is the persistent panel on the right side — the port of
// lib/tui/right_panel.rb.
//
// The behavior worth preserving is the scroll rule: replacing content for the
// SAME identity keeps the reader's place, and moving to another task starts the
// new content at the top. A refresh caused by an external file write must not
// throw a reader back to the first line of a long note.
type RightPanel struct {
	Title    string
	Kind     string
	Identity string
	Lines    []string
	Scroll   int

	FocusedRow    int
	HasFocusedRow bool
}

// NewRightPanel builds a panel showing content for one identity.
func NewRightPanel(title, kind, identity string, lines []string) *RightPanel {
	return &RightPanel{Title: title, Kind: kind, Identity: identity, Lines: lines}
}

// Replace swaps the content. Scroll resets only when the identity changes.
func (p *RightPanel) Replace(title, identity string, lines []string) *RightPanel {
	if identity != p.Identity {
		p.Scroll = 0
	}
	p.Title = title
	p.Lines = lines
	p.Identity = identity
	p.HasFocusedRow = false
	return p
}

// FocusRow asks the next view to reveal a specific content row. The editor
// packet uses this to keep the focused field on screen.
func (p *RightPanel) FocusRow(row int) {
	p.FocusedRow = row
	p.HasFocusedRow = true
}

// PanelView is one frame of panel content.
type PanelView struct {
	Title string
	Lines []string
	Width int
}

// View renders the panel into the given box. The title and divider consume two
// body rows. A status row appears only when the content overflows, and is
// included in the returned line budget.
func (p *RightPanel) View(styler Styler, height, width int) PanelView {
	if styler == nil {
		styler = PlainStyler{}
	}
	budget := max(height-2, 0)
	viewport := p.contentViewport(budget)
	if p.HasFocusedRow {
		p.revealFocusedRow(viewport)
	}
	p.Scroll = clamp(p.Scroll, 0, max(len(p.Lines)-viewport, 0))

	shown := []string{}
	for index := p.Scroll; index < len(p.Lines) && index < p.Scroll+viewport; index++ {
		shown = append(shown, styler.Truncate(p.Lines[index], width))
	}
	if p.overflows(budget) {
		shown = append(shown, styler.Truncate(p.statusLine(styler, len(shown)), width))
	}
	return PanelView{Title: p.Title, Lines: shown, Width: width}
}

// ScrollLine moves by whole lines.
func (p *RightPanel) ScrollLine(delta, height int) { p.scrollBy(delta, height) }

// ScrollHalf moves by half a viewport, at least one line.
func (p *RightPanel) ScrollHalf(direction, height int) {
	p.scrollBy(direction*max(p.Viewport(height)/2, 1), height)
}

// ScrollPage moves by a whole viewport.
func (p *RightPanel) ScrollPage(direction, height int) {
	p.scrollBy(direction*p.Viewport(height), height)
}

// Viewport is the number of content lines visible at this height.
func (p *RightPanel) Viewport(height int) int {
	return p.contentViewport(max(height-2, 0))
}

func (p *RightPanel) revealFocusedRow(viewport int) {
	if viewport <= 0 {
		return
	}
	row := clamp(p.FocusedRow, 0, max(len(p.Lines)-1, 0))
	if row < p.Scroll {
		p.Scroll = row
	}
	if row >= p.Scroll+viewport {
		p.Scroll = row - viewport + 1
	}
}

func (p *RightPanel) contentViewport(budget int) int {
	if p.overflows(budget) {
		return max(budget-1, 0)
	}
	return max(budget, 0)
}

func (p *RightPanel) overflows(budget int) bool {
	return budget > 0 && len(p.Lines) > budget
}

func (p *RightPanel) scrollBy(delta, height int) {
	viewport := p.Viewport(height)
	p.Scroll = clamp(p.Scroll+delta, 0, max(len(p.Lines)-viewport, 0))
}

func (p *RightPanel) statusLine(styler Styler, shown int) string {
	last := min(p.Scroll+shown, len(p.Lines))
	return styler.Paint("muted", fmt.Sprintf("%d/%d · ctrl-u/d scroll", last, len(p.Lines)))
}
