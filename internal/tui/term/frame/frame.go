// Package frame is a pure frame builder: given content, it returns one string
// per terminal row, each exactly `width` visible cells wide. No IO — the caller
// decides how to paint, and tests assert on the result.
//
// Go port of Ruby's lib/tui/frame.rb.
package frame

import (
	"strings"

	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/border"
	"github.com/marcus/tasks/internal/tui/term/layout"
	"github.com/marcus/tasks/internal/tui/term/theme"
)

// Row is one list row. Text carries the row's own field styling, each field
// closed with a reset — the compositing contract selectedRow depends on.
type Row struct{ Text string }

// Panel is the fixed-width right pane.
type Panel struct {
	Title string
	Lines []string
}

// Options describes one frame.
type Options struct {
	Width, Height int
	Header        string
	Rows          []Row
	// Selected is the absolute row index to highlight, or nil.
	Selected  *int
	Footer    []layout.FooterLine
	Popup     *layout.Popup
	Panel     *Panel
	Modal     *layout.Modal
	Layout    *layout.Layout
	Theme     *theme.Theme
	Truecolor bool
	// PanelOffset is only consulted when Layout is nil.
	PanelOffset int
}

// Cursor is the selection glyph. Deliberately distinct from the collapsed-row
// marker ("▸ ") so a selected collapsed row reads "❯ ▸ title", not a doubled
// marker. One cell plus a trailing space = two cells, matching the marker
// column width.
const Cursor = "❯ "

// Build renders the whole frame.
func Build(opts Options) []string {
	th := opts.Theme
	if th == nil {
		th = theme.Default()
	}
	lay := opts.Layout
	if lay == nil {
		lay = layout.New(layout.Options{
			Width: opts.Width, Height: opts.Height, Footer: opts.Footer,
			Selected: opts.Selected, Panel: opts.Panel != nil,
			PanelOffset: opts.PanelOffset,
		})
	}
	width := lay.Width
	height := lay.Height
	w := width - 2
	footer := lay.Footer
	bodyH := lay.BodyHeight
	listW := lay.ListWidth

	visible := layout.VisibleRows(lay, opts.Rows)
	body := make([]string, 0, bodyH)
	for vi, row := range visible {
		if lay.SelectedScreenRow != nil && vi == *lay.SelectedScreenRow {
			body = append(body, selectedRow(th, row, listW))
		} else {
			body = append(body, "  "+row.Text)
		}
	}
	for i := range body {
		body[i] = ansi.VPad(ansi.VTrunc(body[i], listW), listW)
	}
	// Empty filler rows are pre-padded so we do not pay a second full-body pad
	// pass just to expand a short viewport.
	filler := strings.Repeat(" ", listW)
	for len(body) < bodyH {
		body = append(body, filler)
	}

	if opts.Panel != nil {
		renderPanel(th, body, *opts.Panel, lay)
	}

	// Modal first, then the popup on top. Archive confirmation and forms must
	// remain visible above a persistent task-detail panel.
	if opts.Modal != nil {
		overlayModal(th, body, *opts.Modal, w-2, opts.Truecolor)
	}
	if opts.Popup != nil {
		overlay(body, *opts.Popup, w-2)
	}

	// The outer ring is UNDRAWN, and so are the rules that used to sit above
	// and below the body. Four rows and two columns went to saying "the
	// application is here", which the alternate screen already says, and the
	// rules cut the list and the detail rail beside it into stacked boxes.
	// Blank rows separate them now. See internal/tui/render.go, whose output
	// this must match cell for cell.
	//
	// The border painter and its gradient are NOT retired with the ring: every
	// floating surface — modal, popup, the editor form — still draws a box, and
	// a box there is doing real work, separating the surface from what is
	// behind it. Only the frame that had nothing behind it is gone.
	blank := strings.Repeat(" ", width)
	pad := func(text string) string { return " " + ansi.VPad(ansi.VTrunc(text, w), w) + " " }

	lines := make([]string, 0, height)
	lines = append(lines, pad(opts.Header))
	lines = append(lines, blank)
	for _, b := range body {
		lines = append(lines, "  "+ansi.VPad(b, w-2)+"  ")
	}
	lines = append(lines, blank)
	for _, f := range footer {
		if f.Rule {
			lines = append(lines, blank)
			continue
		}
		lines = append(lines, pad(f.Text))
	}
	return lines
}

func renderPanel(th *theme.Theme, body []string, panel Panel, lay *layout.Layout) {
	contentWidth := lay.PanelContentWidth
	panelLines := []string{
		th.Paint("panel_title", ansi.VTrunc(panel.Title, contentWidth)),
		th.Paint("muted", strings.Repeat("─", contentWidth)),
	}
	panelLines = append(panelLines, panel.Lines...)

	for index := range body {
		source := ""
		if index < len(panelLines) {
			source = panelLines[index]
		}
		content := ansi.VPad(ansi.VTrunc(source, contentWidth), contentWidth)
		listLine := ansi.VPad(ansi.VTrunc(body[index], lay.ListWidth), lay.ListWidth)
		body[index] = listLine + "│ " + content
	}
}

// selectedRow renders the selected row with its own field colors intact ON TOP
// of the selection background, via SGR compositing (no stripping or repaint).
//
// Contract: the row text already carries its field SGRs, each closed with a
// reset. We (1) open the line with the selection SGR, (2) re-open it
// immediately after every reset so a field's own fg/attrs layer over the
// selection background instead of clearing it, (3) pad the visible text to the
// full inner width so the background spans the row, then (4) close with a
// single reset. Truncation runs FIRST, on the composed cursor+text, so the
// pad+reset tail can never be clipped. A selection slot that resolves to
// nothing (an unstyled theme) skips compositing and just pads.
func selectedRow(th *theme.Theme, row Row, w int) string {
	body := ansi.VTrunc(Cursor+row.Text, w)
	sel := th.SGR("selection")
	if sel == "" {
		return ansi.VPad(body, w)
	}
	composited := ansi.Composite(sel, body)
	if pad := w - ansi.VisLen(body); pad > 0 {
		composited += strings.Repeat(" ", pad)
	}
	return composited + "\x1b[0m"
}

// overlay pastes popup lines over the body starting at terminal-cell
// coordinates, preserving styled base content on either side. Cell slices
// replace any partially covered wide grapheme with padding, so content after
// the popup remains in its original column.
func overlay(body []string, popup layout.Popup, w int) {
	for k, pl := range popup.Lines {
		r := popup.Row + k
		if r < 0 || r >= len(body) {
			continue
		}
		col := popup.Col
		sourceStart := 0
		if col < 0 {
			sourceStart = -col
		}
		destStart := col
		if destStart < 0 {
			destStart = 0
		}
		if destStart >= w {
			continue
		}

		base := ansi.VPad(ansi.VTrunc(body[r], w), w)
		replacement := ansi.CellSlice(pl, sourceStart, w-destStart)
		replacementWidth := ansi.VisLen(replacement)
		prefix := ansi.VPad(ansi.CellSlice(base, 0, destStart), destStart)
		suffixStart := destStart + replacementWidth
		suffix := ansi.CellSlice(base, suffixStart, w-suffixStart)
		body[r] = ansi.VPad(ansi.VTrunc(prefix+replacement+suffix, w), w)
	}
}

// overlayModal boxes up modal content and centers it over the body via overlay.
// Modal.Width pins the box width (so scrolling cannot resize the box); without
// it the width fits the visible lines. The title strip is painted with the
// modal_title slot, so themes can give it a background.
func overlayModal(th *theme.Theme, body []string, modal layout.Modal, w int, truecolor bool) {
	if len(body) < 3 || w < 4 {
		compact := append([]string{th.Paint("modal_title", modal.Title)}, modal.Lines...)
		if len(compact) > len(body) {
			compact = compact[:len(body)]
		}
		overlay(body, layout.Popup{Lines: compact, Row: 0, Col: 0}, w)
		return
	}

	bw := modal.Width
	if bw == 0 {
		widest := 0
		for _, l := range modal.Lines {
			if v := ansi.VisLen(l); v > widest {
				widest = v
			}
		}
		bw = maxInt(maxInt(widest, ansi.VisLen(modal.Title)+6), 30) + 4
	}
	bw = maxInt(minInt(bw, w), 4)

	inner := make([]string, 0, len(modal.Lines))
	for _, l := range modal.Lines {
		inner = append(inner, ansi.VTrunc(l, bw-4))
	}
	if len(inner) > len(body)-2 {
		inner = inner[:len(body)-2]
	}
	boxed := make([]string, 0, len(inner))
	for _, l := range inner {
		boxed = append(boxed, " "+ansi.VPad(l, bw-4)+" ")
	}
	title := th.Paint("modal_title", ansi.VTrunc(" "+modal.Title+" ", bw-4))
	box := border.Box(border.BoxOptions{
		InnerLines: boxed,
		InnerWidth: bw - 2,
		Gradient:   th.Gradient("border"),
		Solid:      th.SGR("border"),
		Truecolor:  truecolor,
		Title:      title,
		TitleLead:  1,
	})

	row := maxInt((len(body)-len(box))/2, 0)
	col := maxInt((w-bw)/2, 0)
	if modal.Placed {
		row, col = modal.Row, modal.Col
	}
	overlay(body, layout.Popup{Lines: box, Row: row, Col: col}, w)
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
