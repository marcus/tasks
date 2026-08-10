package tui

import (
	"strings"
	"testing"
)

// A host that suppresses quit is exactly the host that needs to observe the
// request. Refusing the action made QuitRequested unreachable.
func TestEmbeddedQuitLatchesEvenWhenTheHostOwnsTheAffordance(t *testing.T) {
	for _, suppress := range []bool{false, true} {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.embedded = true
		harness.model.suppressQuit = suppress
		harness.model.requestQuit()
		if !harness.model.QuitRequested() {
			t.Fatalf("suppressQuit=%v did not latch the request", suppress)
		}
		if harness.model.quitting {
			t.Fatalf("suppressQuit=%v terminated the host", suppress)
		}
		if strings.Contains(harness.model.flash, "managed by the host") {
			t.Fatalf("suppressQuit=%v still flashed a refusal", suppress)
		}
		harness.model.ClearQuitRequest()
		if harness.model.QuitRequested() {
			t.Fatalf("suppressQuit=%v ignored the acknowledgement", suppress)
		}
		harness.model.requestQuit()
		if !harness.model.QuitRequested() {
			t.Fatalf("suppressQuit=%v did not re-latch a second quit", suppress)
		}
	}
}

// Tasks stops advertising an affordance the host owns, while still acting on
// the key.
func TestSuppressQuitDropsTasksOwnQuitHint(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	if !strings.Contains(harness.model.keyHint(), "q quit") {
		t.Fatalf("standalone hint lost quit: %q", harness.model.keyHint())
	}
	harness.model.embedded, harness.model.suppressQuit = true, true
	if strings.Contains(harness.model.keyHint(), "quit") {
		t.Fatalf("suppressed hint still advertises quit: %q", harness.model.keyHint())
	}
}
