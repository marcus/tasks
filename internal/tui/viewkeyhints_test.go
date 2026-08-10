package tui

import (
	"strings"
	"testing"
)

// suppressViewKeyHints removes exactly one thing: the "1 ".."6 " prefixes in
// the header's tab strip. A host that has taken the number row for its own tab
// switching — Sidecar does — must not have Tasks advertise keys the user's
// press will never reach. The keys themselves are untouched, on the same
// reading as SuppressQuit: the HOST owns the affordance, Tasks still acts.

func TestSuppressViewKeyHintsDropsTheNumbersAndKeepsTheNames(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.suppressViewKeyHints = true
	strip := harness.model.TabStrip(200)
	for _, tab := range Tabs {
		if !strings.Contains(strip, tab.PlainLabel) {
			t.Errorf("view %q lost its name: %q", tab.Key, strip)
		}
		if strings.Contains(strip, tab.Label) {
			t.Errorf("view %q still advertises its number: %q", tab.Key, strip)
		}
	}
}

// Degraded widths are the sneaky case: the narrowest label used to BE the
// number, so a host with a narrow pane would have shown nothing but the keys it
// had taken. No cell may LEAD with a jump key at any size. (The Inbox badge is
// a count, not a key, and it trails the name.)
func TestSuppressViewKeyHintsKeepsEveryViewNamedAtEveryWidth(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.suppressViewKeyHints = true
	for _, width := range []int{200, 40, 20, 5} {
		variant := harness.model.tabVariant(width)
		for index, tab := range Tabs {
			cell := harness.model.tabCell(tab, variant)
			want := [3]string{tab.PlainLabel, tab.PlainCompact, tab.PlainMinimum}[variant]
			if !strings.HasPrefix(cell, want) {
				t.Fatalf("width %d: view %q rendered %q, want it to start with %q",
					width, tab.Key, cell, want)
			}
			if strings.HasPrefix(cell, string(rune('1'+index))) {
				t.Fatalf("width %d: view %q still leads with its jump key: %q",
					width, tab.Key, cell)
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
		if !strings.Contains(active, tab.PlainLabel) {
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

// Standalone output must be byte-identical to what it was before the option
// existed, at every degradation step.
func TestTabStripIsUnchangedWhenViewKeyHintsAreNotSuppressed(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	for _, width := range []int{200, 40, 20, 5} {
		want := map[int]string{
			200: "1 Agenda 2 Next 3 Quadrants 4 Projects 5 Outline 6 Inbox 1",
			40:  "1 Ag 2 Nx 3 Q 4 Pr 5 Out 6 In 1",
			20:  "1 2 3 4 5 6 1",
			5:   "1 2 3 4 5 6 1",
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
	if strings.Contains(harness.model.TabStrip(200), "1 Agenda") {
		t.Fatal("view key hints survived their own switch")
	}
	if hints := strings.Join(harness.model.Footer(), "\n"); !strings.Contains(hints, "j/k") {
		t.Fatalf("suppressing view key hints also removed the footer hint row:\n%s", hints)
	}

	harness.model.suppressKeyHints = true
	if footer := strings.Join(harness.model.Footer(), "\n"); strings.Contains(footer, "j/k") {
		t.Fatalf("the footer hint row survived SuppressKeyHints:\n%s", footer)
	}
	if strip := harness.model.TabStrip(200); !strings.Contains(strip, "Agenda") {
		t.Fatalf("the tab strip lost its names: %q", strip)
	}

	harness.model.suppressFooter = true
	if footer := harness.model.Footer(); footer != nil {
		t.Fatalf("SuppressFooter left rows behind: %v", footer)
	}
	// All three set is coherent: header names without keys, no footer at all.
	strip := harness.model.TabStrip(200)
	if !strings.Contains(strip, "Inbox") || strings.Contains(strip, "6 Inbox") ||
		strings.Contains(strip, "1 Agenda") {
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
