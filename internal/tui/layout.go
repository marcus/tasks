package tui

// ScreenLayout is pure geometry for ONE sampled terminal frame — the port of
// lib/tui/screen_layout.rb.
//
// Ruby introduced it so a resize could not mix dimensions from two winsize
// reads. Bubble Tea removes that hazard (a WindowSizeMsg is one coherent
// value), but the type earns its keep for the other reason: rendering, panel
// placement, selection coordinates and mouse hit testing all have to agree
// about where row 3 is, and the only way to guarantee that is to compute it
// once.
type ScreenLayout struct {
	Width  int
	Height int
	Footer []string

	BodyHeight int
	BodyWidth  int

	ListWidth         int
	PanelWidth        int
	PanelContentWidth int
	PanelMode         string
	RequestedMode     string
	PanelOffset       int

	Selected          int
	HasSelection      bool
	ViewportOffset    int
	SelectedScreenRow int

	EditContentHeight int
	Editing           bool
}

// The panel width ladder. Names and numbers are Ruby's.
const (
	FixedRows = 5 // borders, header, and the two rules outside the body/footer

	PanelRatio          = 0.40
	WidePanelRatio      = 0.58
	MinPanelWidth       = 28
	MinListWidth        = 8
	EditMinContentWidth = 32
	InlineContentWidth  = 48
	EditPanelChromeRows = 2 // RightPanel title and divider
	EditMinVisibleRows  = 1 // compact focused-field fallback
)

// PanelModes is the promotion ladder, narrowest first.
var PanelModes = []string{"compact", "standard", "wide", "focus"}

// LayoutRequest is what a caller knows before geometry is computed.
type LayoutRequest struct {
	Width        int
	Height       int
	Footer       []string
	Selected     int
	HasSelection bool
	Panel        bool
	PanelMode    string
	PanelOffset  int
	Editing      bool
}

// NewScreenLayout computes one frame's geometry.
func NewScreenLayout(styler Styler, request LayoutRequest) ScreenLayout {
	if styler == nil {
		styler = PlainStyler{}
	}
	footer := request.Footer
	if keep := request.Height - 6; keep < len(footer) {
		if keep < 0 {
			keep = 0
		}
		footer = footer[len(footer)-keep:]
	}
	copied := append([]string{}, footer...)

	layout := ScreenLayout{
		Width:         request.Width,
		Height:        request.Height,
		Footer:        copied,
		RequestedMode: normalizePanelMode(request.PanelMode),
		PanelOffset:   request.PanelOffset,
		Editing:       request.Editing,
		Selected:      request.Selected,
		HasSelection:  request.HasSelection,
	}
	layout.BodyHeight = max(request.Height-FixedRows-len(copied), 1)
	layout.BodyWidth = max(request.Width-4, 1)

	if request.Panel {
		layout.PanelMode, layout.PanelWidth = layout.calculatePanel()
	} else {
		layout.PanelMode, layout.PanelWidth = layout.RequestedMode, 0
	}
	if layout.PanelWidth == 0 {
		layout.PanelContentWidth = 0
	} else {
		layout.PanelContentWidth = max(layout.PanelWidth-2, 1)
	}
	layout.EditContentHeight = max(layout.BodyHeight-EditPanelChromeRows, 0)
	layout.ListWidth = layout.BodyWidth - layout.PanelWidth

	if layout.HasSelection && layout.Selected >= layout.BodyHeight {
		layout.ViewportOffset = layout.Selected - layout.BodyHeight + 1
	}
	if layout.HasSelection {
		layout.SelectedScreenRow = layout.Selected - layout.ViewportOffset
	}
	return layout
}

// FooterSize is the number of footer rows this frame reserved.
func (l ScreenLayout) FooterSize() int { return len(l.Footer) }

// HasPanel reports whether the right panel occupies any columns.
func (l ScreenLayout) HasPanel() bool { return l.PanelWidth > 0 }

// EditablePanel reports whether the panel is large enough to host the editor.
// The editor packet owns what "editing" means; this is the geometry half of the
// contract, and it is here because the width ladder is here.
func (l ScreenLayout) EditablePanel() bool {
	return l.HasPanel() && l.PanelContentWidth >= EditMinContentWidth &&
		l.EditContentHeight >= EditMinVisibleRows
}

// ContentBreakpoint is the responsive-width decision the editor renders against.
func (l ScreenLayout) ContentBreakpoint() string {
	switch {
	case l.PanelContentWidth < EditMinContentWidth:
		return "below_minimum"
	case l.PanelContentWidth < InlineContentWidth:
		return "stacked"
	default:
		return "inline"
	}
}

// Screen-coordinate rectangles shared by rendering and hit testing. All ranges
// are half-open over 0-based terminal cells, so a click and a painted glyph
// always agree that row 3 is the first body row.

// HeaderRow is the row the tab strip paints on.
func (l ScreenLayout) HeaderRow() int { return 1 }

// BodyRows is [begin, end) of the list body.
func (l ScreenLayout) BodyRows() (int, int) { return 3, 3 + l.BodyHeight }

// ListCols is [begin, end) of the list columns.
func (l ScreenLayout) ListCols() (int, int) { return 2, 2 + l.ListWidth }

// PanelDividerCol is the column the split rule occupies, or -1 with no panel.
func (l ScreenLayout) PanelDividerCol() int {
	if !l.HasPanel() {
		return -1
	}
	return 2 + l.ListWidth
}

// PanelCols is [begin, end) of the panel columns, empty with no panel.
func (l ScreenLayout) PanelCols() (int, int) {
	if !l.HasPanel() {
		return 0, 0
	}
	return 2 + l.ListWidth + 2, 2 + l.BodyWidth
}

// FooterRows is [begin, end) of the footer.
func (l ScreenLayout) FooterRows() (int, int) {
	start := l.BodyHeight + 4
	return start, start + len(l.Footer)
}

// MinimumEditTerminalWidth is the narrowest terminal that can host the editor.
func MinimumEditTerminalWidth() int { return 4 + MinListWidth + 2 + EditMinContentWidth }

// MinimumEditTerminalHeight is the shortest terminal that can host the editor
// with the given number of footer rows. Every footer or help row consumes
// another terminal row before the panel title, divider, and focused fallback.
func MinimumEditTerminalHeight(footerRows int) int {
	return FixedRows + footerRows + EditPanelChromeRows + EditMinVisibleRows
}

// VisibleRows is the slice of rows this frame paints.
func (l ScreenLayout) VisibleRows(rows []Row) []Row {
	if l.ViewportOffset >= len(rows) {
		return nil
	}
	end := min(l.ViewportOffset+l.BodyHeight, len(rows))
	return rows[l.ViewportOffset:end]
}

// calculatePanel picks the mode and width. Mode selection stays keyed off each
// mode's natural (unoffset) width so the editing promotion ladder is unchanged;
// the column offset is a user tweak layered on top of whichever mode wins.
func (l ScreenLayout) calculatePanel() (string, int) {
	candidates := []string{l.RequestedMode}
	if l.Editing {
		candidates = PanelModes[indexOfMode(l.RequestedMode):]
	}
	for _, mode := range candidates {
		width := l.panelWidthFor(mode)
		if !l.Editing || width-2 >= EditMinContentWidth {
			return mode, l.applyPanelOffset(width)
		}
	}
	mode := candidates[len(candidates)-1]
	return mode, l.applyPanelOffset(l.panelWidthFor(mode))
}

func (l ScreenLayout) panelWidthFor(mode string) int {
	if l.BodyWidth < MinListWidth+3 {
		return max(l.BodyWidth-1, 0)
	}
	desired := 0
	switch mode {
	case "compact":
		desired = EditMinContentWidth + 2
	case "standard":
		desired = max(roundHalfUp(float64(l.BodyWidth)*PanelRatio), MinPanelWidth)
	case "wide":
		desired = max(roundHalfUp(float64(l.BodyWidth)*WidePanelRatio), MinPanelWidth)
	case "focus":
		desired = l.BodyWidth - MinListWidth
	}
	return clamp(desired, 3, l.BodyWidth-MinListWidth)
}

// applyPanelOffset is the signed column tweak the resize keys apply on top of
// the mode width. The same invariants that bound panelWidthFor bound the
// result: the list keeps MinListWidth, editing keeps EditMinContentWidth of
// content, and the panel never goes negative or wider than the body.
func (l ScreenLayout) applyPanelOffset(width int) int {
	if l.PanelOffset == 0 || l.BodyWidth < MinListWidth+3 {
		return width
	}
	hi := l.BodyWidth - MinListWidth
	floor := 3
	if l.Editing {
		floor = EditMinContentWidth + 2
	}
	return clamp(width+l.PanelOffset, min(floor, hi), hi)
}

func normalizePanelMode(value string) string {
	for _, mode := range PanelModes {
		if mode == value {
			return value
		}
	}
	return "standard"
}

func indexOfMode(mode string) int {
	for index, candidate := range PanelModes {
		if candidate == mode {
			return index
		}
	}
	return 1
}

// roundHalfUp matches Ruby's Float#round for the positive values a ratio
// produces. Go's math.Round rounds half away from zero, which agrees here, but
// spelling it out keeps the intent legible next to the Ruby it mirrors.
func roundHalfUp(value float64) int { return int(value + 0.5) }

func clamp(value, low, high int) int {
	if high < low {
		high = low
	}
	return min(max(value, low), high)
}
