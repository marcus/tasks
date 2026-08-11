package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/tui/term"
	"github.com/marcus/tasks/internal/tui/term/ansi"
)

func TestTheFrameDrawsNoBorderAndSeparatesWithSpace(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewAgenda)
	lines := strings.Split(renderAt(t, harness, 80, 20), "\n")

	for index, line := range lines {
		// Section rules legitimately draw "─"; a FRAME is corners and edges.
		if strings.ContainsAny(line, "┌┐└┘├┤") {
			t.Fatalf("line %d still draws frame chrome: %q", index, line)
		}
		cells := []rune(line)
		if cells[0] != ' ' || cells[len(cells)-1] != ' ' {
			t.Fatalf("line %d still has an edge cell: %q", index, line)
		}
		if got := len([]rune(line)); got != 80 {
			t.Fatalf("line %d is %d cells wide", index, got)
		}
	}
	layout := harness.model.Layout()
	bodyBegin, bodyEnd := layout.BodyRows()
	for _, blank := range []int{bodyBegin - 1, bodyEnd} {
		if strings.TrimSpace(lines[blank]) != "" {
			t.Fatalf("row %d should be the blank that replaced a rule: %q", blank, lines[blank])
		}
	}
	if strings.TrimSpace(lines[layout.HeaderRow()]) == "" {
		t.Fatal("the header row is blank")
	}
}

func TestTheReclaimedChromeRowsGoToTheList(t *testing.T) {
	// The border, its two corners rows and the two rules cost four rows. Two of
	// them were structural (top and bottom) and two were the rules; all four are
	// gone and two blanks replace them, so the body is two rows taller than the
	// bordered frame at the same terminal size.
	harness := newModelHarness(t, harnessOptions{})
	harness.model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	layout := harness.model.Layout()
	if want := 24 - FixedRows - layout.FooterSize(); layout.BodyHeight != want {
		t.Fatalf("body height = %d, want %d", layout.BodyHeight, want)
	}
	if FixedRows != 3 {
		t.Fatalf("FixedRows = %d, want the header plus two blanks", FixedRows)
	}
}

func TestKeyHintsDropWholePairsAndKeepTheWaysOutLongest(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = term.NewStyler("mono", nil)

	previous := 999
	for width := 120; width >= 20; width -= 4 {
		harness.model.width = width
		hint := ansi.Strip(harness.model.keyHint())
		if hint == "" {
			continue
		}
		if got := len([]rune(hint)); got > width-2 {
			t.Fatalf("width %d: hint is %d cells: %q", width, got, hint)
		}
		// Never cut mid-pair: every key still has its whole label.
		pairs := strings.Split(strings.TrimSpace(hint), "   ")
		for _, pair := range pairs {
			if len(strings.Fields(pair)) < 2 {
				t.Fatalf("width %d: hint was truncated mid-pair: %q", width, hint)
			}
		}
		count := len(pairs)
		if count > previous {
			t.Fatalf("width %d: a narrower terminal grew the hint: %q", width, hint)
		}
		previous = count
		if count > 0 && !strings.Contains(hint, "q quit") {
			t.Fatalf("width %d: quit is the last thing to drop, not the first: %q", width, hint)
		}
	}
}

func TestTheTabStripIsNamesAndTheSelectedRowIsLitEndToEnd(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = term.NewStyler("default", nil)
	if plain := ansi.Strip(harness.model.TabStrip(200)); strings.ContainsAny(plain, "123456") &&
		!strings.Contains(plain, "inbox 1") {
		t.Fatalf("the strip advertises a jump key: %q", plain)
	}

	// A row whose own fields close with a reset — the coloured priority letter —
	// must stay lit to the end of the list column. Wrapping the row in the
	// selection slot puts the highlight out at the first reset; compositing
	// re-opens it, which is what this asserts.
	harness.model.SwitchView(ViewAgenda)
	harness.selectRowByID(fixFlight)
	line := ""
	for _, painted := range strings.Split(harness.model.Render(), "\n") {
		if strings.Contains(ansi.Strip(painted), Cursor) {
			line = painted
			break
		}
	}
	if line == "" {
		t.Fatal("no selected row was painted")
	}
	selection := harness.model.styler.(*term.Styler).Theme().SGR("selection")
	if selection == "" {
		t.Skip("this theme does not paint selection")
	}
	// Every reset inside the row is followed by the selection SGR re-opening, so
	// the highlight survives each coloured field and reaches the padding at the
	// far edge of the list column.
	resets := strings.Count(line, "\x1b[0m")
	reopens := strings.Count(line, "\x1b[0m"+selection)
	if resets == 0 {
		t.Fatalf("the selected row carries no styling at all: %q", line)
	}
	if reopens < resets-1 {
		t.Fatalf("the selection dies at a field reset (%d resets, %d re-opens): %q",
			resets, reopens, line)
	}
	if !strings.HasPrefix(strings.TrimLeft(line, " "), selection) {
		t.Fatalf("the selected row does not open with the selection: %q", line)
	}
}
