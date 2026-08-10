package tui

import (
	"os"
	"strings"
	"testing"
)

// The store's readers coerce defensively, so a broken file used to read as a
// store with fewer tasks in it. Every one of these is a real read failure and
// has to stay one across refreshes, not just flash once.
func TestRefreshReportsAnUnreadableStoreStickily(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	if harness.model.ReadError() != nil {
		t.Fatalf("healthy store reported %v", harness.model.ReadError())
	}

	harness.rewrite("{{{not json\n")
	harness.model.Refresh()
	if harness.model.ReadError() == nil {
		t.Fatal("corrupt store reported no read error")
	}
	// Sticky: the flash expires, the condition does not.
	harness.model.flash = ""
	harness.model.Refresh()
	if harness.model.ReadError() == nil {
		t.Fatal("read error did not survive a second refresh")
	}
	if !strings.Contains(strings.Join(harness.model.Footer(), "\n"), "cannot read the task store") {
		t.Fatalf("footer lost the banner:\n%s", strings.Join(harness.model.Footer(), "\n"))
	}

	harness.rewrite(fixtureStore)
	harness.model.Refresh()
	if harness.model.ReadError() != nil {
		t.Fatalf("a repaired store still reported %v", harness.model.ReadError())
	}
}

func TestRefreshReportsAStorePathThatIsADirectory(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	if err := os.Remove(harness.org); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(harness.org, 0o755); err != nil {
		t.Fatal(err)
	}
	harness.model.Refresh()
	if harness.model.ReadError() == nil {
		t.Fatal("a directory in the store's place reported no read error")
	}
}

func TestRefreshReportsAnUnreadableStoreFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 0000 file")
	}
	harness := newModelHarness(t, harnessOptions{})
	if err := os.Chmod(harness.org, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(harness.org, 0o644) })
	harness.model.Refresh()
	if harness.model.ReadError() == nil {
		t.Fatal("an unreadable store reported no read error")
	}
}

// A first run is not a fault: Tasks writes the store on the first mutation, so
// an absent or zero-length file inside an existing tasks directory is the
// legitimate empty state and must not paint an error banner.
func TestRefreshTreatsAFirstRunStoreAsHealthyAndEmpty(t *testing.T) {
	for name, prepare := range map[string]func(*modelHarness){
		"no file yet": func(h *modelHarness) { _ = os.Remove(h.org) },
		"zero length": func(h *modelHarness) { _ = os.WriteFile(h.org, nil, 0o644) },
		"only a meta": func(h *modelHarness) { _ = os.WriteFile(h.org, []byte("{\"type\":\"meta\",\"version\":2}\n"), 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			harness := newModelHarness(t, harnessOptions{})
			prepare(harness)
			harness.model.Refresh()
			if err := harness.model.ReadError(); err != nil {
				t.Fatalf("%s reported %v", name, err)
			}
			if strings.Contains(strings.Join(harness.model.Footer(), "\n"), "cannot read the task store") {
				t.Fatalf("%s painted an error banner", name)
			}
		})
	}
}
