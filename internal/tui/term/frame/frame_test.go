package frame

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/hitmap"
	"github.com/marcus/tasks/internal/tui/term/layout"
	"github.com/marcus/tasks/internal/tui/term/theme"
)

// Mirrors test/test_frame.rb.

func sel(n int) *int { return &n }

func sampleRows(n int) []Row {
	rows := make([]Row, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, Row{Text: fmt.Sprintf("task number %d", i)})
	}
	return rows
}

func footerText(texts ...string) []layout.FooterLine {
	out := make([]layout.FooterLine, 0, len(texts))
	for _, t := range texts {
		out = append(out, layout.Text(t))
	}
	return out
}

// build applies the same defaults the Ruby helper uses, then the overrides.
func build(mutate func(*Options)) []string {
	opts := Options{
		Width: 60, Height: 15, Header: "header", Rows: sampleRows(5),
		Footer: footerText("keybar", "prompt"),
	}
	if mutate != nil {
		mutate(&opts)
	}
	return Build(opts)
}

// bodyInner strips the two-cell margin from each side of a body row, leaving
// the inner content. The frame is borderless, so the margin is plain spaces —
// but the row's own styling may open before them, which is why this is a regexp
// and not a slice.
var (
	sgrRun    = `(?:\x1b\[[0-9;]*m)*`
	leftEdge  = regexp.MustCompile(`^` + sgrRun + `  `)
	rightEdge = regexp.MustCompile(`  ` + sgrRun + `$`)
)

func bodyInner(line string) string {
	return rightEdge.ReplaceAllString(leftEdge.ReplaceAllString(line, ""), "")
}

func TestEveryLineHasExactVisibleWidth(t *testing.T) {
	for i, line := range build(nil) {
		if ansi.VisLen(line) != 60 {
			t.Fatalf("line %d wrong width: %q", i, line)
		}
	}
}

func TestFrameHeightMatchesTerminal(t *testing.T) {
	if got := len(build(nil)); got != 15 {
		t.Fatalf("height = %d", got)
	}
}

func TestShortFrameClipsFooterToPreserveExactHeight(t *testing.T) {
	lines := build(func(o *Options) {
		o.Width, o.Height = 20, 6
		o.Footer = []layout.FooterLine{layout.Text("old response"), layout.Rule(), layout.Text("prompt")}
	})
	if len(lines) != 6 {
		t.Fatalf("height = %d", len(lines))
	}
	for _, line := range lines {
		if ansi.VisLen(line) != 20 {
			t.Fatalf("width = %d: %q", ansi.VisLen(line), line)
		}
		if strings.Contains(ansi.Strip(line), "old response") {
			t.Fatalf("clipped footer line survived: %q", line)
		}
	}
}

func TestSelectedRowIsHighlighted(t *testing.T) {
	lines := build(func(o *Options) { o.Selected = sel(2) })
	// header(1) + blank(1) + 2 rows
	if !strings.Contains(lines[4], "\x1b[7m") {
		t.Fatalf("no reverse video: %q", lines[4])
	}
	if !strings.Contains(ansi.Strip(lines[4]), "❯ task number 3") {
		t.Fatalf("wrong row selected: %q", ansi.Strip(lines[4]))
	}
}

func TestSelectedRowCompositesSelectionUnderFieldColors(t *testing.T) {
	th := theme.Configure("default", map[string]string{"selection": "on-blue"})
	line := build(func(o *Options) {
		o.Theme = th
		o.Rows = []Row{{Text: "\x1b[35mProject\x1b[0m task"}}
		o.Selected = sel(0)
	})[2]

	if !strings.Contains(line, "\x1b[44m❯ ") {
		t.Fatalf("line does not open with the selection SGR + cursor: %q", line)
	}
	if !strings.Contains(line, "\x1b[35mProject") {
		t.Fatalf("the field's own fg SGR was stripped: %q", line)
	}
	if !strings.Contains(line, "\x1b[0m\x1b[44m") {
		t.Fatalf("selection SGR not re-injected after the field reset: %q", line)
	}
	if !strings.Contains(ansi.Strip(line), "❯ Project task") {
		t.Fatalf("visible text = %q", ansi.Strip(line))
	}
	if !strings.HasSuffix(bodyInner(line), "\x1b[0m") {
		t.Fatalf("row does not close with a reset: %q", bodyInner(line))
	}
}

func TestSelectedRowPadsToFullWidthUnderSelection(t *testing.T) {
	th := theme.Configure("default", map[string]string{"selection": "on-blue"})
	line := build(func(o *Options) {
		o.Theme = th
		o.Rows = []Row{{Text: "short"}}
		o.Selected = sel(0)
	})[2]
	inner := bodyInner(line)
	if ansi.VisLen(inner) != 56 {
		t.Fatalf("inner width = %d, want 60 - borders/margins", ansi.VisLen(inner))
	}
	if !strings.HasSuffix(inner, "\x1b[0m") || !strings.Contains(inner, "\x1b[44m") {
		t.Fatalf("selection background missing across the row: %q", inner)
	}
}

func TestSelectedRowReverseVideoCoversPadding(t *testing.T) {
	// Mono / NO_COLOR: selection is reverse video (attribute-only, no bg color).
	line := build(func(o *Options) {
		o.Theme = theme.Configure("mono", nil)
		o.Rows = []Row{{Text: "plain row"}}
		o.Selected = sel(0)
	})[2]
	inner := bodyInner(line)
	if !strings.HasPrefix(inner, "\x1b[7m") {
		t.Fatalf("reverse video must open the row: %q", inner)
	}
	if ansi.VisLen(inner) != 56 || !strings.HasSuffix(inner, "\x1b[0m") {
		t.Fatalf("inner = %q (width %d)", inner, ansi.VisLen(inner))
	}
	if !strings.Contains(ansi.Strip(inner), "❯ plain row") {
		t.Fatalf("visible = %q", ansi.Strip(inner))
	}
}

func TestSelectedRowTruncatesAndStaysWellFormed(t *testing.T) {
	th := theme.Configure("default", map[string]string{"selection": "on-blue"})
	line := build(func(o *Options) {
		o.Theme = th
		o.Rows = []Row{{Text: "\x1b[35m" + strings.Repeat("x", 200) + "\x1b[0m"}}
		o.Selected = sel(0)
	})[2]
	inner := bodyInner(line)
	if ansi.VisLen(inner) != 56 {
		t.Fatalf("truncated row width = %d", ansi.VisLen(inner))
	}
	if !strings.HasSuffix(inner, "\x1b[0m") {
		t.Fatalf("truncated row does not close: %q", inner)
	}
	if !strings.Contains(ansi.Strip(inner), "…") {
		t.Fatalf("no ellipsis: %q", ansi.Strip(inner))
	}
	if !strings.Contains(inner, "\x1b[44m") {
		t.Fatalf("selection SGR lost in truncation: %q", inner)
	}
}

func TestSelectedPlainRowGetsFullWidthSelection(t *testing.T) {
	th := theme.Configure("default", map[string]string{"selection": "on-blue"})
	line := build(func(o *Options) {
		o.Theme = th
		o.Rows = []Row{{Text: "no ansi here"}}
		o.Selected = sel(0)
	})[2]
	inner := bodyInner(line)
	if !strings.HasPrefix(inner, "\x1b[44m❯ ") {
		t.Fatalf("selection must open even with no field SGRs: %q", inner)
	}
	if ansi.VisLen(inner) != 56 || !strings.HasSuffix(inner, "\x1b[0m") {
		t.Fatalf("inner = %q", inner)
	}
}

func TestFooterRuleSentinelDrawsDivider(t *testing.T) {
	lines := build(func(o *Options) {
		o.Footer = []layout.FooterLine{
			layout.Text("response line"), layout.Rule(), layout.Text("keybar"), layout.Text("prompt"),
		}
	})
	// The divider sentinel now paints a blank separator rather than a ├──┤
	// rule: the frame draws no chrome, so a footer break is space.
	rule := ansi.Strip(lines[len(lines)-3])
	if strings.TrimSpace(rule) != "" || len(rule) != 60 {
		t.Fatalf("rule = %q", rule)
	}
}

func TestLongRowsTruncateNotOverflow(t *testing.T) {
	lines := build(func(o *Options) { o.Rows = []Row{{Text: strings.Repeat("x", 200)}} })
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
	if !strings.Contains(ansi.Strip(lines[2]), "…") {
		t.Fatalf("no ellipsis: %q", ansi.Strip(lines[2]))
	}
}

func TestScrollsToKeepSelectionVisible(t *testing.T) {
	lines := build(func(o *Options) {
		o.Rows = sampleRows(50)
		o.Selected = sel(49)
	})
	found, leaked := false, false
	for _, l := range lines {
		text := ansi.Strip(l)
		if strings.Contains(text, "task number 50") {
			found = true
		}
		if strings.Contains(text, "task number 1 ") {
			leaked = true
		}
	}
	if !found || leaked {
		t.Fatalf("found = %v, leaked = %v", found, leaked)
	}
}

func TestPopupOverlaysBody(t *testing.T) {
	lines := build(func(o *Options) {
		o.Popup = &layout.Popup{Lines: []string{"[POPUP]"}, Row: 1, Col: 4}
	})
	if !strings.Contains(ansi.Strip(lines[3]), "[POPUP]") {
		t.Fatalf("row = %q", ansi.Strip(lines[3]))
	}
	if ansi.VisLen(lines[3]) != 60 {
		t.Fatalf("width = %d", ansi.VisLen(lines[3]))
	}
}

func TestRightPanelSplitsBodyAtFixedLayoutWidth(t *testing.T) {
	lay := layout.New(layout.Options{
		Width: 60, Height: 15, Footer: footerText("prompt"), Selected: sel(0), Panel: true,
	})
	lines := build(func(o *Options) {
		o.Rows = []Row{{Text: "selected task"}}
		o.Selected = sel(0)
		o.Footer = lay.Footer
		o.Panel = &Panel{Title: "task", Lines: []string{"details", "more"}}
		o.Layout = lay
	})
	body := ansi.Strip(lines[2])
	if !strings.Contains(body, "selected task") || !strings.Contains(body, "task") {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(ansi.Strip(lines[5]), "more") {
		t.Fatalf("panel line = %q", ansi.Strip(lines[5]))
	}
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
}

func TestPanelWidthDoesNotChangeWithContent(t *testing.T) {
	short := build(func(o *Options) { o.Panel = &Panel{Title: "task", Lines: []string{"x"}} })
	long := build(func(o *Options) {
		o.Panel = &Panel{Title: "task", Lines: []string{strings.Repeat("x", 200)}}
	})
	shortDivider := strings.Index(ansi.Strip(short[3])[len("x"):], "│")
	longDivider := strings.Index(ansi.Strip(long[3])[len("x"):], "│")
	if shortDivider != longDivider {
		t.Fatalf("divider moved: %d vs %d", shortDivider, longDivider)
	}
}

func TestPanelRemainsRenderableAtMinimumTerminalSize(t *testing.T) {
	lines := build(func(o *Options) {
		o.Width, o.Height = 8, 6
		o.Rows = []Row{{Text: "task"}}
		o.Footer = nil
		o.Selected = sel(0)
		o.Panel = &Panel{Title: "task", Lines: []string{"details"}}
	})
	if len(lines) != 6 {
		t.Fatalf("height = %d", len(lines))
	}
	for _, l := range lines {
		if ansi.VisLen(l) != 8 {
			t.Fatalf("width = %d: %q", ansi.VisLen(l), l)
		}
	}
}

func TestPopupPreservesBaseContentOnBothSides(t *testing.T) {
	row := ansi.Strip(build(func(o *Options) {
		o.Rows = []Row{{Text: "left-side middle-part right-side-content"}}
		o.Popup = &layout.Popup{Lines: []string{"[P]"}, Row: 0, Col: 12}
	})[2])
	for _, want := range []string{"left-side", "[P]", "right-side-content"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row %q missing %q", row, want)
		}
	}
}

func TestPopupSplicesAtTerminalCellBoundaries(t *testing.T) {
	// Body coordinates include the two-cell row marker. Column 3 lands on the
	// first cell of 界; replacing only that cell must blank the other half while
	// leaving b at its original terminal column.
	line := ansi.Strip(build(func(o *Options) {
		o.Width, o.Height = 20, 8
		o.Rows = []Row{{Text: "a界bcdef"}}
		o.Footer = nil
		o.Popup = &layout.Popup{Lines: []string{"X"}, Row: 0, Col: 3}
	})[2])
	if !strings.Contains(line, "aX bcdef") {
		t.Fatalf("line = %q", line)
	}
	if ansi.VisLen(line) != 20 {
		t.Fatalf("width = %d", ansi.VisLen(line))
	}
}

func TestPopupPreservesStylesAroundWideBaseContent(t *testing.T) {
	line := build(func(o *Options) {
		o.Width, o.Height = 20, 8
		o.Rows = []Row{{Text: ansi.Red("a界bcdef")}}
		o.Footer = nil
		o.Popup = &layout.Popup{Lines: []string{ansi.Bold("X")}, Row: 0, Col: 3}
	})[2]
	if ansi.VisLen(line) != 20 {
		t.Fatalf("width = %d", ansi.VisLen(line))
	}
	if !strings.Contains(line, "\x1b[1mX\x1b[0m") {
		t.Fatalf("popup styling lost: %q", line)
	}
	if !strings.Contains(line, "\x1b[31m bcdef") {
		t.Fatalf("base styling not restored on the suffix: %q", line)
	}
	if !strings.Contains(ansi.Strip(line), "aX bcdef") {
		t.Fatalf("visible = %q", ansi.Strip(line))
	}
}

func TestWidePopupIsClippedWithoutOverflowAtRightEdge(t *testing.T) {
	line := build(func(o *Options) {
		o.Width, o.Height = 20, 8
		o.Rows = []Row{{Text: "underlay"}}
		o.Footer = nil
		o.Popup = &layout.Popup{Lines: []string{"界"}, Row: 0, Col: 15}
	})[2]
	if ansi.VisLen(line) != 20 {
		t.Fatalf("width = %d", ansi.VisLen(line))
	}
	if strings.Contains(ansi.Strip(line), "界") {
		t.Fatalf("a two-cell cluster cannot be half-rendered: %q", line)
	}
}

func TestPopupClipsCleanlyAtNegativeLeftEdge(t *testing.T) {
	line := build(func(o *Options) {
		o.Width, o.Height = 20, 8
		o.Rows = []Row{{Text: "underlay"}}
		o.Footer = nil
		o.Popup = &layout.Popup{Lines: []string{"[ABC]"}, Row: 0, Col: -2}
	})[2]
	if ansi.VisLen(line) != 20 {
		t.Fatalf("width = %d", ansi.VisLen(line))
	}
	if !strings.Contains(ansi.Strip(line), "BC]nderlay") {
		t.Fatalf("visible = %q", ansi.Strip(line))
	}
}

func TestModalDrawsCenteredBoxWithTitle(t *testing.T) {
	lines := build(func(o *Options) {
		o.Modal = &layout.Modal{Title: "task", Lines: []string{"alpha", "beta gamma"}}
	})
	joined := ""
	var boxLine string
	for _, l := range lines {
		text := ansi.Strip(l)
		joined += text + "\n"
		if strings.Contains(text, "╭─ task") {
			boxLine = text
		}
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
	for _, want := range []string{"╭─ task ", "alpha", "beta gamma", "╰"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if idx := strings.Index(boxLine, "╭"); idx <= 5 {
		t.Fatalf("box not centered: %q", boxLine)
	}
}

func TestModalUsesCompactUnboxedViewWhenBodyIsOneRow(t *testing.T) {
	lines := build(func(o *Options) {
		// FixedRows plus one: the shortest frame whose body is a single row.
		o.Width, o.Height = 8, 4
		o.Rows = nil
		o.Footer = nil
		o.Modal = &layout.Modal{Title: "task", Lines: []string{"details"}}
	})
	if len(lines) != 4 {
		t.Fatalf("height = %d", len(lines))
	}
	for _, l := range lines {
		if ansi.VisLen(l) != 8 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
	body := ansi.Strip(lines[2])
	if !strings.Contains(body, "tas") {
		t.Fatalf("compact modal unidentifiable: %q", body)
	}
	if strings.ContainsAny(body, "╭╮╰╯┌┐└┘") {
		t.Fatalf("clipped box fragment: %q", body)
	}
}

func TestPopupRendersOnTopOfModal(t *testing.T) {
	row := ansi.Strip(build(func(o *Options) {
		o.Modal = &layout.Modal{Title: "task", Lines: []string{"alpha", "beta", "gamma", "delta"}}
		o.Popup = &layout.Popup{Lines: []string{strings.Repeat("X", 50)}, Row: 3, Col: 0}
	})[2+3])
	if !strings.Contains(row, strings.Repeat("X", 50)) {
		t.Fatalf("popup must overlay the modal: %q", row)
	}
	if strings.Contains(row, "│ alpha") {
		t.Fatalf("modal content not covered: %q", row)
	}
}

func TestModalWiderThanFrameIsClamped(t *testing.T) {
	lines := build(func(o *Options) {
		o.Modal = &layout.Modal{Title: "wide", Lines: []string{strings.Repeat("z", 200)}}
	})
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
}

func TestModalExplicitWidthPinsTheBox(t *testing.T) {
	raw := build(func(o *Options) {
		o.Modal = &layout.Modal{Title: "t", Lines: []string{"short"}, Width: 44}
	})
	var top, bottom, content string
	for _, l := range raw {
		text := ansi.Strip(l)
		if strings.Contains(text, "╭─ t ") {
			top = text
		}
		if bottom == "" && strings.Contains(text, "╰") {
			bottom = text
		}
		if content == "" && strings.Contains(text, "short") {
			content = text
		}
	}
	if got := runeIndexLast(top, "╮") - runeIndex(top, "╭") + 1; got != 44 {
		t.Fatalf("top border width = %d: %q", got, top)
	}
	if got := runeIndexLast(bottom, "╯") - runeIndex(bottom, "╰") + 1; got != 44 {
		t.Fatalf("bottom border width = %d: %q", got, bottom)
	}
	if runeIndex(top, "╭") != runeIndexFrom(content, "│", 1) {
		t.Fatalf("content row does not align with the border: %q / %q", top, content)
	}
}

func TestModalExplicitWidthIsClampedToFrame(t *testing.T) {
	lines := build(func(o *Options) {
		o.Modal = &layout.Modal{Title: "t", Lines: []string{"short"}, Width: 500}
	})
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
}

func TestModalTitleIsPaintedWithThemeSlot(t *testing.T) {
	th := theme.Configure("default", map[string]string{"modal_title": "on-blue"})
	lines := build(func(o *Options) {
		o.Theme = th
		o.Modal = &layout.Modal{Title: "task", Lines: []string{"x"}}
	})
	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "╭─ task") {
			if !strings.Contains(l, "\x1b[44m task \x1b[0m") {
				t.Fatalf("title strip missing modal_title style: %q", l)
			}
			return
		}
	}
	t.Fatal("no modal title row rendered")
}

func TestEmptyRowsRenderBlankBody(t *testing.T) {
	lines := build(func(o *Options) { o.Rows = nil })
	if len(lines) != 15 {
		t.Fatalf("height = %d", len(lines))
	}
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("width = %d", ansi.VisLen(l))
		}
	}
}

// The outer ring is gone, but the gradient it used to carry is not: every
// floating surface still draws a box, and that box is where the sweep now
// lives. This asserts both halves — no ring, and a swept modal box.
func TestFrameDrawsNoOuterRingButStillSweepsFloatingBoxes(t *testing.T) {
	th := theme.Configure("default", map[string]string{"border_gradient": "#000000 #ffffff @45"})
	modal := layout.Modal{Title: "confirm", Lines: []string{"really?", "second line"}}
	lines := build(func(o *Options) {
		o.Theme = th
		o.Truecolor = true
		o.Modal = &modal
	})
	for index, line := range lines {
		if strings.ContainsAny(ansi.Strip(line), "╭╰│├") && index == 0 {
			t.Fatalf("the frame drew an outer ring on line %d: %q", index, ansi.Strip(line))
		}
	}
	if first := ansi.Strip(lines[0]); strings.HasPrefix(first, "╭") || strings.HasPrefix(first, "│") {
		t.Fatalf("the frame opened with a border: %q", first)
	}
	topRe := regexp.MustCompile(`\x1b\[38;2;(\d+;\d+;\d+)m╭`)
	bottomRe := regexp.MustCompile(`\x1b\[38;2;(\d+;\d+;\d+)m╰`)
	joined := strings.Join(lines, "\n")
	topMatch, bottomMatch := topRe.FindStringSubmatch(joined), bottomRe.FindStringSubmatch(joined)
	if topMatch == nil || bottomMatch == nil {
		t.Fatalf("the modal box lost its gradient corners:\n%s", joined)
	}
	if topMatch[1] == bottomMatch[1] {
		t.Fatalf("the sweep must color the two corners differently: %s", topMatch[1])
	}
	for _, l := range lines {
		if ansi.VisLen(l) != 60 {
			t.Fatalf("gradient SGR disturbed the visible width: %d", ansi.VisLen(l))
		}
	}
}

func TestMixedUnicodeFramesRespectNarrowWidthBoundaries(t *testing.T) {
	rows := []Row{{Text: ansi.Cyan("界👩‍💻 é " + strings.Repeat("x", 40))}}
	modal := layout.Modal{Title: "界 task", Lines: []string{ansi.Yellow("👩‍💻 details é")}}

	for width := 8; width <= 24; width++ {
		popup := layout.Popup{Lines: []string{ansi.Red("界 edge")}, Row: 1, Col: width - 8}
		m := modal
		lines := Build(Options{
			Width: width, Height: 10, Header: ansi.Bold("界👩‍💻 header"),
			Rows: rows, Footer: footerText("界 footer"), Modal: &m, Popup: &popup,
		})
		if len(lines) != 10 {
			t.Fatalf("width %d: height = %d", width, len(lines))
		}
		for index, line := range lines {
			if ansi.VisLen(line) != width {
				t.Fatalf("width %d, line %d: %q (%d cells)", width, index, line, ansi.VisLen(line))
			}
		}
	}
}

// Anti-drift: every list cell the hit map claims must show a glyph that came
// from that absolute row's sentinel text. This catches a future shift of the
// frame's body origin.
func TestHitMapListCellsMatchRenderedRowGlyphs(t *testing.T) {
	const width, height = 60, 15
	rows := make([]Row, 8)
	for n := range rows {
		rows[n] = Row{Text: fmt.Sprintf("ROW%dXXXX", n)}
	}
	lay := layout.New(layout.Options{
		Width: width, Height: height, Footer: footerText("prompt"), Selected: sel(0), Panel: true,
	})
	panelLines := make([]string, lay.BodyHeight)
	for n := range panelLines {
		panelLines[n] = fmt.Sprintf("PANEL%d", n)
	}
	lines := Build(Options{
		Width: width, Height: height, Header: "header", Rows: rows, Selected: sel(0),
		Footer: lay.Footer, Panel: &Panel{Title: "Detail", Lines: panelLines}, Layout: lay,
	})
	m := hitmap.Build(hitmap.Options{Layout: lay, RowCount: len(rows), Panel: true})

	for row := lay.BodyRows().Begin; row < lay.BodyRows().End; row++ {
		for col := lay.ListCols().Begin; col < lay.ListCols().End; col++ {
			hit := m.At(row, col)
			if hit.Zone != hitmap.ZoneListRow || hit.Index >= len(rows) {
				continue
			}
			// Skip the cursor/prefix columns (the first two list cells).
			textCol := col - lay.ListCols().Begin
			if textCol < 2 {
				continue
			}
			text := rows[hit.Index].Text
			if textCol-2 >= len(text) {
				continue
			}
			expected := string(text[textCol-2])
			if expected == " " {
				continue
			}
			cell := ansi.CellSlice(ansi.Strip(lines[row]), col, 1)
			if cell != expected {
				t.Fatalf("list cell (%d,%d) hit row %d but rendered %q, want %q",
					row, col, hit.Index, cell, expected)
			}
		}
	}
}

func runeIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}

func runeIndexLast(s, sub string) int {
	i := strings.LastIndex(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}

func runeIndexFrom(s, sub string, from int) int {
	runes := []rune(s)
	if from >= len(runes) {
		return -1
	}
	i := strings.Index(string(runes[from:]), sub)
	if i < 0 {
		return -1
	}
	return from + len([]rune(string(runes[from:])[:i]))
}
