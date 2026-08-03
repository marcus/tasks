// Package border is the single owner of box-drawing chrome: rounded corners
// plus an angled truecolor gradient swept across a container's border cells.
// Frames, modals, and palettes all route their boxes through here so every
// container gets the same look, and so the box-drawing glyphs live in exactly
// one place.
//
// A Gradient is a stop list plus an angle in degrees. For each border cell the
// cell is projected onto the angle's direction vector (with a vertical aspect
// correction, since terminal cells are ~2x taller than wide), the projection is
// normalized across the box's four corners to t in [0,1], and the stops are
// interpolated at t. When no gradient is active (no truecolor, a NO_COLOR/mono
// theme, or none configured) the border falls back to a single solid SGR —
// same call sites, no branching at the caller.
//
// Go port of Ruby's lib/tui/border.rb. Truecolor availability is a Painter
// option rather than Ruby's module-level flag.
package border

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"tasks-go/internal/tui/term/ansi"
)

// Charset is one box-drawing glyph set.
type Charset struct {
	TL, TR, BL, BR, H, V, ML, MR string
}

// Round has rounded outer corners; Square keeps them square. The divider tees
// (ML/MR) are shared by both, since the header/footer rules keep their square
// junctions.
var (
	Round  = Charset{TL: "╭", TR: "╮", BL: "╰", BR: "╯", H: "─", V: "│", ML: "├", MR: "┤"}
	Square = Charset{TL: "┌", TR: "┐", BL: "└", BR: "┘", H: "─", V: "│", ML: "├", MR: "┤"}
)

// Corners selects a charset.
type Corners int

const (
	CornersRound Corners = iota
	CornersSquare
)

// Chars returns the charset for the requested corner style.
func Chars(c Corners) Charset {
	if c == CornersSquare {
		return Square
	}
	return Round
}

const (
	// DefaultSteps is the number of distinct colors quantized along the ring.
	// Quantizing both caps the number of SGR sequences emitted and lets
	// adjacent equal-color cells coalesce into one run, so a ~200-cell
	// perimeter costs a couple dozen escapes, not 200.
	DefaultSteps = 24
	// DefaultAspect scales y so a configured angle reads on screen as that
	// angle rather than vertically skewed.
	DefaultAspect = 2.0
)

const reset = "\x1b[0m"

// RGB is one gradient stop.
type RGB [3]int

// Gradient is a parsed border_gradient spec.
type Gradient struct {
	Stops []RGB
	Angle float64
}

var (
	hexStop  = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	angleNum = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)
)

// ParseGradient parses a border_gradient spec: two or more hex stops then an
// "@<angle>", e.g. "#7aa2f7 #bb9af7 @60". It returns nil if the spec is
// malformed, so a bad config value degrades to the solid border rather than
// failing.
func ParseGradient(spec string) *Gradient {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(spec)))
	if len(tokens) == 0 {
		return nil
	}
	g := &Gradient{}
	for _, tok := range tokens {
		switch {
		case strings.HasPrefix(tok, "@"):
			deg := tok[1:]
			if !angleNum.MatchString(deg) {
				return nil
			}
			f, err := strconv.ParseFloat(deg, 64)
			if err != nil {
				return nil
			}
			g.Angle = f
		case hexStop.MatchString(tok):
			r, _ := strconv.ParseInt(tok[1:3], 16, 32)
			gr, _ := strconv.ParseInt(tok[3:5], 16, 32)
			b, _ := strconv.ParseInt(tok[5:7], 16, 32)
			g.Stops = append(g.Stops, RGB{int(r), int(gr), int(b)})
		default:
			return nil
		}
	}
	if len(g.Stops) < 2 {
		return nil
	}
	return g
}

// Env looks up an environment variable; nil means "no environment".
type Env func(string) string

// DetectTruecolor is the Ruby truecolor probe: a non-empty NO_COLOR disables
// color entirely, COLORTERM naming truecolor/24bit enables it, and otherwise
// any TERM that is neither empty nor "dumb" is assumed capable.
func DetectTruecolor(env Env) bool {
	get := func(k string) string {
		if env == nil {
			return ""
		}
		return env(k)
	}
	if get("NO_COLOR") != "" {
		return false
	}
	colorterm := strings.ToLower(get("COLORTERM"))
	if strings.Contains(colorterm, "truecolor") || strings.Contains(colorterm, "24bit") {
		return true
	}
	term := get("TERM")
	return term != "" && term != "dumb"
}

// PainterOptions configures one box-sized colorizer.
type PainterOptions struct {
	Width, Height int
	Gradient      *Gradient
	// Solid is an SGR opener (e.g. "\e[90m") or "" used when the gradient is
	// inactive.
	Solid string
	// Truecolor gates the gradient. Absent truecolor, gradients drop to Solid.
	Truecolor bool
	Steps     int
	Aspect    float64
}

// Painter precomputes the per-cell colorizer for one box size. Cell and Run
// return glyphs wrapped in the appropriate SGR; the caller maps only border
// glyphs through it, leaving interior content untouched.
type Painter struct {
	solid   string
	palette []string
	cos     float64
	sin     float64
	aspect  float64
	pmin    float64
	rng     float64
}

// NewPainter builds a Painter for a box of Width x Height cells.
func NewPainter(opts PainterOptions) *Painter {
	p := &Painter{solid: opts.Solid}
	if opts.Gradient == nil || !opts.Truecolor {
		return p
	}
	steps := opts.Steps
	if steps == 0 {
		steps = DefaultSteps
	}
	if steps < 2 {
		steps = 2
	}
	aspect := opts.Aspect
	if aspect == 0 {
		aspect = DefaultAspect
	}

	rad := opts.Gradient.Angle * math.Pi / 180.0
	p.cos = math.Cos(rad)
	p.sin = math.Sin(rad)
	p.aspect = aspect
	corners := [4][2]int{{0, 0}, {opts.Width - 1, 0}, {0, opts.Height - 1}, {opts.Width - 1, opts.Height - 1}}
	pmin, pmax := math.Inf(1), math.Inf(-1)
	for _, c := range corners {
		proj := float64(c[0])*p.cos + float64(c[1])*aspect*p.sin
		pmin = math.Min(pmin, proj)
		pmax = math.Max(pmax, proj)
	}
	p.pmin = pmin
	p.rng = pmax - pmin

	p.palette = make([]string, steps)
	for i := 0; i < steps; i++ {
		c := lerp(opts.Gradient.Stops, float64(i)/float64(steps-1))
		p.palette[i] = "\x1b[38;2;" + strconv.Itoa(c[0]) + ";" + strconv.Itoa(c[1]) + ";" + strconv.Itoa(c[2]) + "m"
	}
	return p
}

// SGRAt is the opening sequence for the cell at (x, y): a truecolor foreground
// in gradient mode, else the solid fallback (possibly "").
func (p *Painter) SGRAt(x, y int) string {
	if p.palette == nil {
		return p.solid
	}
	proj := float64(x)*p.cos + float64(y)*p.aspect*p.sin
	t := 0.0
	if p.rng != 0 {
		t = (proj - p.pmin) / p.rng
	}
	index := int(math.Round(t * float64(len(p.palette)-1)))
	if index < 0 {
		index = 0
	}
	if index > len(p.palette)-1 {
		index = len(p.palette) - 1
	}
	return p.palette[index]
}

// Cell renders a single colored glyph, self-closing.
func (p *Painter) Cell(x, y int, glyph string) string {
	seq := p.SGRAt(x, y)
	if seq == "" {
		return glyph
	}
	return seq + glyph + reset
}

// Run renders consecutive cells on row y starting at column x0, coalescing
// equal adjacent colors into one SGR. It closes with a single reset iff any
// color was emitted.
func (p *Painter) Run(y, x0 int, glyphs []string) string {
	var out strings.Builder
	last := ""
	haveLast := false
	styled := false
	for i, glyph := range glyphs {
		seq := p.SGRAt(x0+i, y)
		if !haveLast || seq != last {
			out.WriteString(seq)
			last = seq
			haveLast = true
			if seq != "" {
				styled = true
			}
		}
		out.WriteString(glyph)
	}
	if styled {
		out.WriteString(reset)
	}
	return out.String()
}

// lerp interpolates the stop list at t in [0,1] in plain sRGB. Predictable and
// fine for the adjacent-hue sweeps borders use; isolated here so the color
// space can be upgraded without touching callers.
func lerp(stops []RGB, t float64) RGB {
	if len(stops) == 1 {
		return stops[0]
	}
	seg := t * float64(len(stops)-1)
	if seg < 0 {
		seg = 0
	}
	if seg > float64(len(stops)-1) {
		seg = float64(len(stops) - 1)
	}
	i := int(math.Floor(seg))
	if i > len(stops)-2 {
		i = len(stops) - 2
	}
	if i < 0 {
		i = 0
	}
	f := seg - float64(i)
	a, b := stops[i], stops[i+1]
	var out RGB
	for k := 0; k < 3; k++ {
		out[k] = int(math.Round(float64(a[k]) + (float64(b[k])-float64(a[k]))*f))
	}
	return out
}

// BoxOptions describes one complete box.
type BoxOptions struct {
	// InnerLines are each exactly InnerWidth visible cells (any inner margin is
	// the caller's); the vertical edges are added here.
	InnerLines []string
	InnerWidth int
	Gradient   *Gradient
	Solid      string
	Truecolor  bool
	// Title is already styled by the caller and sits after TitleLead leading
	// dashes; the border glyphs around it are painted while the title text
	// passes through untouched. An empty title draws a plain top rule.
	Title     string
	TitleLead int
	Corners   Corners
	Steps     int
	Aspect    float64
}

// Box renders a complete box. Returned rows are each InnerWidth + 2 cells wide.
func Box(opts BoxOptions) []string {
	width := opts.InnerWidth + 2
	height := len(opts.InnerLines) + 2
	c := Chars(opts.Corners)
	p := NewPainter(PainterOptions{
		Width: width, Height: height, Gradient: opts.Gradient, Solid: opts.Solid,
		Truecolor: opts.Truecolor, Steps: opts.Steps, Aspect: opts.Aspect,
	})

	rows := []string{topRow(p, c, opts.InnerWidth, opts.Title, opts.TitleLead)}
	for i, line := range opts.InnerLines {
		y := i + 1
		rows = append(rows, p.Cell(0, y, c.V)+line+p.Cell(width-1, y, c.V))
	}
	rows = append(rows, p.Run(height-1, 0, glyphRow(c.BL, c.H, opts.InnerWidth, c.BR)))
	return rows
}

func glyphRow(left, fill string, n int, right string) []string {
	out := make([]string, 0, n+2)
	out = append(out, left)
	for i := 0; i < n; i++ {
		out = append(out, fill)
	}
	return append(out, right)
}

func topRow(p *Painter, c Charset, innerWidth int, title string, titleLead int) string {
	if title == "" {
		return p.Run(0, 0, glyphRow(c.TL, c.H, innerWidth, c.TR))
	}
	// Self-enforce the fit: clamp the lead and truncate the (styled) title so
	// lead + title never exceeds innerWidth. Callers already pre-truncate, so
	// this is a no-op for them, but it keeps the top row the same width as the
	// rest of the box for any caller of this shared primitive.
	lead := titleLead
	if lead < 0 {
		lead = 0
	}
	if lead > innerWidth {
		lead = innerWidth
	}
	title = ansi.VTrunc(title, innerWidth-lead)
	tw := ansi.VisLen(title)
	fill := innerWidth - lead - tw

	head := make([]string, 0, lead+1)
	head = append(head, c.TL)
	for i := 0; i < lead; i++ {
		head = append(head, c.H)
	}
	tail := make([]string, 0, fill+1)
	for i := 0; i < fill; i++ {
		tail = append(tail, c.H)
	}
	tail = append(tail, c.TR)
	return p.Run(0, 0, head) + title + p.Run(0, 1+lead+tw, tail)
}
