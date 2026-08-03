package hitmap

import (
	"testing"

	"tasks-go/internal/tui/term/layout"
)

// Mirrors test/test_hit_map.rb. The Ruby tests take their tab spans from
// Tui::Views (the shell owner's package); these use equivalent literal spans,
// because the hit map only cares that a span is a key plus a column range.

func sel(n int) *int { return &n }

func footer(texts ...string) []layout.FooterLine {
	out := make([]layout.FooterLine, 0, len(texts))
	for _, t := range texts {
		out = append(out, layout.Text(t))
	}
	return out
}

type layoutOpts struct {
	width, height int
	footer        []layout.FooterLine
	selected      *int
	panel         bool
	panelMode     layout.PanelMode
	panelOffset   int
}

func lay(mutate func(*layoutOpts)) *layout.Layout {
	o := layoutOpts{width: 80, height: 24, footer: footer("prompt"), selected: sel(0)}
	if mutate != nil {
		mutate(&o)
	}
	return layout.New(layout.Options{
		Width: o.width, Height: o.height, Footer: o.footer, Selected: o.selected,
		Panel: o.panel, PanelMode: o.panelMode, PanelOffset: o.panelOffset,
	})
}

// tabSpans is a stand-in for the shell's real tab strip: three tabs starting at
// the frame's first header column.
var tabSpans = []TabSpan{
	{Key: "agenda", Start: 2, End: 10},
	{Key: "next", Start: 11, End: 17},
	{Key: "inbox", Start: 18, End: 25},
}

func mapFor(l *layout.Layout, mutate func(*Options)) *HitMap {
	o := Options{Layout: l, TabSpans: tabSpans, RowCount: 20}
	if mutate != nil {
		mutate(&o)
	}
	return Build(o)
}

func TestExhaustiveZoneCoverage24x80(t *testing.T) {
	l := lay(nil)
	m := mapFor(l, nil)
	zones := map[Zone]bool{}
	for row := 0; row < l.Height; row++ {
		for col := 0; col < l.Width; col++ {
			zones[m.At(row, col).Zone] = true
		}
	}
	for _, want := range []Zone{ZoneListRow, ZoneTab, ZoneHeader, ZoneFooterRow, ZoneBorder} {
		if !zones[want] {
			t.Fatalf("zone %q never reached", want)
		}
	}
}

func TestExhaustiveZoneCoverageDegenerate6x8(t *testing.T) {
	l := lay(func(o *layoutOpts) { o.width, o.height, o.footer = 8, 6, nil })
	m := mapFor(l, func(o *Options) { o.RowCount = 1 })
	for row := 0; row < l.Height; row++ {
		for col := 0; col < l.Width; col++ {
			if m.At(row, col).Zone == "" {
				t.Fatalf("(%d,%d) resolved to no zone", row, col)
			}
		}
	}
}

func TestOutsideBeyondFrame(t *testing.T) {
	l := lay(nil)
	m := mapFor(l, nil)
	for _, c := range [][2]int{{-1, 0}, {0, -1}, {l.Height, 0}, {0, l.Width}} {
		if got := m.At(c[0], c[1]).Zone; got != ZoneOutside {
			t.Fatalf("At(%d,%d) = %q, want outside", c[0], c[1], got)
		}
	}
}

func TestListRowIncludesViewportOffset(t *testing.T) {
	l := lay(func(o *layoutOpts) { o.selected = sel(20) })
	m := mapFor(l, func(o *Options) { o.RowCount = 40 })
	hit := m.At(l.BodyRows().Begin, l.ListCols().Begin+4)
	if hit.Zone != ZoneListRow || hit.Index != l.ViewportOffset {
		t.Fatalf("hit = %#v, want list_row %d", hit, l.ViewportOffset)
	}
}

func TestPanelZones(t *testing.T) {
	l := lay(func(o *layoutOpts) { o.panel = true })
	m := mapFor(l, func(o *Options) { o.Panel = true })
	bodyRow := l.BodyRows().Begin
	if got := m.At(bodyRow, l.PanelDividerCol()).Zone; got != ZonePanelDivider {
		t.Fatalf("divider = %q", got)
	}
	if got := m.At(bodyRow, l.PanelCols().Begin).Zone; got != ZonePanel {
		t.Fatalf("panel = %q", got)
	}
	if got := m.At(bodyRow, l.ListCols().Begin+2).Zone; got != ZoneListRow {
		t.Fatalf("list = %q", got)
	}
}

func TestPanelModesAndOffset(t *testing.T) {
	for _, mode := range layout.PanelModes {
		l := lay(func(o *layoutOpts) {
			o.panel, o.panelMode, o.panelOffset = true, mode, 2
		})
		m := mapFor(l, func(o *Options) { o.Panel = true })
		if got := m.At(l.BodyRows().Begin, l.PanelCols().Begin).Zone; got != ZonePanel {
			t.Fatalf("%s: panel col = %q", mode, got)
		}
	}
}

func TestModalTakesPrecedenceOverList(t *testing.T) {
	l := lay(nil)
	placed := l.PlaceModal(layout.Modal{Title: "Help", Lines: []string{"a", "b", "c"}, Width: 30})
	m := mapFor(l, func(o *Options) { o.Modal = &placed })
	originRow, originCol := l.BodyOrigin()
	hit := m.At(originRow+placed.Row+1, originCol+placed.Col+2)
	if hit.Zone != ZoneModalRow {
		t.Fatalf("hit = %#v", hit)
	}
}

func TestPopupTakesPrecedenceOverList(t *testing.T) {
	l := lay(nil)
	popup := layout.Popup{Lines: []string{"aaaa", "bbbb"}, Row: 0, Col: 0}
	m := mapFor(l, func(o *Options) { o.Popup = &popup })
	originRow, originCol := l.BodyOrigin()
	hit := m.At(originRow, originCol)
	if hit.Zone != ZonePopupRow || hit.Index != 0 {
		t.Fatalf("hit = %#v", hit)
	}
}

func TestPopupRaggedRightEdgeIsChrome(t *testing.T) {
	l := lay(nil)
	popup := layout.Popup{Lines: []string{"aaaaaaaa", "bb"}, Row: 0, Col: 0}
	m := mapFor(l, func(o *Options) { o.Popup = &popup })
	originRow, originCol := l.BodyOrigin()
	if got := m.At(originRow+1, originCol+5).Zone; got != ZoneBorder {
		t.Fatalf("short popup line past its text = %q, want border", got)
	}
}

func TestFooterRoles(t *testing.T) {
	l := lay(func(o *layoutOpts) {
		o.footer = []layout.FooterLine{
			layout.Text(" result #1"), layout.Text("   line"), layout.Rule(), layout.Text(" ❯ ask"),
		}
	})
	m := mapFor(l, func(o *Options) {
		o.FooterRoles = []FooterRole{RoleResponse, RoleResponse, RoleChrome, RolePrompt}
	})
	fr := l.FooterRows().Begin
	if got := m.At(fr, 2).Footer.Role; got != RoleResponse {
		t.Fatalf("first footer role = %q", got)
	}
	if got := m.At(fr+3, 2).Footer.Role; got != RolePrompt {
		t.Fatalf("last footer role = %q", got)
	}
	if got := m.At(fr, 0).Zone; got != ZoneBorder {
		t.Fatalf("footer edge column = %q, want border", got)
	}
}

func TestUndeclaredFooterRoleDefaultsToChrome(t *testing.T) {
	l := lay(func(o *layoutOpts) { o.footer = footer("one", "two") })
	m := mapFor(l, nil)
	if got := m.At(l.FooterRows().Begin, 2).Footer.Role; got != RoleChrome {
		t.Fatalf("role = %q, want chrome", got)
	}
}

func TestTabSpansResolveToTheirKeys(t *testing.T) {
	l := lay(nil)
	m := mapFor(l, nil)
	for _, span := range tabSpans {
		if hit := m.At(l.HeaderRow(), span.Start); hit.Zone != ZoneTab || hit.Tab != span.Key {
			t.Fatalf("start of %s = %#v", span.Key, hit)
		}
		if hit := m.At(l.HeaderRow(), span.End-1); hit.Zone != ZoneTab || hit.Tab != span.Key {
			t.Fatalf("end of %s = %#v", span.Key, hit)
		}
	}
	// A header cell past the last tab is plain header, not a tab.
	if got := m.At(l.HeaderRow(), tabSpans[len(tabSpans)-1].End).Zone; got != ZoneHeader {
		t.Fatalf("past the last tab = %q", got)
	}
}

func TestCollapseMarkerZone(t *testing.T) {
	l := lay(nil)
	m := mapFor(l, func(o *Options) {
		o.RowCount = 5
		o.MarkerSpans = map[int]MarkerSpan{0: {Start: 0, End: 2}}
	})
	// list col + 2 (cursor prefix) + 0 (marker start)
	hit := m.At(l.BodyRows().Begin, l.ListCols().Begin+2)
	if hit.Zone != ZoneCollapseMarker || hit.Index != 0 {
		t.Fatalf("hit = %#v", hit)
	}
	// Just past the marker span is an ordinary row hit.
	if hit := m.At(l.BodyRows().Begin, l.ListCols().Begin+4); hit.Zone != ZoneListRow {
		t.Fatalf("past the marker = %#v", hit)
	}
}

func TestBodyRowsPastTheRowCountAreChrome(t *testing.T) {
	l := lay(nil)
	m := mapFor(l, func(o *Options) { o.RowCount = 1 })
	if got := m.At(l.BodyRows().Begin+1, l.ListCols().Begin+2).Zone; got != ZoneBorder {
		t.Fatalf("empty body row = %q, want border", got)
	}
}
