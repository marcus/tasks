package tui

import (
	"strings"
	"testing"
)

// suppressViewKeyHints removes exactly one thing: the footer's "1-6 views"
// pair, which is where the jump keys are advertised now that the tab strip
// carries names alone. A host that has taken the number row for its own tab
// switching — Sidecar does — must not have Tasks advertise keys the user's
// press will never reach. The keys themselves are untouched, on the same
// reading as SuppressQuit: the HOST owns the affordance, Tasks still acts.

func TestSuppressViewKeyHintsDropsTheJumpKeyHintAndKeepsTheRest(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.width = 200
	if !strings.Contains(harness.model.keyHint(), "1-6 views") {
		t.Fatalf("the jump keys are advertised nowhere: %q", harness.model.keyHint())
	}
	harness.model.suppressViewKeyHints = true
	hint := harness.model.keyHint()
	if strings.Contains(hint, "1-6") {
		t.Errorf("the jump key hint survived its own switch: %q", hint)
	}
	if !strings.Contains(hint, "j/k move") || !strings.Contains(hint, "q quit") {
		t.Errorf("suppressing the jump keys took the rest of the hint row with it: %q", hint)
	}
}

// The tab strip never carried the numbers to begin with, at any width, with the
// switch set or not: names only, and every view keeps a name at every size.
func TestTheTabStripIsNamesOnlyAtEveryWidth(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, suppress := range []bool{false, true} {
		harness.model.suppressViewKeyHints = suppress
		for _, width := range []int{200, 40, 20, 5} {
			variant := harness.model.tabVariant(width)
			for index, tab := range Tabs {
				cell := harness.model.tabCell(tab, variant)
				want := [3]string{tab.Label, tab.Compact, tab.Minimum}[variant]
				if !strings.HasPrefix(cell, want) {
					t.Fatalf("width %d: view %q rendered %q, want it to start with %q",
						width, tab.Key, cell, want)
				}
				if strings.ContainsRune(cell, rune('1'+index)) {
					t.Fatalf("width %d: view %q advertises its jump key: %q",
						width, tab.Key, cell)
				}
			}
		}
	}
}

func TestSuppressViewKeyHintsKeepsTheCurrentViewIndicated(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.styler = testStyler()
	harness.model.suppressViewKeyHints = true
	for _, view := range []string{ViewNext, ViewOutline, ViewInbox} {
		tab := tabFor(t, view)
		harness.model.SwitchView(ViewAgenda)
		inactive := harness.model.tabCell(tab, 0)
		harness.model.SwitchView(view)
		active := harness.model.tabCell(tab, 0)
		if active == inactive {
			t.Fatalf("view %q looks the same current and not: %q", view, active)
		}
		if !strings.Contains(active, tab.Label) {
			t.Fatalf("the current view lost its name: %q", active)
		}
		if !strings.Contains(harness.model.TabStrip(200), active) {
			t.Fatalf("the strip does not show %q as current: %q",
				view, harness.model.TabStrip(200))
		}
	}
}

// The whole point of the option: it is an ADVERTISEMENT switch. A host may have
// taken only some of the number row, and standalone behavior is untouched.
func TestSuppressViewKeyHintsLeavesTheNumberKeysWorking(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.suppressViewKeyHints = true
	for index, tab := range Tabs {
		harness.model.SwitchView(ViewAgenda)
		harness.press(rune('1' + index))
		if harness.model.CurrentView() != tab.Key {
			t.Fatalf("key %d did not jump to %q: view=%q", index+1, tab.Key,
				harness.model.CurrentView())
		}
	}
}

// Standalone output is identical with the option set or not — the strip has
// nothing left for it to suppress — at every degradation step.
func TestTabStripIsUnchangedWhenViewKeyHintsAreNotSuppressed(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, width := range []int{200, 40, 20, 5} {
		want := map[int]string{
			200: "agenda   next   quadrants   projects   outline   inbox 1",
			40:  "ag   nx   quad   proj   out   in 1",
			20:  "ag   nx   q   pr   out   in 1",
			5:   "ag   nx   q   pr   out   in 1",
		}[width]
		if got := harness.model.TabStrip(width); got != want {
			t.Fatalf("width %d: strip = %q, want %q", width, got, want)
		}
	}
}

// The three suppression switches are independent knobs, not a scale.
func TestSuppressionSwitchesAreIndependent(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.embedded = true
	harness.model.suppressViewKeyHints = true
	harness.model.width = 200
	if strings.Contains(harness.model.keyHint(), "1-6") {
		t.Fatal("view key hints survived their own switch")
	}
	if hints := strings.Join(harness.model.Footer(), "\n"); !strings.Contains(hints, "j/k") {
		t.Fatalf("suppressing view key hints also removed the footer hint row:\n%s", hints)
	}

	harness.model.suppressKeyHints = true
	if footer := strings.Join(harness.model.Footer(), "\n"); strings.Contains(footer, "j/k") {
		t.Fatalf("the footer hint row survived SuppressKeyHints:\n%s", footer)
	}
	if strip := harness.model.TabStrip(200); !strings.Contains(strip, "agenda") {
		t.Fatalf("the tab strip lost its names: %q", strip)
	}

	harness.model.suppressFooter = true
	if footer := harness.model.Footer(); footer != nil {
		t.Fatalf("SuppressFooter left rows behind: %v", footer)
	}
	// All three set is coherent: header names without keys, no footer at all.
	strip := harness.model.TabStrip(200)
	if !strings.Contains(strip, "inbox") || strings.Contains(strip, "6 inbox") ||
		strings.Contains(strip, "1 agenda") {
		t.Fatalf("all three set produced an incoherent strip: %q", strip)
	}
}

func tabFor(t *testing.T, view string) Tab {
	t.Helper()
	for _, tab := range Tabs {
		if tab.Key == view {
			return tab
		}
	}
	t.Fatalf("no tab for view %q", view)
	return Tab{}
}
