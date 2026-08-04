package tui

import (
	"fmt"
	"strings"
	"testing"
)

func linesOf(count int) []string {
	out := make([]string, count)
	for index := range out {
		out[index] = fmt.Sprintf("line %d", index)
	}
	return out
}

func TestPanelKeepsScrollWhenContentIsReplacedForTheSameIdentity(t *testing.T) {
	// This is the save-on-refresh contract for the panel: an external write
	// must not throw a reader back to the top of a long note.
	panel := NewRightPanel("task", PanelDetail, "aaaa0001", linesOf(100))
	panel.ScrollPage(1, 12)
	before := panel.Scroll
	if before == 0 {
		t.Fatal("scrolling did nothing")
	}
	panel.Replace("task", "aaaa0001", linesOf(100))
	if panel.Scroll != before {
		t.Fatalf("scroll reset from %d to %d on a same-identity replace", before, panel.Scroll)
	}
}

func TestPanelResetsScrollWhenTheIdentityChanges(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "aaaa0001", linesOf(100))
	panel.ScrollPage(1, 12)
	panel.Replace("task", "aaaa0002", linesOf(100))
	if panel.Scroll != 0 {
		t.Fatalf("moving to another task kept scroll at %d", panel.Scroll)
	}
}

func TestPanelStatusRowAppearsOnlyOnOverflowAndCostsALine(t *testing.T) {
	short := NewRightPanel("task", PanelDetail, "a", linesOf(3))
	view := short.View(PlainStyler{}, 10, 40)
	if len(view.Lines) != 3 {
		t.Fatalf("short content rendered %d lines", len(view.Lines))
	}

	long := NewRightPanel("task", PanelDetail, "a", linesOf(100))
	view = long.View(PlainStyler{}, 10, 40)
	if len(view.Lines) != 8 {
		t.Fatalf("overflowing content rendered %d lines, want 7 content + 1 status", len(view.Lines))
	}
	if !strings.Contains(view.Lines[len(view.Lines)-1], "/100") {
		t.Fatalf("last line is not the status row: %q", view.Lines[len(view.Lines)-1])
	}
}

func TestPanelScrollIsClampedToTheContent(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "a", linesOf(10))
	panel.ScrollPage(1, 12)
	panel.ScrollPage(1, 12)
	panel.ScrollPage(1, 12)
	if panel.Scroll > len(panel.Lines) {
		t.Fatalf("scrolled past the end: %d of %d", panel.Scroll, len(panel.Lines))
	}
	panel.ScrollPage(-1, 12)
	panel.ScrollPage(-1, 12)
	panel.ScrollPage(-1, 12)
	if panel.Scroll != 0 {
		t.Fatalf("scrolled above the start: %d", panel.Scroll)
	}
}

func TestPanelHalfScrollAlwaysMovesAtLeastOneLine(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "a", linesOf(50))
	panel.ScrollHalf(1, 3) // viewport of 0 or 1 rows
	if panel.Scroll == 0 {
		t.Fatal("a half-page scroll in a tiny panel moved nothing")
	}
}

func TestPanelRevealsAFocusedRow(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "a", linesOf(100))
	panel.FocusRow(60)
	panel.View(PlainStyler{}, 12, 40)
	viewport := panel.Viewport(12)
	if panel.Scroll > 60 || 60 >= panel.Scroll+viewport {
		t.Fatalf("row 60 is not inside the viewport [%d,%d)", panel.Scroll, panel.Scroll+viewport)
	}
}

func TestPanelTruncatesEveryLineToTheGivenWidth(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "a", []string{strings.Repeat("x", 200)})
	view := panel.View(PlainStyler{}, 10, 20)
	if got := len([]rune(view.Lines[0])); got != 20 {
		t.Fatalf("line rendered %d cells wide, want 20", got)
	}
}

func TestPanelSurvivesAZeroHeightBox(t *testing.T) {
	panel := NewRightPanel("task", PanelDetail, "a", linesOf(10))
	view := panel.View(PlainStyler{}, 0, 20)
	if len(view.Lines) != 0 {
		t.Fatalf("a zero-height panel rendered %d lines", len(view.Lines))
	}
}
