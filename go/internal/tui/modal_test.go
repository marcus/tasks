package tui

import (
	"fmt"
	"strings"
	"testing"

	"tasks-go/internal/tui/term"
	"tasks-go/internal/tui/term/ansi"
)

// modalBodyH is Ruby's BODY_H: borders take 2 rows, leaving 10 inner rows, and
// the status line takes one more when shown.
const modalBodyH = 12

func numberedLines(count int) []string {
	out := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		out = append(out, fmt.Sprintf("line %d", index))
	}
	return out
}

func newTestModal(lines []string, title string) *Modal {
	if lines == nil {
		lines = numberedLines(30)
	}
	if title == "" {
		title = "t"
	}
	return NewModal(ModalOptions{Title: title, Lines: lines, Kind: ModalHelp, Filterable: true})
}

// testStyler is the REAL styler, not the plain placeholder. Widths in this file
// have to be cell widths or every geometry assertion would be measuring
// something the terminal does not.
func testStyler() Styler { return term.NewStyler("default", nil) }

func viewTexts(view ModalView) []string {
	out := make([]string, 0, len(view.Lines))
	for _, line := range view.Lines {
		out = append(out, ansi.Strip(line))
	}
	return out
}

func TestModalWidthComesFromFullContentNotTheVisibleWindow(t *testing.T) {
	styler := testStyler()
	lines := append(strings.Split(strings.Repeat("short\n", 20), "\n")[:20],
		"a much much longer line below the fold")
	modal := newTestModal(lines, "")
	width := modal.Width(styler)
	if want := styler.Width(lines[len(lines)-1]) + 4; width < want {
		t.Fatalf("width %d is narrower than the longest line needs (%d)", width, want)
	}
	modal.ScrollPage(1, modalBodyH)
	if got := modal.View(styler, modalBodyH, "").Width; got != width {
		t.Errorf("scrolling changed the width: %d -> %d", width, got)
	}
	modal.SetFilter("short")
	if got := modal.View(styler, modalBodyH, "").Width; got != width {
		t.Errorf("filtering changed the width: %d -> %d", width, got)
	}
}

func TestModalWidthFloorsOnTitleAndMinimum(t *testing.T) {
	styler := testStyler()
	title := "a much longer modal title"
	if got, want := newTestModal([]string{"x"}, title).Width(styler),
		styler.Width(title)+6+4; got < want {
		t.Errorf("title width %d, want at least %d", got, want)
	}
	// 30 minimum plus the box's four columns of padding.
	if got := newTestModal([]string{"x"}, "t").Width(styler); got != 34 {
		t.Errorf("minimum width %d, want 34", got)
	}
}

func TestModalViewNeverExceedsTheBody(t *testing.T) {
	styler := testStyler()
	for bodyHeight := 5; bodyHeight <= 14; bodyHeight++ {
		view := newTestModal(nil, "").View(styler, bodyHeight, "")
		limit := max(bodyHeight, modalMinInner+2)
		if len(view.Lines)+2 > limit {
			t.Errorf("a %d-row body got %d boxed lines, over the %d limit",
				bodyHeight, len(view.Lines)+2, limit)
		}
	}
}

func TestModalShortContentShowsFullyWithoutStatusLine(t *testing.T) {
	view := newTestModal([]string{"a", "b", "c"}, "").View(testStyler(), modalBodyH, "")
	if got := viewTexts(view); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("short content rendered %v", got)
	}
}

func TestModalOverflowingContentGetsScrollStatusLine(t *testing.T) {
	view := newTestModal(nil, "").View(testStyler(), modalBodyH, "")
	if len(view.Lines) != 10 {
		t.Fatalf("got %d lines, want 9 content + 1 status", len(view.Lines))
	}
	last := viewTexts(view)[len(view.Lines)-1]
	if !strings.Contains(last, "9/30") || !strings.Contains(last, "↑↓ scroll") {
		t.Errorf("status line %q does not report position and scrollability", last)
	}
}

func TestModalScrollStepsAndClamping(t *testing.T) {
	styler := testStyler()
	modal := newTestModal(nil, "")
	// Viewport: 10 inner rows minus the status line = 9.
	modal.ScrollLine(1, modalBodyH)
	if modal.Scroll() != 1 {
		t.Errorf("line scroll moved %d, want 1", modal.Scroll())
	}
	modal.ScrollHalf(1, modalBodyH)
	if modal.Scroll() != 1+4 {
		t.Errorf("half scroll landed on %d, want 5", modal.Scroll())
	}
	modal.ScrollPage(1, modalBodyH)
	if modal.Scroll() != 5+9 {
		t.Errorf("page scroll landed on %d, want 14", modal.Scroll())
	}
	// Clamping: never past the end, never before the start.
	for range 10 {
		modal.ScrollPage(1, modalBodyH)
	}
	if got, want := modal.Scroll(), 30-9; got != want {
		t.Errorf("scroll ran past the end: %d, want %d", got, want)
	}
	for range 10 {
		modal.ScrollPage(-1, modalBodyH)
	}
	if modal.Scroll() != 0 {
		t.Errorf("scroll ran before the start: %d", modal.Scroll())
	}
	_ = styler
}

func TestModalFilterNarrowsLinesIgnoringStylingAndCase(t *testing.T) {
	styler := testStyler()
	modal := NewModal(ModalOptions{
		Title: "t", Kind: ModalHelp, Filterable: true,
		Lines: []string{styler.Paint("accent", "Alpha"), "beta", "GAMMA alpha"},
	})
	modal.SetFilter("alpha")
	if got := len(modal.Lines()); got != 2 {
		t.Fatalf("filter matched %d lines, want 2 (case-insensitive, styling-blind)", got)
	}
}

func TestModalFilterKeepsTheBoxTheSameHeight(t *testing.T) {
	styler := testStyler()
	modal := newTestModal(nil, "")
	unfiltered := len(modal.View(styler, modalBodyH, "").Lines)
	modal.SetFilter("line 1")
	filtered := len(modal.View(styler, modalBodyH, "/ line 1").Lines)
	if filtered != unfiltered {
		t.Errorf("the box jumped from %d rows to %d while filtering", unfiltered, filtered)
	}
	modal.SetFilter("nothing matches this")
	empty := len(modal.View(styler, modalBodyH, "/ nothing").Lines)
	if empty != unfiltered {
		t.Errorf("an empty match set resized the box: %d, want %d", empty, unfiltered)
	}
}

func TestModalNoMatchShowsAPlaceholderNotAnEmptyBox(t *testing.T) {
	modal := newTestModal(nil, "")
	modal.SetFilter("zzz")
	texts := viewTexts(modal.View(testStyler(), modalBodyH, ""))
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "no lines match") {
		t.Errorf("an empty filter result rendered no explanation: %q", joined)
	}
}

func TestModalUnchangedQueryKeepsScrollAndMemo(t *testing.T) {
	modal := newTestModal(nil, "")
	modal.SetFilter("line 1")
	modal.ScrollLine(2, modalBodyH)
	scroll := modal.Scroll()
	modal.SetFilter("line 1")
	if modal.Scroll() != scroll {
		t.Errorf("re-applying the same query reset scroll to %d, want %d", modal.Scroll(), scroll)
	}
}

func TestModalReplacePreservesFilterAndScrollIntent(t *testing.T) {
	modal := newTestModal(nil, "")
	modal.SetFilter("line 1")
	modal.ScrollLine(1, modalBodyH)
	modal.Replace("t", numberedLines(40), nil)
	if modal.Filter() != "line 1" {
		t.Errorf("replacing content dropped the filter")
	}
	if modal.Scroll() != 1 {
		t.Errorf("replacing content reset the scroll to %d", modal.Scroll())
	}
	if len(modal.Lines()) == 0 {
		t.Errorf("the refreshed content did not re-run the filter")
	}
}

func TestModalGroupedFilterKeepsTheWholeMatchingBlock(t *testing.T) {
	modal := NewModal(ModalOptions{
		Title: "t", Kind: ModalHelp, Filterable: true,
		Lines:        []string{"heading one", "alpha", "heading two", "beta"},
		FilterGroups: []string{"one", "one", "two", "two"},
	})
	modal.SetFilter("alpha")
	got := modal.Lines()
	if len(got) != 2 || got[0] != "heading one" {
		t.Errorf("a grouped match lost its heading: %v", got)
	}
}

func TestModalClearingTheFilterRestoresEveryLine(t *testing.T) {
	modal := newTestModal(nil, "")
	modal.SetFilter("line 1")
	modal.SetFilter("")
	if got := len(modal.Lines()); got != 30 {
		t.Errorf("clearing the filter left %d lines, want 30", got)
	}
}

func TestModalMisalignedFilterGroupsAreIgnoredRatherThanHidingLines(t *testing.T) {
	// A group map that does not line up with the content would hide the wrong
	// rows. Dropping it degrades to ungrouped filtering, which is merely less
	// helpful rather than wrong.
	modal := NewModal(ModalOptions{
		Title: "t", Kind: ModalHelp, Filterable: true,
		Lines: []string{"a", "b"}, FilterGroups: []string{"only-one"},
	})
	modal.SetFilter("a")
	if got := modal.Lines(); len(got) != 1 || got[0] != "a" {
		t.Errorf("a misaligned group map changed which lines matched: %v", got)
	}
}
