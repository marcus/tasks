package border

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term/ansi"
)

// Mirrors test/test_border.rb — gradient projection, corner charset,
// coalescing, degradation.

var (
	blue = RGB{0x5a, 0xa2, 0xf7}
	cyan = RGB{0x56, 0xd3, 0xc9}
	grad = &Gradient{Stops: []RGB{blue, cyan}, Angle: 0} // angle 0 → sweep along x
)

func painter(t *testing.T, width, height int, g *Gradient, solid string, steps int) *Painter {
	t.Helper()
	return NewPainter(PainterOptions{
		Width: width, Height: height, Gradient: g, Solid: solid,
		Truecolor: true, Steps: steps,
	})
}

// -- spec parsing ------------------------------------------------------------

func TestParsesTwoStopsAndAngle(t *testing.T) {
	g := ParseGradient("#5aa2f7 #56d3c9 @60")
	if g == nil || len(g.Stops) != 2 || g.Stops[0] != blue || g.Stops[1] != cyan {
		t.Fatalf("stops = %#v", g)
	}
	if g.Angle < 59.999 || g.Angle > 60.001 {
		t.Fatalf("angle = %v", g.Angle)
	}
}

func TestParsesThreeStopsAndDefaultsAngleToZero(t *testing.T) {
	g := ParseGradient("#000000 #808080 #ffffff")
	if g == nil || len(g.Stops) != 3 || g.Angle != 0 {
		t.Fatalf("gradient = %#v", g)
	}
}

func TestRejectsMalformedSpecs(t *testing.T) {
	for _, spec := range []string{
		"",
		"#5aa2f7",            // one stop is not a gradient
		"#5aa2f7 chartreuse", // bad token poisons the whole spec
		"#5aa2f7 #56d3c9 @x", // non-numeric angle
		"#12345 #56d3c9",     // short hex
	} {
		if g := ParseGradient(spec); g != nil {
			t.Fatalf("ParseGradient(%q) = %#v, want nil", spec, g)
		}
	}
}

// -- gradient projection -----------------------------------------------------

func TestEndpointsHitTheTerminalStops(t *testing.T) {
	p := painter(t, 10, 4, grad, "", 0)
	if !strings.Contains(p.Cell(0, 0, "╭"), "\x1b[38;2;90;162;247m") {
		t.Fatalf("left corner = %q", p.Cell(0, 0, "╭"))
	}
	if !strings.Contains(p.Cell(9, 0, "╮"), "\x1b[38;2;86;211;201m") {
		t.Fatalf("right corner = %q", p.Cell(9, 0, "╮"))
	}
}

func TestGradientVariesAcrossTheSpan(t *testing.T) {
	p := painter(t, 20, 4, grad, "", 0)
	left := fg(t, p.Cell(0, 0, "─"))
	mid := fg(t, p.Cell(10, 0, "─"))
	right := fg(t, p.Cell(19, 0, "─"))
	if left == mid || mid == right {
		t.Fatalf("gradient is flat: %v %v %v", left, mid, right)
	}
	// The green channel climbs monotonically from blue (0xa2) toward cyan (0xd3).
	if !(left[1] < mid[1] && mid[1] < right[1]) {
		t.Fatalf("green channel not monotonic: %v %v %v", left, mid, right)
	}
}

func TestAngleChangesTheSweepAxis(t *testing.T) {
	horizontal := painter(t, 10, 10, &Gradient{Stops: []RGB{blue, cyan}, Angle: 0}, "", 0)
	vertical := painter(t, 10, 10, &Gradient{Stops: []RGB{blue, cyan}, Angle: 90}, "", 0)
	if fg(t, horizontal.Cell(0, 0, "│")) != fg(t, horizontal.Cell(0, 9, "│")) {
		t.Fatal("horizontal sweep must be constant down a column")
	}
	if fg(t, vertical.Cell(0, 0, "│")) == fg(t, vertical.Cell(0, 9, "│")) {
		t.Fatal("vertical sweep must vary down a column")
	}
}

// -- coalescing --------------------------------------------------------------

func TestRunCoalescesAdjacentEqualColors(t *testing.T) {
	p := painter(t, 40, 4, grad, "", 4)
	glyphs := append([]string{"╭"}, repeat("─", 38)...)
	glyphs = append(glyphs, "╮")
	row := p.Run(0, 0, glyphs)
	if ansi.VisLen(row) != 40 {
		t.Fatalf("row width = %d", ansi.VisLen(row))
	}
	// 4 quantization steps → at most 4 color openers across the whole run.
	if n := strings.Count(row, "\x1b[38;2;"); n > 4 {
		t.Fatalf("%d color openers, want <= 4", n)
	}
	if !strings.HasSuffix(row, "\x1b[0m") {
		t.Fatalf("run not closed: %q", row)
	}
}

// -- degradation -------------------------------------------------------------

func TestSolidFallbackWhenNoGradient(t *testing.T) {
	p := painter(t, 10, 4, nil, "\x1b[90m", 0)
	if got := p.Cell(0, 1, "│"); got != "\x1b[90m│\x1b[0m" {
		t.Fatalf("cell = %q", got)
	}
}

func TestNoGradientAndNoSolidIsPlain(t *testing.T) {
	p := painter(t, 10, 4, nil, "", 0)
	if got := p.Cell(0, 1, "│"); got != "│" {
		t.Fatalf("cell = %q", got)
	}
	if got := p.Run(0, 0, []string{"╭", "─", "─", "╮"}); got != "╭──╮" {
		t.Fatalf("run = %q", got)
	}
}

func TestNoTruecolorDropsGradientToSolid(t *testing.T) {
	p := NewPainter(PainterOptions{Width: 10, Height: 4, Gradient: grad, Solid: "\x1b[90m", Truecolor: false})
	if got := p.Cell(0, 0, "╭"); got != "\x1b[90m╭\x1b[0m" {
		t.Fatalf("cell = %q", got)
	}
}

// DetectTruecolor is the NO_COLOR contract at the border layer: a non-empty
// NO_COLOR turns the gradient off no matter what the terminal claims.
func TestDetectTruecolorHonorsNoColorFirst(t *testing.T) {
	env := func(vars map[string]string) Env {
		return func(k string) string { return vars[k] }
	}
	cases := []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"NO_COLOR beats COLORTERM", map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, false},
		{"NO_COLOR beats TERM", map[string]string{"NO_COLOR": "anything", "TERM": "xterm-256color"}, false},
		{"empty NO_COLOR is absent", map[string]string{"NO_COLOR": "", "TERM": "xterm-256color"}, true},
		{"COLORTERM truecolor", map[string]string{"COLORTERM": "truecolor"}, true},
		{"COLORTERM 24bit", map[string]string{"COLORTERM": "24BIT"}, true},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, false},
		{"no environment at all", map[string]string{}, false},
	}
	for _, c := range cases {
		if got := DetectTruecolor(env(c.vars)); got != c.want {
			t.Fatalf("%s: DetectTruecolor = %v, want %v", c.name, got, c.want)
		}
	}
	if DetectTruecolor(nil) {
		t.Fatal("a nil environment must not claim truecolor")
	}
}

// -- box ---------------------------------------------------------------------

func TestBoxHasRoundedCornersAndExactWidth(t *testing.T) {
	rows := Box(BoxOptions{InnerLines: []string{"ab", "cd"}, InnerWidth: 2, Gradient: grad, Truecolor: true})
	stripped := make([]string, len(rows))
	for i, r := range rows {
		stripped[i] = ansi.Strip(r)
		if ansi.VisLen(r) != 4 {
			t.Fatalf("row %d width = %d", i, ansi.VisLen(r))
		}
	}
	if stripped[0] != "╭──╮" || stripped[1] != "│ab│" || stripped[len(stripped)-1] != "╰──╯" {
		t.Fatalf("box = %#v", stripped)
	}
}

func TestBoxSquareCornersOptOut(t *testing.T) {
	rows := Box(BoxOptions{InnerLines: []string{"ab"}, InnerWidth: 2, Corners: CornersSquare})
	if ansi.Strip(rows[0]) != "┌──┐" || ansi.Strip(rows[len(rows)-1]) != "└──┘" {
		t.Fatalf("box = %q / %q", rows[0], rows[len(rows)-1])
	}
}

func TestBoxOversizedTitleNeverWidensTheTopRow(t *testing.T) {
	rows := Box(BoxOptions{InnerLines: nil, InnerWidth: 3, Title: "abcdef", TitleLead: 1})
	for i, r := range rows {
		if ansi.VisLen(r) != 5 {
			t.Fatalf("row %d width = %d, want inner_width + 2", i, ansi.VisLen(r))
		}
	}
	if got := ansi.Strip(rows[0]); got != "╭─a…╮" {
		t.Fatalf("top = %q", got)
	}
}

func TestBoxTitleStripKeepsTitleUnpainted(t *testing.T) {
	title := "\x1b[1mHi\x1b[0m"
	rows := Box(BoxOptions{
		InnerLines: []string{"....."}, InnerWidth: 5, Gradient: grad, Truecolor: true,
		Title: title, TitleLead: 1,
	})
	top := rows[0]
	if !strings.Contains(top, title) {
		t.Fatalf("title not passed through: %q", top)
	}
	if got := ansi.Strip(top); got != "╭─Hi──╮" {
		t.Fatalf("top = %q", got)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

var fgRe = regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)m`)

// fg extracts the [r,g,b] of the first truecolor fg SGR in a styled string.
func fg(t *testing.T, s string) RGB {
	t.Helper()
	m := fgRe.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no truecolor fg in %q", s)
	}
	var out RGB
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}
