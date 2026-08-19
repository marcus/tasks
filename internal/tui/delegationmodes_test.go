package tui

import (
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// The `?` overlay and the command palette are where a TUI user LEARNS the
// vocabulary; the `D` prompt is where they use it. Those three answers came
// from two different places before this — a static registry built during init
// and the store — so a configured user was told to type a word the store would
// then refuse. They are one answer now.
func TestTheShortcutsOverlayQuotesTheConfiguredModes(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{modes: record.ModeSet{"triage", "ship"}})

	overlay := strings.Join(HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(overlay, "Delegate… — person or agent · triage · ship · note") {
		t.Fatalf("help overlay does not quote the configured modes:\n%s", overlay)
	}
	if strings.Contains(overlay, "refine") {
		t.Fatalf("help overlay still quotes the built-in modes:\n%s", overlay)
	}
	if strings.Contains(overlay, "{modes}") {
		t.Fatalf("help overlay leaked the placeholder:\n%s", overlay)
	}

	// The `D` modal's Mode field, the other half of the pair, offers the same
	// vocabulary — and offers it as options, so there is nothing to spell.
	offered := []string{}
	for _, option := range delegateModeOptions(harness.model.app.DelegationModes()) {
		offered = append(offered, option.Value)
	}
	if strings.Join(offered, ",") != "triage,ship" {
		t.Fatalf("the Mode field offers %v", offered)
	}
}

// An unconfigured store still reads exactly as it did.
func TestTheShortcutsOverlayQuotesTheBuiltInModesByDefault(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	overlay := strings.Join(HelpContent(harness.model.styler, harness.model.app.DelegationModes()).Lines, "\n")
	if !strings.Contains(overlay, "Delegate… — person or agent · refine · research · implement · note") {
		t.Fatalf("help overlay = %s", overlay)
	}
}

// The modal has no word grammar left to make ambiguous.
//
// The old prompt matched the modes, `release` and the clear words against ONE
// text field, so a mode spelled like a verb made every input ambiguous. Here
// each part has its own control: the only word the assignee field reads
// specially is `agent`, and a mode — even a mode literally named `agent` — is
// never typed into that field at all. The two vocabularies cannot collide
// because they are never compared.
func TestTheDelegateModalHasNoWordGrammarToMakeAmbiguous(t *testing.T) {
	collides := record.ModeSet{delegateAgentValue, "ship"}
	if delegateModeError(delegateAgentValue, collides) != "" {
		t.Fatalf("a mode named %q is refused by the Mode field", delegateAgentValue)
	}
	if delegateAssigneeError("pat@example.com") != "" {
		t.Fatal("an address is refused while such a mode is configured")
	}

	// Every offered mode is one the field will then accept, for any vocabulary.
	for _, vocabulary := range []record.ModeVocabulary{
		nil,
		record.ModeSet{"triage", "ship"},
		record.ModeSet{"release_notes", "offboard", "cleared"},
	} {
		for _, option := range delegateModeOptions(vocabulary) {
			if delegateModeError(option.Value, vocabulary) != "" {
				t.Fatalf("%v offers %q and then refuses it", vocabulary, option.Value)
			}
		}
	}
}

// No flash or hint may show a user the raw placeholder.
func TestNoTuiSurfaceShowsTheModesPlaceholder(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{modes: record.ModeSet{"triage", "ship"}})
	entry, found := shortcutForDelegate()
	if !found {
		t.Fatal("the delegate entry is gone from the registry")
	}
	described := harness.model.describe(entry)
	if strings.Contains(described, shortcuts.ModesPlaceholder) {
		t.Fatalf("describe leaks the placeholder: %q", described)
	}
	if !strings.Contains(described, "triage") {
		t.Fatalf("describe = %q", described)
	}
	// The modal's own hints are written text, not templates, and its Mode field
	// carries the vocabulary itself — so there is nothing left to leak.
	harness.model.SwitchView(ViewNext)
	harness.selectRowByID(fixFlight)
	harness.pressKeys("D")
	frame := ansi.Strip(harness.model.Render())
	if strings.Contains(frame, shortcuts.ModesPlaceholder) {
		t.Fatalf("the delegate modal leaks the placeholder:\n%s", frame)
	}
	if !strings.Contains(frame, "triage") {
		t.Fatalf("the delegate modal does not offer the configured modes:\n%s", frame)
	}
}

func shortcutForDelegate() (shortcuts.Entry, bool) {
	for _, entry := range shortcuts.Registry {
		if entry.CommandID == "delegate-selected" {
			return entry, true
		}
	}
	return shortcuts.Entry{}, false
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
