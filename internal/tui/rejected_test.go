package tui

import (
	"strings"
	"testing"
)

// rejectedFixture has one undecided proposal, one decline from the day before
// the harness clock, and one ordinary cancellation — the three cases the toggle
// has to keep apart.
const rejectedFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"random thought about the garden"}
{"type":"task","id":"aaaa0010","parent":"aaaa0001","state":"PROPOSED","title":"Pending proposal"}
{"type":"task","id":"aaaa0011","parent":"aaaa0001","state":"CANCELLED","title":"Declined proposal","closed":"2026-07-13","rejected":"2026-07-13"}
{"type":"task","id":"aaaa0012","parent":"aaaa0001","state":"CANCELLED","title":"Ordinary cancellation","closed":"2026-07-13"}
`

func rowsText(harness *modelHarness) string {
	return strings.Join(harness.titles(), "\n")
}

// Intake opens clean: a decision already made is not part of the queue of
// undecided work until the reviewer asks for it.
func TestInboxHidesRejectedUntilToggled(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	if text := rowsText(harness); strings.Contains(text, "Declined proposal") {
		t.Fatalf("APPROVALS showed a decline by default:\n%s", text)
	}

	harness.press('R')
	shown := rowsText(harness)
	if !strings.Contains(shown, "Declined proposal") {
		t.Fatalf("R did not reveal the decline:\n%s", shown)
	}
	if !strings.Contains(shown, "2026-07-13") {
		t.Errorf("a revealed decline says when it was declined:\n%s", shown)
	}
	if strings.Contains(shown, "Ordinary cancellation") {
		t.Errorf("an ordinary cancellation is not a declined proposal:\n%s", shown)
	}
	if !strings.Contains(harness.model.FlashMessage(), "rejected") {
		t.Errorf("flash = %q", harness.model.FlashMessage())
	}

	harness.press('R')
	if text := rowsText(harness); strings.Contains(text, "Declined proposal") {
		t.Fatalf("R again did not hide the decline:\n%s", text)
	}
}

// One keystroke from a visible row: `a` restores the selected decline, and the
// row it produces is a PROPOSED task in the queue above.
func TestRestoreARevealedRejectFromInbox(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	harness.press('R')
	harness.selectRowByID("aaaa0011")
	harness.press('a')

	if !strings.Contains(harness.model.FlashMessage(), "restored → PROPOSED") {
		t.Fatalf("flash = %q", harness.model.FlashMessage())
	}
	item, ok := harness.model.read.Queries().FindLive("aaaa0011")
	if !ok {
		t.Fatal("the restored task is gone")
	}
	if item.State != "PROPOSED" || item.Rejected != "" || item.Closed != "" {
		t.Errorf("restored item = %+v", item)
	}
	// It is now an ordinary pending proposal, decidable with a/r as usual.
	harness.model.showRejected = false
	harness.model.RefreshRows()
	if text := rowsText(harness); !strings.Contains(text, "Declined proposal") {
		t.Errorf("the restored proposal must show in APPROVALS without the toggle:\n%s", text)
	}
}

// `a` on an ordinary cancellation is not a restore. The availability predicate is
// what keeps the two apart, so it is asserted directly as well.
func TestRestoreRefusesANonDecline(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	harness.press('R')
	if harness.model.availability("reject_restore_available?") {
		t.Error("nothing is selected yet")
	}
	harness.selectRowByID("aaaa0011")
	if !harness.model.availability("reject_restore_available?") {
		t.Error("a live decline is restorable")
	}
	harness.model.RestoreRejected()
	if !strings.Contains(harness.model.FlashMessage(), "restored") {
		t.Fatalf("flash = %q", harness.model.FlashMessage())
	}
	// After the restore the same row is a proposal, and restore no longer applies.
	harness.selectRowByID("aaaa0011")
	if harness.model.availability("reject_restore_available?") {
		t.Error("a PROPOSED task is not a decline")
	}
	harness.model.RestoreRejected()
	if !strings.Contains(harness.model.FlashMessage(), "select a rejected proposal") {
		t.Errorf("flash = %q", harness.model.FlashMessage())
	}
}

// A `/` search narrows every row in the view, and a revealed decline is a row
// in the view: filtering Inbox must not keep showing unrelated rejects.
func TestRevealedRejectsHonorTheTextFilter(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	harness.press('R')
	for _, key := range "/garden\r" {
		harness.press(key)
	}
	text := rowsText(harness)
	if strings.Contains(text, "Declined proposal") {
		t.Fatalf("a /garden filter still showed an unrelated decline:\n%s", text)
	}
	if !strings.Contains(text, "garden") {
		t.Fatalf("the filter lost its own match:\n%s", text)
	}
}

// With the reveal on and nothing pending, the queue is still empty — the
// placeholder says so above the decided rows instead of vanishing.
func TestPlaceholderSurvivesWhenOnlyRejectsAreShown(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0011","parent":"aaaa0001","state":"CANCELLED","title":"Declined proposal","closed":"2026-07-13","rejected":"2026-07-13"}
`
	harness := newModelHarness(t, harnessOptions{live: fixture})
	harness.model.SwitchView(ViewInbox)
	harness.press('R')
	text := rowsText(harness)
	if !strings.Contains(text, "Nothing pending approval") {
		t.Fatalf("the empty-queue placeholder vanished behind revealed rejects:\n%s", text)
	}
	if !strings.Contains(text, "Declined proposal") {
		t.Fatalf("the revealed decline went missing:\n%s", text)
	}
}

// `c` on a proposal is approve+complete: the advertised key does what it says,
// which the plain completion could not — the store refuses to complete a
// PROPOSED task at all.
func TestCompleteOnAProposalApprovesAndCompletesIt(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	harness.selectRowByID("aaaa0010")
	if !harness.model.availability("proposal_action_available?") ||
		harness.model.availability("completion_action_available?") {
		t.Fatal("a proposal row must offer approve+complete rather than plain completion")
	}
	harness.press('c')

	if !strings.Contains(harness.model.FlashMessage(), "approved + DONE") {
		t.Fatalf("flash = %q", harness.model.FlashMessage())
	}
	item, ok := harness.model.read.Queries().FindLive("aaaa0010")
	if !ok {
		t.Fatal("the approved task is gone")
	}
	if item.State != "DONE" || item.Closed == "" {
		t.Errorf("item = %+v, want a closed DONE task", item)
	}

	// One undo step puts the proposal back exactly as it was.
	harness.model.UndoLast()
	restored, ok := harness.model.read.Queries().FindLive("aaaa0010")
	if !ok {
		t.Fatal("undo lost the proposal")
	}
	if restored.State != "PROPOSED" || restored.Closed != "" {
		t.Errorf("after undo = %+v, want PROPOSED", restored)
	}
}

// `c` away from a proposal is the ordinary completion, unchanged.
func TestCompleteElsewhereIsStillThePlainCompletion(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{live: rejectedFixture})
	harness.model.SwitchView(ViewInbox)
	harness.selectRowByID("aaaa0002")
	harness.press('c')
	if !strings.Contains(harness.model.FlashMessage(), "\u2713 DONE") ||
		strings.Contains(harness.model.FlashMessage(), "approved") {
		t.Fatalf("flash = %q", harness.model.FlashMessage())
	}
}
