package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/record"
)

// The `?` overlay and the command palette are where a TUI user LEARNS the
// vocabulary; the `D` prompt is where they use it. Those three answers came
// from two different places before this — a static registry built during init
// and the store — so a configured user was told to type a word the store would
// then refuse. They are one answer now.
func TestTheShortcutsOverlayQuotesTheConfiguredModes(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{modes: record.ModeSet{"triage", "ship"}})

	overlay := strings.Join(HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(overlay, "Delegate… — email · triage · ship · release · off") {
		t.Fatalf("help overlay does not quote the configured modes:\n%s", overlay)
	}
	if strings.Contains(overlay, "refine") {
		t.Fatalf("help overlay still quotes the built-in modes:\n%s", overlay)
	}
	if strings.Contains(overlay, "{modes}") {
		t.Fatalf("help overlay leaked the placeholder:\n%s", overlay)
	}

	// The `D` prompt hint, the other half of the pair, agrees with it.
	if hint := delegateHint(harness.model.app.DelegationModes()); !strings.Contains(hint, "triage") ||
		strings.Contains(hint, "refine") {
		t.Fatalf("prompt hint = %q", hint)
	}
}

// An unconfigured store still reads exactly as it did.
func TestTheShortcutsOverlayQuotesTheBuiltInModesByDefault(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	overlay := strings.Join(HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(overlay, "Delegate… — email · refine · research · implement · release · off") {
		t.Fatalf("help overlay = %s", overlay)
	}
}

// The read-only check VIEW displays the store's vocabulary too. It refuses
// nothing, but it is what the format-error flash quotes, and telling a user
// their own configured mode is a format error sends them hunting a fault that
// is not there.
func TestTheCheckViewUsesTheStoreVocabulary(t *testing.T) {
	live := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","title":"Book flight","delegation":{"kind":"agent","mode":"triage","status":"ready","at":"2026-07-14T11:00:00Z"}}
`
	configured := newModelHarness(t, harnessOptions{live: live, modes: record.ModeSet{"triage", "ship"}})
	if err := configured.model.ReadError(); err != nil {
		t.Fatalf("the check view called a configured mode a format error: %v", err)
	}

	// The same file under the built-in vocabulary is not an error either — an
	// unlisted mode is a warning on disk, not a reason to refuse the file.
	plain := newModelHarness(t, harnessOptions{live: live})
	if err := plain.model.ReadError(); err != nil {
		t.Fatalf("an unlisted mode invalidated the file: %v", err)
	}
}
