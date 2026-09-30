package store

import (
	"strings"
	"testing"
)

// body_append composes the new body UNDER the lock, against whatever body is
// there — which is what lets it run without a per-field precondition on the
// changeset path and still never lose text.
func TestBodyAppendAddsALineInsideTheTransaction(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, store.ApplyChangeset(Changeset{
		ID: "aa000010", Today: "2026-06-10",
		Changes: []Change{{FieldBodyAppend, TextValue("Second line.")}},
	}))
	if got := line(t, store, "aa000010"); !strings.Contains(got, `"body":"A note.\nSecond line."`) {
		t.Errorf("appended body: %s", got)
	}
	// An empty body takes the text as the whole body, with no leading newline.
	mustOK(t, store.ApplyChangeset(Changeset{
		ID: "aa000011", Today: "2026-06-10",
		Changes: []Change{{FieldBodyAppend, TextValue("First.")}},
	}))
	if got := line(t, store, "aa000011"); !strings.Contains(got, `"body":"First."`) {
		t.Errorf("first note: %s", got)
	}
}

func TestBodyAppendRefusesEmptyTextAndABodyReplacement(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	before := readStore(t, store)
	empty := store.ApplyChangeset(Changeset{
		ID: "aa000010", Today: "2026-06-10",
		Changes: []Change{{FieldBodyAppend, TextValue("")}},
	})
	if empty.Status != MutationInvalid {
		t.Errorf("empty append: %q %v", empty.Status, empty.Errors)
	}
	both := store.ApplyChangeset(Changeset{
		ID: "aa000010", Today: "2026-06-10",
		Changes: []Change{{FieldBody, TextValue("x")}, {FieldBodyAppend, TextValue("y")}},
	})
	if both.Status != MutationInvalid || !strings.Contains(both.FirstError(), "body_append cannot be combined with body") {
		t.Errorf("body + body_append: %q %v", both.Status, both.Errors)
	}
	if readStore(t, store) != before {
		t.Error("a refused append wrote")
	}
}

// The single-field path keeps the note command's narrow check: the baseline
// is the body, so an append against a body that changed underneath refuses.
func TestBodyAppendPatchComparesTheBodyBaseline(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	baseline, found := store.ExpectedFor("aa000010", FieldBodyAppend)
	if !found || baseline != "A note." {
		t.Fatalf("baseline = %q, %v", baseline, found)
	}
	mustOK(t, patch(t, store, "aa000010", FieldBody, TextValue("Rewritten.")))
	stale := store.Patch(PatchRequest{
		ID: "aa000010", Field: FieldBodyAppend, Value: TextValue("late"),
		Expected: baseline, Today: "2026-06-10",
	})
	if stale.Status != MutationConflict {
		t.Errorf("status = %q, want conflict", stale.Status)
	}
}

// A DONE that rolls a recurring task reports where the anchor went, so a
// surface can say so without comparing two reads.
func TestRecurrenceRollReportsTheAnchorBeforeAndAfter(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	result := mustOK(t, applyChanges(t, store, "aa000013", Change{FieldState, TextValue("DONE")}))
	summary := result.Summary
	if summary.Action != ActionRecurrenceAdvanced || summary.TaskID != "aa000013" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.From != "2026-06-08" {
		t.Errorf("from = %q, want the pre-roll anchor", summary.From)
	}
	if summary.To != "2026-06-17" || !strings.Contains(line(t, store, "aa000013"), `"scheduled":"2026-06-17"`) {
		t.Errorf("to = %q; stored %s", summary.To, line(t, store, "aa000013"))
	}
}

// A lone activate carries the verb's own undo label, whichever surface sent it.
func TestLoneActivateIsLabelledAsTheVerb(t *testing.T) {
	ordered := []Change{{FieldActivate, BoolValue(true)}}
	if got := changesetLabel(ordered, "Deferred"); got != "activate: Deferred" {
		t.Errorf("label = %q", got)
	}
	mixed := []Change{{FieldTitle, TextValue("x")}, {FieldActivate, BoolValue(true)}}
	if got := changesetLabel(mixed, "Deferred"); got != "edit title, activate: Deferred" {
		t.Errorf("mixed label = %q", got)
	}
}
