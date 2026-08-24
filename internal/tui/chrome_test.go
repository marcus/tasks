package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/tui/term"
	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/border"
)

// The scrollbar math is a port of sidecar's internal/scroll/thumb.go (branch
// scroll), and these tests are the port of its thumb_test.go: the guarantees
// they pin — legacy-arithmetic fidelity, monotonicity, clamping, round-trip
// drift bounds, re-anchoring — are what a future drag gesture stands on.

// legacyThumbMath is sidecar's pre-shared arithmetic every scrollbar surface
// drew with before ThumbLocFor; ThumbLocFor must reproduce it exactly.
func legacyThumbMath(totalItems, scrollOffset, visibleItems, trackHeight int) (size, pos int, has bool) {
	if trackHeight < 1 || totalItems <= visibleItems {
		return 0, 0, false
	}
	size = (visibleItems * trackHeight) / totalItems
	if size < 1 {
		size = 1
	}
	if size > trackHeight {
		size = trackHeight
	}
	maxOffset := totalItems - visibleItems
	if maxOffset < 1 {
		maxOffset = 1
	}
	pos = (scrollOffset * (trackHeight - size)) / maxOffset
	if pos < 0 {
		pos = 0
	}
	if pos > trackHeight-size {
		pos = trackHeight - size
	}
	return size, pos, true
}

func TestThumbLocForMatchesLegacyMath(t *testing.T) {
	for total := range 61 {
		for visible := range 31 {
			for track := range 25 {
				wantSize, wantPos, wantHas := legacyThumbMath(total, 0, visible, track)
				got := ThumbLocFor(total, 0, visible, track)
				if got.Size != wantSize || got.Pos != wantPos || got.Has != wantHas {
					t.Fatalf("ThumbLocFor(%d,%d,%d,%d) = %+v, want (%d,%d,%v)",
						total, 0, visible, track, got, wantSize, wantPos, wantHas)
				}
			}
		}
	}
}

func TestThumbLocForTracksOffset(t *testing.T) {
	loc := ThumbLocFor(100, 0, 10, 10)
	if !loc.Has || loc.Pos != 0 || loc.Size != 1 {
		t.Fatalf("offset 0 = %+v, want thumb at top", loc)
	}
	mid := ThumbLocFor(100, 45, 10, 10)
	if mid.Pos != 4 { // 45*9/90
		t.Fatalf("offset 45 pos = %d, want 4", mid.Pos)
	}
	bottom := ThumbLocFor(100, 90, 10, 10)
	if bottom.Pos != 9 { // clamped to track-size
		t.Fatalf("offset 90 pos = %d, want 9", bottom.Pos)
	}
	negative := ThumbLocFor(100, -5, 10, 10)
	if negative.Pos != 0 {
		t.Fatalf("negative offset pos = %d, want 0", negative.Pos)
	}
}

func TestThumbLocForMinSize(t *testing.T) {
	loc := ThumbLocFor(10000, 5000, 1, 10)
	if !loc.Has || loc.Size != 1 {
		t.Fatalf("min-size thumb = %+v, want Has with Size 1", loc)
	}
}

func TestThumbLocForNoThumb(t *testing.T) {
	for _, tc := range []struct{ total, visible int }{
		{5, 10}, {10, 10}, {0, 0}, {0, 5},
	} {
		if loc := ThumbLocFor(tc.total, 3, tc.visible, 8); loc.Has {
			t.Errorf("total=%d visible=%d: reported thumb, want none", tc.total, tc.visible)
		}
	}
	if loc := ThumbLocFor(100, 0, 10, 0); loc.Has {
		t.Error("zero-height track: reported thumb, want none")
	}
}

func TestOffsetAtRowMonotonicAndClamped(t *testing.T) {
	for _, tc := range []struct{ total, visible, track int }{
		{100, 10, 10}, {10000, 1, 40}, {21, 20, 3}, {50, 7, 13}, {11, 10, 1},
	} {
		prev := -1
		for row := -5; row <= tc.track+5; row++ {
			got := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			if got < 0 || got > tc.total-tc.visible {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow(%d) = %d, out of range",
					tc.total, tc.visible, tc.track, row, got)
			}
			if got < prev {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow not monotonic at row %d (%d < %d)",
					tc.total, tc.visible, tc.track, row, got, prev)
			}
			prev = got
		}
	}
}

func TestOffsetAtRowEndpoints(t *testing.T) {
	total, visible, track := 100, 10, 91
	if got := OffsetAtRow(total, visible, track, -1); got != 0 {
		t.Errorf("row below track = %d, want 0", got)
	}
	if got := OffsetAtRow(total, visible, track, 0); got != 0 {
		t.Errorf("top row = %d, want 0", got)
	}
	if got := OffsetAtRow(total, visible, track, track-1); got != 90 {
		t.Errorf("bottom row = %d, want max offset 90", got)
	}
	if got := OffsetAtRow(total, visible, track, track+10); got != 90 {
		t.Errorf("row past track = %d, want clamped to 90", got)
	}
}

func TestOffsetAtRowNoThumbReturnsZero(t *testing.T) {
	if got := OffsetAtRow(5, 10, 8, 4); got != 0 {
		t.Errorf("fits-without-thumb: OffsetAtRow = %d, want 0", got)
	}
	if got := OffsetAtRow(100, 10, 0, 4); got != 0 {
		t.Errorf("no track: OffsetAtRow = %d, want 0", got)
	}
}

func TestRoundTripStability(t *testing.T) {
	for _, tc := range []struct{ total, visible, track int }{
		{100, 10, 10}, {10000, 1, 40}, {50, 7, 13}, {120, 40, 25}, {11, 10, 1},
		{8, 6, 15}, // size 11, travel 4 > maxOffset 2: collapsed anchoring
	} {
		maxOffset := tc.total - tc.visible
		travel := tc.track - ThumbLocFor(tc.total, 0, tc.visible, tc.track).Size
		if travel < 1 {
			continue
		}
		band := maxOffset/travel + 1 // widest run of offsets sharing one row
		for offset := range maxOffset + 1 {
			row := RowForOffset(tc.total, tc.visible, tc.track, offset)
			back := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			drift := offset - back
			if drift < 0 || drift > band {
				t.Fatalf("total=%d visible=%d track=%d: round trip %d -> row %d -> %d drifts by %d",
					tc.total, tc.visible, tc.track, offset, row, back, drift)
			}
		}
		// Anchoring: rendering the offset a row maps to must place the thumb
		// back on that row without ever snapping above it. Exact re-anchor
		// holds while travel <= maxOffset; in the collapsed regime the
		// documented guarantee weakens to at-or-below, with monotonicity,
		// clamping, and endpoints still holding.
		exactAnchor := travel <= maxOffset
		prev := -1
		for row := range travel {
			back := OffsetAtRow(tc.total, tc.visible, tc.track, row)
			if back < prev {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow not monotonic at row %d (%d < %d)",
					tc.total, tc.visible, tc.track, row, back, prev)
			}
			if back < 0 || back > maxOffset {
				t.Fatalf("total=%d visible=%d track=%d: OffsetAtRow(%d) = %d, out of [0,%d]",
					tc.total, tc.visible, tc.track, row, back, maxOffset)
			}
			reanchored := RowForOffset(tc.total, tc.visible, tc.track, back)
			if exactAnchor && reanchored != row {
				t.Fatalf("total=%d visible=%d track=%d: row %d maps to offset %d which renders at row %d",
					tc.total, tc.visible, tc.track, row, back, reanchored)
			}
			if !exactAnchor && reanchored < row {
				t.Fatalf("total=%d visible=%d track=%d: collapsed regime snapped above: row %d -> offset %d -> row %d",
					tc.total, tc.visible, tc.track, row, back, reanchored)
			}
			prev = back
		}
		if got := OffsetAtRow(tc.total, tc.visible, tc.track, 0); got != 0 {
			t.Errorf("total=%d visible=%d track=%d: top row = %d, want 0",
				tc.total, tc.visible, tc.track, got)
		}
		if got := OffsetAtRow(tc.total, tc.visible, tc.track, travel); got != maxOffset {
			t.Errorf("total=%d visible=%d track=%d: bottom row = %d, want %d",
				tc.total, tc.visible, tc.track, got, maxOffset)
		}
	}
}

func TestRoundTripExactWhenTrackResolvesEveryOffset(t *testing.T) {
	// With TrackHeight == TotalItems the thumb is VisibleItems tall and
	// travel equals maxOffset, so one row per offset makes the pair exact
	// inverses.
	total, visible, track := 100, 10, 100
	for offset := range total - visible + 1 {
		row := RowForOffset(total, visible, track, offset)
		if got := OffsetAtRow(total, visible, track, row); got != offset {
			t.Fatalf("offset %d: exact inverse broken, got %d", offset, got)
		}
	}
}

func TestClampScrollBounds(t *testing.T) {
	cases := []struct{ offset, visible, total, want int }{
		{-3, 5, 20, 0},
		{99, 5, 20, 15},
		{7, 5, 20, 7},
		{7, 5, 5, 0}, // nothing to scroll
		{0, 5, 0, 0}, // empty content
		{-9, 5, 3, 0},
	}
	for _, c := range cases {
		if got := ClampScroll(c.offset, c.visible, c.total); got != c.want {
			t.Fatalf("ClampScroll(%d,%d,%d) = %d, want %d", c.offset, c.visible, c.total, got, c.want)
		}
	}
}

func TestScrollbarCellPaintsThumbAndTrack(t *testing.T) {
	styler := PlainStyler{}
	thumb := ScrollThumb{Pos: 1, Size: 2, Has: true}
	if got := ScrollbarCell(styler, 1, thumb); got != scrollbarThumbGlyph {
		t.Fatalf("inside thumb painted %q", got)
	}
	if got := ScrollbarCell(styler, 0, thumb); got != scrollbarTrackGlyph {
		t.Fatalf("outside thumb painted %q", got)
	}
	column := PaintScrollbar(styler, 4, thumb)
	if column == "" || len([]rune(column)) != 4 {
		t.Fatalf("PaintScrollbar = %q, want 4 cells", column)
	}
	if PaintScrollbar(styler, 4, ScrollThumb{}) != "" {
		t.Fatal("no-thumb track must paint nothing; callers hide the column")
	}
}

// cellStyler measures like the real styler does — escape sequences count zero,
// wide runes count two — while painting nothing. It is what pins span geometry
// against text PlainStyler would miscount.
type cellStyler struct{ PlainStyler }

func (cellStyler) Width(text string) int { return ansi.VisLen(text) }

func TestModalButtonVariantsLabelsWidthsAndSpans(t *testing.T) {
	styler := PlainStyler{}
	submit := ModalButton{ID: "submit", Label: "Delegate", KeyLabel: "enter", Variant: ButtonPrimary}
	cancel := ModalButton{ID: "cancel", Label: "Cancel", Variant: ButtonMuted}
	release := ModalButton{ID: "release", Label: "Release!", KeyLabel: "ctrl-r", Variant: ButtonDangerArmed}

	if got, want := submit.plain(), " enter Delegate "; got != want {
		t.Fatalf("submit plain = %q, want %q", got, want)
	}
	if got, want := cancel.plain(), "  Cancel  "; got != want {
		t.Fatalf("cancel plain = %q, want %q", got, want)
	}
	if submit.Width(styler) != len([]rune(submit.plain())) {
		t.Fatal("plain width disagrees with Width for ASCII labels")
	}

	text, spans := PaintButtonRow(styler, []ModalButton{submit, cancel, release})
	if !strings.Contains(text, " enter Delegate ") || !strings.Contains(text, " ctrl-r Release! ") {
		t.Fatalf("button row text = %q", text)
	}
	if len(spans) != 3 {
		t.Fatalf("%d spans, want 3", len(spans))
	}
	// Spans index into the returned line directly: each span's slice IS its
	// button's text, so a click can resolve by geometry without trusting
	// painted bytes.
	runes := []rune(text)
	column := 0
	for i, span := range spans {
		if span.Begin < column || span.End <= span.Begin {
			t.Fatalf("span %d = [%d,%d) overlaps or inverts at column %d", i, span.Begin, span.End, column)
		}
		column = span.End
	}
	for i, b := range []ModalButton{submit, cancel, release} {
		if got := string(runes[spans[i].Begin:spans[i].End]); got != b.plain() {
			t.Fatalf("span %d slices %q, want %q", i, got, b.plain())
		}
	}
	if spans[0].ID != "submit" || spans[1].ID != "cancel" || spans[2].ID != "release" {
		t.Fatalf("span ids = %v", spans)
	}

	// Wide runes and painted text: the span arithmetic runs in CELLS, not
	// runes or bytes, or a CJK label shifts every later button.
	wide := ModalButton{ID: "wide", Label: "削除", KeyLabel: "ctrl-x", Variant: ButtonDanger}
	wideText, wideSpans := PaintButtonRow(cellStyler{}, []ModalButton{wide, cancel})
	if got := ansi.VisLen(wideText); got != cancel.Width(cellStyler{})+wide.Width(cellStyler{})+1 {
		t.Fatalf("wide row paints %d cells, want %d", got, cancel.Width(cellStyler{})+wide.Width(cellStyler{})+1)
	}
	first, second := wideSpans[0], wideSpans[1]
	if second.Begin != first.End+1 {
		t.Fatalf("gap between wide buttons = [%d,%d)+1 vs [%d,%d)", first.Begin, first.End, second.Begin, second.End)
	}
}

func TestButtonVariantSlotsAreDistinct(t *testing.T) {
	variants := map[ButtonVariant]string{
		ButtonPrimary:     "",
		ButtonDanger:      "",
		ButtonDangerArmed: "",
		ButtonMuted:       "",
	}
	seen := map[string]bool{}
	for v := range variants {
		slot := v.slot()
		if slot == "" {
			t.Fatalf("variant %d has no slot", v)
		}
		if seen[slot] {
			t.Fatalf("slot %q shared by multiple variants", slot)
		}
		seen[slot] = true
	}
}

func TestKeyChipsFormatAndEmptyList(t *testing.T) {
	styler := PlainStyler{}
	if got := PaintKeyChips(styler, nil); got != "" {
		t.Fatalf("nil chips = %q, want empty", got)
	}
	got := PaintKeyChips(styler, []KeyChip{{"tab", "next"}, {"ctrl-s", "newline"}})
	want := "[tab] next   [ctrl-s] newline"
	if got != want {
		t.Fatalf("chips = %q, want %q", got, want)
	}
}

func TestStatusLineLevels(t *testing.T) {
	styler := PlainStyler{}
	if got := PaintStatusLine(styler, StatusNone, "ignored"); got != "" {
		t.Fatalf("StatusNone painted %q", got)
	}
	if got := PaintStatusLine(styler, StatusError, ""); got != "" {
		t.Fatalf("empty error painted %q", got)
	}
	if got, want := PaintStatusLine(styler, StatusError, "boom"), "  ! boom"; got != want {
		t.Fatalf("error line = %q, want %q", got, want)
	}
	if got, want := PaintStatusLine(styler, StatusWarning, "armed"), "  ! armed"; got != want {
		t.Fatalf("warning line = %q, want %q", got, want)
	}
	// Inline newlines would break the one-fixed-row contract.
	if got := PaintStatusLine(styler, StatusError, "two\nlines"); strings.Contains(got, "\n") {
		t.Fatalf("status line leaked a newline: %q", got)
	}
}

func TestChromeVariantsBorderSlotsAndNeutralPassthrough(t *testing.T) {
	styler := PlainStyler{}
	if got := PaintChrome(styler, BoxNeutral, border.Round.TL); got != border.Round.TL {
		t.Fatalf("neutral chrome altered the piece: %q", got)
	}
	if BoxAccent.BorderSlot() == "" || BoxWarning.BorderSlot() == "" || BoxDanger.BorderSlot() == "" {
		t.Fatal("accent/warning/danger variants must name border slots")
	}
	if BoxNeutral.BorderSlot() != "" {
		t.Fatal("neutral must stay unpainted so existing modals keep their look")
	}
	if got := ChromeHorizontal(styler, BoxNeutral, 3); got != strings.Repeat(border.Round.H, 3) {
		t.Fatalf("neutral horizontal = %q", got)
	}
	if got := ChromeHorizontal(styler, BoxAccent, 0); got != "" {
		t.Fatalf("zero-width horizontal = %q", got)
	}
}

func TestApplyModalBackdropCompositesOnlyTheMargins(t *testing.T) {
	styler := PlainStyler{}
	frame := "left middle right"
	left, right := ApplyModalBackdrop(styler, frame[:4], frame[12:])
	if left != "left" || right != "right" {
		t.Fatalf("backdrop margins = (%q, %q), want the two pieces separately", left, right)
	}
	// The composite seam receives already-sliced pieces; it must not re-cut.
	if l, r := ApplyModalBackdrop(styler, "", ""); l != "" || r != "" {
		t.Fatal("empty margins should stay empty")
	}
}

// backdropOffStyler is a user who set modal_backdrop = none: the flat look.
type backdropOffStyler struct{ cellStyler }

func (backdropOffStyler) Composite(_, text string) string { return "" }

func TestApplyModalBackdropRespectsASlotTurnedOff(t *testing.T) {
	styler := backdropOffStyler{}
	left, right := ApplyModalBackdrop(styler, "abc", "def")
	if left != "" || right != "" {
		t.Fatalf("a none slot must composite to nothing, got (%q, %q)", left, right)
	}
}

// The backdrop dims the frame cells BESIDE a backdropped box — never the box
// itself, never rows above or below it — and a caller that did not ask for a
// backdrop keeps the flat frame.
func TestCompositeDimsTheMarginsOfABackdroppedBox(t *testing.T) {
	model := &Model{styler: term.NewStyler("default", nil)}
	frame := []string{"aaaaaaaaaa", "aaaaaaaaaa", "aaaaaaaaaa"}
	box := &OverlayBox{Lines: []string{"[xx]"}, Row: 1, Col: 3, FocusedContentRow: -1}

	plain := model.composite(frame, box)
	if plain[1] != "aaa[xx]aaa" {
		t.Fatalf("plain splice = %q", plain[1])
	}

	box.Backdrop = true
	dimmed := model.composite(frame, box)
	if dimmed[1] == plain[1] {
		t.Fatal("the backdrop changed nothing beside the box")
	}
	if strings.Contains(dimmed[1], "[xx]") == false || !strings.Contains(ansi.Strip(dimmed[1]), "[xx]") {
		t.Fatalf("the box itself was altered by the backdrop: %q", dimmed[1])
	}
	// Only the rows the box spans are dimmed.
	if dimmed[0] != frame[0] || dimmed[2] != frame[2] {
		t.Fatal("rows outside the box were dimmed")
	}
	// The margins carry the modal_backdrop slot's SGR.
	backdrop := model.styler.(*term.Styler).Theme().SGR("modal_backdrop")
	if backdrop == "" {
		t.Skip("this theme does not paint modal_backdrop")
	}
	if !strings.Contains(dimmed[1], backdrop) {
		t.Fatalf("the margins do not carry the backdrop SGR %q: %q", backdrop, dimmed[1])
	}
}
