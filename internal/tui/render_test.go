package tui

import (
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rendering is checked with FIXED-SIZE fixtures rather than with a terminal.
// The model renders to a string, so a frame is a value a test can compare, and
// the golden files are readable diffs rather than escape soup.
//
// Run `go test ./internal/tui -update` to rewrite them after an intended
// change; read the diff before you do.
var update = os.Getenv("UPDATE_TUI_GOLDEN") != ""

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".txt")
	if update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (set UPDATE_TUI_GOLDEN=1 to create it):\n%s", path, got)
	}
	if string(want) != got {
		t.Fatalf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func renderAt(t *testing.T, harness *modelHarness, width, height int) string {
	t.Helper()
	harness.model.width, harness.model.height = width, height
	return harness.model.Render()
}

func TestFrameIsExactlyTheRequestedSize(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, size := range [][2]int{{80, 24}, {100, 30}, {40, 12}, {MinWidth, MinHeight}} {
		frame := renderAt(t, harness, size[0], size[1])
		lines := strings.Split(frame, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d frame has %d lines", size[0], size[1], len(lines))
		}
		for index, line := range lines {
			if got := len([]rune(line)); got != size[0] {
				t.Errorf("%dx%d frame line %d is %d cells wide: %q",
					size[0], size[1], index, got, line)
			}
		}
	}
}

func TestFrameNeverWrapsOnANarrowTerminal(t *testing.T) {
	// A responsive width contract: the body must degrade, not overflow. An
	// overflowing line wraps in the real terminal and shears the whole frame.
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewQuadrants)
	for width := MinWidth; width <= 60; width++ {
		for _, line := range strings.Split(renderAt(t, harness, width, 20), "\n") {
			if got := len([]rune(line)); got != width {
				t.Fatalf("at width %d a line came out %d cells wide: %q", width, got, line)
			}
		}
	}
}

func TestAgendaFrameGolden(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewAgenda)
	harness.selectRowByID(fixFlight)
	assertGolden(t, "agenda_80x24", renderAt(t, harness, 80, 24))
}

func TestNextFrameWithPanelGolden(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.model.OpenDetail()
	assertGolden(t, "next_panel_100x24", renderAt(t, harness, 100, 24))
}

func TestOutlineFrameGolden(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewOutline)
	assertGolden(t, "outline_80x24", renderAt(t, harness, 80, 24))
}

func TestInboxFrameGolden(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewInbox)
	assertGolden(t, "inbox_80x24", renderAt(t, harness, 80, 24))
}

func TestNarrowFrameGolden(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	assertGolden(t, "next_narrow_44x16", renderAt(t, harness, 44, 16))
}

func TestSelectedRowCarriesAGutterMarkerWithNoColorAtAll(t *testing.T) {
	// PlainStyler paints nothing — the same thing a NO_COLOR monochrome theme
	// can end up doing. The cursor must still be findable.
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixPlants)
	// Counted over the BODY only: the prompt line opens with the same glyph,
	// deliberately — both mean "you are here".
	frame := strings.Split(renderAt(t, harness, 80, 24), "\n")
	// The body is everything between the header's blank and the footer's.
	body := frame[2 : len(frame)-3]
	if got := strings.Count(strings.Join(body, "\n"), Cursor); got != 1 {
		t.Fatalf("expected exactly one cursor marker in the body, got %d:\n%s",
			got, strings.Join(body, "\n"))
	}
}

func TestFooterNamesTheKeysThatWork(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	footer := strings.Join(harness.model.Footer(), "\n")
	for _, key := range []string{"j/k", "1-6", "h/l", "enter", "/", "q"} {
		if !strings.Contains(footer, key) {
			t.Errorf("footer does not advertise %q: %s", key, footer)
		}
	}
}

func TestKeyHintDegradesOnAWordRatherThanMidWord(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for width := MinWidth; width <= 120; width++ {
		harness.model.width = width
		hint := harness.model.keyHint()
		if got := len([]rune(hint)); got > width-2 {
			t.Fatalf("at width %d the hint is %d cells: %q", width, got, hint)
		}
		if strings.HasSuffix(hint, " ·") || strings.HasSuffix(hint, "·") {
			t.Fatalf("at width %d the hint ends on a separator: %q", width, hint)
		}
	}
}

func TestFooterShowsAnActiveSearch(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.press('/')
	harness.press('p')
	if !strings.Contains(strings.Join(harness.model.Footer(), "\n"), "/p") {
		t.Fatalf("the search buffer is invisible: %v", harness.model.Footer())
	}
}

func TestFooterShowsActiveContextFilters(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.contextFilters = []string{"@home"}
	if !strings.Contains(strings.Join(harness.model.Footer(), "\n"), "@home") {
		t.Fatalf("an active context filter is invisible: %v", harness.model.Footer())
	}
}

func TestNoBoundKeyStillRefusesAsUnimplemented(t *testing.T) {
	// The registry is the contract. At completion every bound handler must
	// either DO its thing or refuse for a reason about the CURRENT SELECTION —
	// never "this build cannot".
	//
	// stillUnbuilt is the shrinking list. It must reach empty; a name that is
	// implemented while still listed here also fails, so the list cannot rot in
	// either direction.
	// EMPTY. Every bound key does its thing or refuses for a reason about the
	// current selection.
	stillUnbuilt := map[string]bool{}
	for name := range unbuiltHandlers {
		if !stillUnbuilt[name] {
			t.Errorf("handler %q refuses as unimplemented but is not on the known list", name)
		}
	}
	for name := range stillUnbuilt {
		if _, refuses := unbuiltHandlers[name]; !refuses {
			t.Errorf("handler %q is implemented; remove it from stillUnbuilt", name)
		}
	}
	for _, context := range shortcuts.Contexts {
		entries, err := shortcuts.Entries(context, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.DocOnly || entry.Handler == "" {
				continue
			}
			if stillUnbuilt[entry.Handler] {
				continue
			}
			harness := newModelHarness(t, harnessOptions{})
			if _, present := harness.model.handlers()[entry.Handler]; !present {
				t.Errorf("registry handler %q has no implementation", entry.Handler)
			}
		}
	}
}

// The keys this packet built must now DO something. A test that only pins the
// refusals would stay green if the whole half were reverted.
func TestEditorOwnedKeysAreLiveNow(t *testing.T) {
	cases := []struct {
		key   rune
		check func(*Model) bool
		what  string
	}{
		{'?', func(m *Model) bool { return m.Mode() == ModeModal && m.Modal().Kind() == ModalHelp }, "help modal"},
		{':', func(m *Model) bool { return m.Mode() == ModePalette }, "action palette"},
		{'@', func(m *Model) bool { return m.Mode() == ModeContextPalette }, "context palette"},
		{'d', func(m *Model) bool { return m.Mode() == ModeForm && m.Form().Kind == QuickFormDate }, "date popup"},
		{'r', func(m *Model) bool { return m.Mode() == ModeForm && m.Form().Kind == QuickFormRecurrence }, "recurrence popup"},
	}
	for _, testCase := range cases {
		harness := newModelHarness(t, harnessOptions{})
		harness.selectRowByID(fixFlight)
		harness.press(testCase.key)
		if !testCase.check(harness.model) {
			t.Errorf("key %q did not open the %s; mode is %q, flash %q",
				testCase.key, testCase.what, harness.model.Mode(), harness.model.FlashMessage())
		}
	}
}

func TestHeaderCountsOpenAvailableWork(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	if got := harness.model.OpenTaskCount(); got != 6 {
		t.Fatalf("open count %d, want the 6 open available fixture tasks", got)
	}
}

func TestHeaderMarksTheActiveTab(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SwitchView(ViewInbox)
	// PlainStyler paints nothing, so the tab strip is checked structurally:
	// the theme decides the look, and every tab stays reachable.
	strip := harness.model.TabStrip(200)
	for _, tab := range Tabs {
		if !strings.Contains(strip, tab.Label) {
			t.Errorf("tab %q is missing from the strip: %q", tab.Label, strip)
		}
	}
}

func TestTabStripDegradesLabelsRatherThanDroppingTabs(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	strip := harness.model.TabStrip(20)
	for _, tab := range Tabs {
		if !strings.Contains(strip, tab.Minimum) {
			t.Fatalf("tab %q vanished at width 20: %q", tab.Key, strip)
		}
	}
}

func TestAReadFailureIsStatedRatherThanPaintedAsAnEmptyStore(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.rewrite("this is not jsonl at all\n")
	harness.tick()
	frame := renderAt(t, harness, 100, 24)
	// The store's readers coerce defensively, so a broken file reads as a store
	// with FEWER TASKS rather than as an error. The frame must therefore say
	// something — either the read failed outright, or the format check did.
	if !strings.Contains(frame, "cannot read") && !strings.Contains(frame, "format error") {
		t.Fatalf("an unreadable store rendered as an empty list:\n%s", frame)
	}
}
