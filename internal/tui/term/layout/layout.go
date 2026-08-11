// Package layout is pure geometry for one sampled terminal frame. Rendering,
// popup placement, modal scrolling, and selection coordinates all consume the
// same value so a resize cannot mix dimensions from different terminal-size
// reads.
//
// Go port of Ruby's lib/tui/screen_layout.rb. It lives with the terminal
// primitives because both the frame renderer and the mouse hit map must agree
// on the same rectangles; nothing here knows about tasks or views.
package layout

import "github.com/marcus/tasks/internal/tui/term/ansi"

const (
	// FixedRows is the header plus the two blank rows that separate it and the
	// footer from the body. The frame itself is undrawn — see internal/tui's
	// render.go, whose geometry this port must agree with cell for cell.
	FixedRows = 3

	PanelRatio          = 0.40
	WidePanelRatio      = 0.58
	MinPanelWidth       = 28
	MinListWidth        = 8
	EditMinContentWidth = 32
	InlineContentWidth  = 48
	// EditPanelChromeRows are the right panel's title and divider.
	EditPanelChromeRows = 2
	// EditMinVisibleRows is the compact focused-field fallback.
	EditMinVisibleRows = 1
)

// PanelMode names one of the right panel's width presets.
type PanelMode string

const (
	PanelCompact  PanelMode = "compact"
	PanelStandard PanelMode = "standard"
	PanelWide     PanelMode = "wide"
	PanelFocus    PanelMode = "focus"
)

// PanelModes is the promotion ladder, narrowest first.
var PanelModes = []PanelMode{PanelCompact, PanelStandard, PanelWide, PanelFocus}

// Breakpoint describes how much room the panel has for editable content.
type Breakpoint string

const (
	BelowMinimum Breakpoint = "below_minimum"
	Stacked      Breakpoint = "stacked"
	Inline       Breakpoint = "inline"
)

// FooterLine is one footer entry. A Rule line draws a divider instead of text.
type FooterLine struct {
	Text string
	Rule bool
}

// Text builds an ordinary footer line.
func Text(s string) FooterLine { return FooterLine{Text: s} }

// Rule is the divider sentinel (Ruby's :rule symbol).
func Rule() FooterLine { return FooterLine{Rule: true} }

// Options describes one sampled frame.
type Options struct {
	Width, Height int
	Footer        []FooterLine
	// Selected is the absolute index of the selected row; nil means no
	// selection.
	Selected    *int
	Panel       bool
	PanelMode   PanelMode
	PanelOffset int
	Editing     bool
}

// Layout is the resolved geometry. All fields are read-only after
// construction.
type Layout struct {
	Width, Height      int
	Footer             []FooterLine
	BodyHeight         int
	BodyWidth          int
	ListWidth          int
	PanelWidth         int
	PanelContentWidth  int
	ViewportOffset     int
	Selected           *int
	SelectedScreenRow  *int
	PanelMode          PanelMode
	RequestedPanelMode PanelMode
	EditContentHeight  int
	PanelOffset        int
	editing            bool
}

// New resolves a layout from the sampled terminal size.
func New(opts Options) *Layout {
	l := &Layout{Width: opts.Width, Height: opts.Height}

	keep := opts.Height - FixedRows - 1
	if keep < 0 {
		keep = 0
	}
	footer := opts.Footer
	if len(footer) > keep {
		footer = footer[len(footer)-keep:]
	}
	l.Footer = append([]FooterLine(nil), footer...)

	l.BodyHeight = maxInt(opts.Height-FixedRows-len(l.Footer), 1)
	l.BodyWidth = maxInt(opts.Width-4, 1)
	l.RequestedPanelMode = normalizePanelMode(opts.PanelMode)
	l.PanelOffset = opts.PanelOffset
	l.editing = opts.Editing

	if opts.Panel {
		l.PanelMode, l.PanelWidth = l.calculatePanel()
	} else {
		l.PanelMode, l.PanelWidth = l.RequestedPanelMode, 0
	}
	if l.PanelWidth == 0 {
		l.PanelContentWidth = 0
	} else {
		l.PanelContentWidth = maxInt(l.PanelWidth-2, 1)
	}
	l.EditContentHeight = maxInt(l.BodyHeight-EditPanelChromeRows, 0)
	l.ListWidth = l.BodyWidth - l.PanelWidth
	l.Selected = opts.Selected
	if opts.Selected != nil && *opts.Selected >= l.BodyHeight {
		l.ViewportOffset = *opts.Selected - l.BodyHeight + 1
	}
	if opts.Selected != nil {
		row := *opts.Selected - l.ViewportOffset
		l.SelectedScreenRow = &row
	}
	return l
}

func (l *Layout) FooterSize() int { return len(l.Footer) }
func (l *Layout) HasPanel() bool  { return l.PanelWidth > 0 }
func (l *Layout) Editing() bool   { return l.editing }

// EditablePanel reports whether the panel has room for the task editor.
func (l *Layout) EditablePanel() bool {
	return l.HasPanel() && l.PanelContentWidth >= EditMinContentWidth &&
		l.EditContentHeight >= EditMinVisibleRows
}

// ContentBreakpoint classifies the panel's editable content width.
func (l *Layout) ContentBreakpoint() Breakpoint {
	switch {
	case l.PanelContentWidth < EditMinContentWidth:
		return BelowMinimum
	case l.PanelContentWidth < InlineContentWidth:
		return Stacked
	default:
		return Inline
	}
}

// Span is a half-open range [Begin, End) over 0-based terminal cells, so a
// click and a painted glyph always agree on "row 2 is the first body row".
type Span struct{ Begin, End int }

func (s Span) Covers(v int) bool { return v >= s.Begin && v < s.End }

func (l *Layout) HeaderRow() int { return 0 }

// BodyOrigin is the (row, col) of the first body cell.
func (l *Layout) BodyOrigin() (int, int) { return 2, 2 }

func (l *Layout) BodyRows() Span { return Span{2, 2 + l.BodyHeight} }
func (l *Layout) ListCols() Span { return Span{2, 2 + l.ListWidth} }

// PanelDividerCol is the column of the "│" between list and panel, or -1 when
// no panel is drawn.
func (l *Layout) PanelDividerCol() int {
	if !l.HasPanel() {
		return -1
	}
	return 2 + l.ListWidth
}

func (l *Layout) PanelCols() Span {
	if !l.HasPanel() {
		return Span{0, 0}
	}
	return Span{2 + l.ListWidth + 2, 2 + l.BodyWidth}
}

func (l *Layout) FooterRows() Span {
	start := l.BodyHeight + 3
	return Span{start, start + len(l.Footer)}
}

// MinimumEditTerminalWidth is the narrowest terminal that can host the editor.
func MinimumEditTerminalWidth() int { return 4 + MinListWidth + 2 + EditMinContentWidth }

// MinimumEditTerminalHeight is the named zero-footer minimum. Every footer/help
// row consumes another terminal row before the panel title, divider, and
// focused fallback.
func MinimumEditTerminalHeight(footerRows int) int {
	return FixedRows + footerRows + EditPanelChromeRows + EditMinVisibleRows
}

// VisibleRows returns the slice of rows inside the viewport.
func VisibleRows[T any](l *Layout, rows []T) []T {
	if l.ViewportOffset >= len(rows) {
		return nil
	}
	end := l.ViewportOffset + l.BodyHeight
	if end > len(rows) {
		end = len(rows)
	}
	return rows[l.ViewportOffset:end]
}

// Popup is an overlay placed in body coordinates.
type Popup struct {
	Lines    []string
	Row, Col int
}

// PlacePopup positions a popup under (or above) the selected row, clamped into
// the body.
func (l *Layout) PlacePopup(popup Popup, preferredCol int) Popup {
	popupWidth := 0
	for _, line := range popup.Lines {
		if w := ansi.VisLen(line); w > popupWidth {
			popupWidth = w
		}
	}
	popupHeight := len(popup.Lines)
	hi := maxInt(l.BodyWidth-popupWidth, 0)
	col := clamp(preferredCol, 0, hi)

	selectedRow := 0
	if l.SelectedScreenRow != nil {
		selectedRow = *l.SelectedScreenRow
	}
	below := selectedRow + 1
	var row int
	switch {
	case popupHeight <= l.BodyHeight-below:
		row = below
	case popupHeight <= selectedRow:
		row = selectedRow - popupHeight
	default:
		row = maxInt(l.BodyHeight-popupHeight, 0)
	}
	popup.Row = row
	popup.Col = col
	return popup
}

// Modal is a centered box over the body. Width pins the box width; zero means
// "fit the content".
type Modal struct {
	Title string
	Lines []string
	Width int
	// Row and Col are meaningful only once Placed is true; PlaceModal sets all
	// three. An unplaced modal is centered by the renderer.
	Row, Col int
	Placed   bool
}

// PlaceModal supplies the modal's stable anchor. The frame still draws the box.
func (l *Layout) PlaceModal(modal Modal) Modal {
	modal.Placed = true
	if l.BodyHeight < 3 || l.BodyWidth < 4 {
		modal.Row, modal.Col = 0, 0
		return modal
	}
	boxWidth, boxHeight := l.ModalBoxSize(modal)
	modal.Row = maxInt((l.BodyHeight-boxHeight)/2, 0)
	modal.Col = maxInt((l.BodyWidth-boxWidth)/2, 0)
	return modal
}

// ModalBoxSize is the drawn box size for a modal, shared by the renderer and
// the hit map so a click and a glyph never disagree.
func (l *Layout) ModalBoxSize(modal Modal) (width, height int) {
	boxWidth := modal.Width
	if boxWidth == 0 {
		widest := 0
		for _, line := range modal.Lines {
			if w := ansi.VisLen(line); w > widest {
				widest = w
			}
		}
		boxWidth = maxInt(maxInt(widest, ansi.VisLen(modal.Title)+6), 30) + 4
	}
	boxWidth = maxInt(minInt(boxWidth, l.BodyWidth), 4)
	boxHeight := minInt(len(modal.Lines), l.BodyHeight-2) + 2
	return boxWidth, boxHeight
}

func (l *Layout) calculatePanel() (PanelMode, int) {
	candidates := []PanelMode{l.RequestedPanelMode}
	if l.editing {
		for i, mode := range PanelModes {
			if mode == l.RequestedPanelMode {
				candidates = PanelModes[i:]
				break
			}
		}
	}
	for _, mode := range candidates {
		width := l.panelWidthFor(mode)
		if !l.editing || width-2 >= EditMinContentWidth {
			return mode, l.applyPanelOffset(width)
		}
	}
	mode := PanelFocus
	if len(candidates) > 0 {
		mode = candidates[len(candidates)-1]
	}
	return mode, l.applyPanelOffset(l.panelWidthFor(mode))
}

func (l *Layout) panelWidthFor(mode PanelMode) int {
	if l.BodyWidth < MinListWidth+3 {
		return maxInt(l.BodyWidth-1, 0)
	}
	var desired int
	switch mode {
	case PanelCompact:
		desired = EditMinContentWidth + 2
	case PanelStandard:
		desired = maxInt(roundHalfUp(float64(l.BodyWidth)*PanelRatio), MinPanelWidth)
	case PanelWide:
		desired = maxInt(roundHalfUp(float64(l.BodyWidth)*WidePanelRatio), MinPanelWidth)
	case PanelFocus:
		desired = l.BodyWidth - MinListWidth
	}
	return clamp(desired, 3, l.BodyWidth-MinListWidth)
}

// applyPanelOffset shifts the panel by a signed column count on top of the mode
// width. The same invariants that bound panelWidthFor bound the result: the
// list keeps MinListWidth, editing keeps EditMinContentWidth of content, and
// the panel never goes negative or wider than the body.
func (l *Layout) applyPanelOffset(width int) int {
	if l.PanelOffset == 0 || l.BodyWidth < MinListWidth+3 {
		return width
	}
	hi := l.BodyWidth - MinListWidth
	floor := 3
	if l.editing {
		floor = EditMinContentWidth + 2
	}
	return clamp(width+l.PanelOffset, minInt(floor, hi), hi)
}

func normalizePanelMode(v PanelMode) PanelMode {
	for _, mode := range PanelModes {
		if mode == v {
			return v
		}
	}
	return PanelStandard
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if lo > hi {
		lo = hi
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// roundHalfUp matches Ruby's Float#round, which rounds halves away from zero
// (Go's math.Round does the same, but the intent is worth naming here because
// the panel ratio lands on .5 at common terminal widths).
func roundHalfUp(f float64) int {
	if f < 0 {
		return -int(-f + 0.5)
	}
	return int(f + 0.5)
}
