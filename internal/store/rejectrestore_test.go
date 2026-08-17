package store

import (
	"testing"

	"github.com/marcus/tasks/internal/check"
)

// -- restoring a declined proposal ---------------------------------------------

// A reject stamps the marker that separates it from an ordinary cancellation.
// Without that fact in the file, no read could tell the two apart, and both
// `list --rejected` and `unreject` would be guessing from history.
func TestRejectStampsTheDeclineMarker(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	if result := store.DecideProposal("ccdd0003", ProposalReject, nil, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("reject = %q %v", result.Status, result.Errors)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("rejected") != sweepDay {
		t.Errorf("rejected = %q, want %q", child.String("rejected"), sweepDay)
	}
	if !check.Check(store.org).OK() {
		t.Error("the marker must leave a store check accepts")
	}
}

// An ordinary cancellation carries no marker, so it can never be restored into a
// review queue it was never in.
func TestCancelLeavesNoDeclineMarker(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	expected, _ := store.ExpectedFor("ccdd0004", FieldState)
	if result := store.PatchTask("ccdd0004", FieldState, "CANCELLED", expected, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("cancel = %q %v", result.Status, result.Errors)
	}
	accepted, _ := recordFor(t, store.org, "Accepted work")
	if accepted.String("rejected") != "" {
		t.Errorf("rejected = %q, want empty on an ordinary cancellation", accepted.String("rejected"))
	}
	result := store.UnrejectProposal("ccdd0004", "", sweepDay)
	if result.Status != MutationInvalid ||
		result.FirstError() != "task is CANCELLED, not a rejected proposal" {
		t.Errorf("unreject of a plain cancellation = %q %v", result.Status, result.Errors)
	}
}

func TestUnrejectRestoresTheProposalInPlace(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	if result := store.DecideProposal("ccdd0003", ProposalReject,
		[]string{"out of scope"}, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("reject = %q %v", result.Status, result.Errors)
	}
	result := store.UnrejectProposal("ccdd0003", "", sweepDay)
	if result.Status != MutationOK {
		t.Fatalf("unreject = %q %v", result.Status, result.Errors)
	}
	if result.Summary.From != "CANCELLED" || result.Summary.To != "PROPOSED" {
		t.Errorf("summary = %+v", result.Summary)
	}
	if len(result.TouchedIDs) != 1 || result.TouchedIDs[0] != "ccdd0003" {
		t.Errorf("touched = %v, want the SAME id back", result.TouchedIDs)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("state") != "PROPOSED" {
		t.Errorf("state = %q, want PROPOSED", child.String("state"))
	}
	if child.String("id") != "ccdd0003" {
		t.Errorf("id = %q — a restore must never mint a new one", child.String("id"))
	}
	if child.String("closed") != "" || child.String("rejected") != "" {
		t.Errorf("closed = %q, rejected = %q — both belong to the decision being undone",
			child.String("closed"), child.String("rejected"))
	}
	if child.String("body") != "out of scope" {
		t.Errorf("body = %q — the withdrawal note is history, not a mistake", child.String("body"))
	}
	if !check.Check(store.org).OK() {
		t.Error("a restored proposal must leave a store check accepts")
	}

	// Undo puts the decision back exactly as it was, marker included; redo
	// restores again. A restore is an ordinary journaled write.
	if outcome, label := store.HistoryStep(-1); outcome != HistoryOK ||
		label != "unreject proposal: Child proposal" {
		t.Fatalf("undo = %q %q", outcome, label)
	}
	undone, _ := recordFor(t, store.org, "Child proposal")
	if undone.String("state") != "CANCELLED" || undone.String("rejected") != sweepDay {
		t.Errorf("undo of a restore = %q/%q, want the decline back",
			undone.String("state"), undone.String("rejected"))
	}
	if outcome, _ := store.HistoryStep(1); outcome != HistoryOK {
		t.Fatalf("redo = %q", outcome)
	}
	redone, _ := recordFor(t, store.org, "Child proposal")
	if redone.String("state") != "PROPOSED" || redone.String("rejected") != "" {
		t.Errorf("redo of a restore = %q/%q", redone.String("state"), redone.String("rejected"))
	}
}

func TestUnrejectRefusesANonRejectedTarget(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	before := readStore(t, store)
	result := store.UnrejectProposal("ccdd0004", "", sweepDay)
	if result.Status != MutationInvalid ||
		result.FirstError() != "task is TODO, not a rejected proposal" {
		t.Errorf("unreject of a TODO = %q %v", result.Status, result.Errors)
	}
	if result := store.UnrejectProposal("nosuchid", "", sweepDay); result.Status != MutationNotFound {
		t.Errorf("unreject of a missing id = %q", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused restore writes nothing")
	}
}

// The marker is dropped by ANY write that leaves CANCELLED, not only by the
// restore verb — otherwise a plain state patch would leave a task listed as a
// standing decline forever, and `check` would refuse the file it produced.
func TestLeavingCancelledDropsTheDeclineMarker(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	if result := store.DecideProposal("ccdd0003", ProposalReject, nil, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("reject = %q %v", result.Status, result.Errors)
	}
	expected, _ := store.ExpectedFor("ccdd0003", FieldState)
	if result := store.PatchTask("ccdd0003", FieldState, "PROPOSED", expected, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("state patch = %q %v", result.Status, result.Errors)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("rejected") != "" {
		t.Errorf("rejected = %q, want cleared", child.String("rejected"))
	}
	if !check.Check(store.org).OK() {
		t.Error("check must accept the file after the marker is dropped")
	}
}
