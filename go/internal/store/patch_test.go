package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tasks-go/internal/temporal"
)

// patchFixture is the shape most of these tests need: one task per field
// combination the vocabulary has an opinion about, in one section.
const patchFixture = `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","priority":"C","title":"Plain task","tags":["@home","urgent","@errands"],"body":"A note."}
{"type":"task","id":"aa000011","parent":"aa000001","state":"TODO","title":"Scheduled","scheduled":"2026-06-15"}
{"type":"task","id":"aa000012","parent":"aa000001","state":"TODO","title":"Deadline with a lead","deadline":"2026-08-01","lead":"3w"}
{"type":"task","id":"aa000013","parent":"aa000001","state":"NEXT","title":"Recurring","scheduled":"2026-06-08","recur":".+1w"}
{"type":"task","id":"aa000014","parent":"aa000001","state":"INBOX","title":"Inbox item"}
{"type":"task","id":"aa000015","parent":"aa000001","state":"TODO","title":"Deferred","tags":["@home","defer","research"]}
{"type":"task","id":"aa000016","parent":"aa000001","state":"PROPOSED","title":"A proposal"}
{"type":"task","id":"aa000017","parent":"aa000001","state":"TODO","title":"Parent"}
{"type":"task","id":"aa000018","parent":"aa000017","state":"NEXT","title":"Child"}
`

// patch applies one field change through the whole transaction, reading the
// baseline the same way a caller would.
func patch(t *testing.T, store *Store, id string, field PatchField, value PatchValue) MutationResult {
	t.Helper()
	expected, _ := store.ExpectedFor(id, field)
	return store.Patch(PatchRequest{
		ID: id, Field: field, Value: value, Expected: expected, Today: "2026-06-10",
	})
}

// line is the stored record for an id, so an assertion can name the bytes it
// cares about without depending on the rest of the file.
func line(t *testing.T, store *Store, id string) string {
	t.Helper()
	for _, text := range strings.Split(readStore(t, store), "\n") {
		if strings.Contains(text, `"id":"`+id+`"`) {
			return text
		}
	}
	return ""
}

func mustOK(t *testing.T, result MutationResult) MutationResult {
	t.Helper()
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	return result
}

func mustInvalid(t *testing.T, result MutationResult, want string) {
	t.Helper()
	if result.Status != MutationInvalid {
		t.Fatalf("status = %q, errors = %v; want invalid", result.Status, result.Errors)
	}
	if result.FirstError() != want {
		t.Errorf("error = %q, want %q", result.FirstError(), want)
	}
}

// -- the vocabulary is closed --------------------------------------------------

// test_store_exposes_only_the_stable_patch_protocol_for_existing_tasks: the
// implemented set is exactly what PatchesField publishes, and a field outside it
// is refused rather than falling through to a write.
func TestPatchVocabularyIsClosedAndLocationRefuses(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	editFields := []PatchField{
		FieldTitle, FieldPriority, FieldDeferred, FieldScheduled, FieldDeadline,
		FieldRecurrence, FieldLead, FieldContexts, FieldTags, FieldBody, FieldState,
		FieldLocation,
	}
	for _, field := range editFields {
		if !store.PatchesField(field) {
			t.Errorf("PatchesField(%q) = false, want true", field)
		}
	}
	for _, field := range []PatchField{FieldTagDelta, FieldActivate, FieldDateClear} {
		if !store.PatchesField(field) {
			t.Errorf("PatchesField(%q) = false, want true", field)
		}
	}
	if store.PatchesField("nonsense") {
		t.Error("PatchesField(nonsense) = true, want false")
	}

	before := readStore(t, store)
	result := patch(t, store, "aa000010", "nonsense", TextValue("x"))
	if result.Status != MutationInvalid {
		t.Fatalf("unknown-field status = %q, want invalid", result.Status)
	}
	if !strings.Contains(result.FirstError(), "unknown editable field") {
		t.Errorf("unknown-field error = %q", result.FirstError())
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused field wrote bytes")
	}
}

// test_missing_and_invalid_field_are_typed_and_write_nothing.
func TestMissingAndInvalidFieldAreTypedAndWriteNothing(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	before := readStore(t, store)

	missing := patch(t, store, "no-such-id", FieldTitle, TextValue("x"))
	if missing.Status != MutationNotFound {
		t.Errorf("missing id status = %q, want not_found", missing.Status)
	}
	unknown := store.Patch(PatchRequest{
		ID: "aa000010", Field: "nonsense", Value: TextValue("x"), Today: "2026-06-10",
	})
	if unknown.Status != MutationInvalid {
		t.Errorf("unknown field status = %q, want invalid", unknown.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a typed refusal wrote bytes")
	}
}

// -- title, body, priority ------------------------------------------------------

func TestTitlePatchStripsAndRefusesBlank(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldTitle, TextValue("  Renamed  ")))
	if !strings.Contains(line(t, store, "aa000010"), `"title":"Renamed"`) {
		t.Errorf("title not stripped: %s", line(t, store, "aa000010"))
	}
	mustInvalid(t, patch(t, store, "aa000010", FieldTitle, TextValue("   ")), "title cannot be blank")
	mustInvalid(t, patch(t, store, "aa000010", FieldTitle, NoValue()), "title must be text")
	mustInvalid(t, patch(t, store, "aa000010", FieldTitle, BoolValue(true)), "title must be text")
}

// Ruby's String#strip removes ASCII whitespace and NUL only; a non-breaking
// space is part of the title. Go's strings.TrimSpace would eat it.
func TestTitleStripIsRubysNotUnicodes(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldTitle, TextValue(" held ")))
	if !strings.Contains(line(t, store, "aa000010"), `"title":" held "`) {
		t.Errorf("non-breaking space was stripped: %s", line(t, store, "aa000010"))
	}
}

// test_body_replacement_preserves_exact_whitespace_and_newlines.
func TestBodyReplacementPreservesExactWhitespaceAndNewlines(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldBody, TextValue("one\n\n  two  \n")))
	if !strings.Contains(line(t, store, "aa000010"), `"body":"one\n\n  two  \n"`) {
		t.Errorf("body bytes: %s", line(t, store, "aa000010"))
	}
	// An empty body is an ABSENT key, not a present empty string.
	mustOK(t, patch(t, store, "aa000010", FieldBody, TextValue("")))
	if strings.Contains(line(t, store, "aa000010"), `"body"`) {
		t.Errorf("empty body kept the key: %s", line(t, store, "aa000010"))
	}
	mustInvalid(t, patch(t, store, "aa000010", FieldBody, NoValue()), "body must be text")
}

func TestPriorityAcceptsTheVocabularyAndClearsOnNil(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustInvalid(t, patch(t, store, "aa000010", FieldPriority, TextValue("D")),
		"priority must be A, B, C, or nil")
	// The empty STRING is not nil: Ruby refuses it, because "" is not a priority.
	mustInvalid(t, patch(t, store, "aa000010", FieldPriority, TextValue("")),
		"priority must be A, B, C, or nil")
	mustOK(t, patch(t, store, "aa000010", FieldPriority, NoValue()))
	if strings.Contains(line(t, store, "aa000010"), `"priority"`) {
		t.Errorf("priority survived a clear: %s", line(t, store, "aa000010"))
	}
}

// -- tag slices ------------------------------------------------------------------

// test_priority_deferred_context_and_tag_slices_merge_without_erasing_each_other,
// and test_changed_contexts_preserve_interleaved_unowned_tag_placement.
func TestTagSlicesMergeInPlaceWithoutErasingEachOther(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldContexts, ListValue([]string{"@office"})))
	if !strings.Contains(line(t, store, "aa000010"), `"tags":["@office","urgent"]`) {
		t.Errorf("contexts merge: %s", line(t, store, "aa000010"))
	}
	mustOK(t, patch(t, store, "aa000010", FieldTags, ListValue([]string{"calm", "later"})))
	if !strings.Contains(line(t, store, "aa000010"), `"tags":["@office","calm","later"]`) {
		t.Errorf("tag merge: %s", line(t, store, "aa000010"))
	}
	// The defer marker is owned by neither slice and keeps its place.
	store2, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store2, "aa000015", FieldTags, ListValue([]string{"reading"})))
	if !strings.Contains(line(t, store2, "aa000015"), `"tags":["@home","defer","reading"]`) {
		t.Errorf("defer marker moved: %s", line(t, store2, "aa000015"))
	}
}

// test_invalid_tag_slice_is_typed_and_atomic.
func TestInvalidTagSliceIsTypedAndAtomic(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	before := readStore(t, store)
	for _, testCase := range []struct {
		name  string
		field PatchField
		value PatchValue
		want  string
	}{
		{"context without @", FieldContexts, ListValue([]string{"office"}), "invalid contexts tag"},
		{"bare @", FieldContexts, ListValue([]string{"@"}), "invalid contexts tag"},
		{"duplicate context", FieldContexts, ListValue([]string{"@a", "@a"}), "duplicate contexts tag"},
		{"context in the tag slice", FieldTags, ListValue([]string{"@x"}), "invalid tags tag"},
		{"defer in the tag slice", FieldTags, ListValue([]string{"defer"}), "invalid tags tag"},
		{"empty tag", FieldTags, ListValue([]string{""}), "invalid tags tag"},
		{"duplicate tag", FieldTags, ListValue([]string{"a", "a"}), "duplicate tags tag"},
		{"not a list", FieldTags, TextValue("a"), "tags must be a list of tags"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mustInvalid(t, patch(t, store, "aa000010", testCase.field, testCase.value), testCase.want)
		})
	}
	if got := readStore(t, store); got != before {
		t.Error("an invalid slice wrote bytes")
	}
}

// test_interleaved_tag_slice_noops_preserve_exact_bytes_and_history: a slice
// set to what it already holds writes nothing at all.
func TestInterleavedTagSliceNoopsPreserveExactBytes(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	before := readStore(t, store)
	result := patch(t, store, "aa000010", FieldContexts, ListValue([]string{"@home", "@errands"}))
	if result.Status != MutationNoChange {
		t.Fatalf("status = %q, want no_change", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a no-op slice rewrote the file")
	}
}

// The CLI `tag` verb owns the whole ordered sequence in one undoable write.
func TestTagDeltaOwnsTheWholeSequence(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldTagDelta,
		TagDeltaValue([]string{"defer", "@office"}, []string{"urgent"})))
	if !strings.Contains(line(t, store, "aa000010"), `"tags":["@home","@errands","defer","@office"]`) {
		t.Errorf("tag delta: %s", line(t, store, "aa000010"))
	}
	mustInvalid(t, patch(t, store, "aa000010", FieldTagDelta, TextValue("x")),
		"tag changes must contain add and remove lists")
}

func TestDeferredTogglesTheMarkerAndNothingElse(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldDeferred, BoolValue(true)))
	if !strings.Contains(line(t, store, "aa000010"), `"tags":["@home","urgent","@errands","defer"]`) {
		t.Errorf("defer added: %s", line(t, store, "aa000010"))
	}
	if patch(t, store, "aa000010", FieldDeferred, BoolValue(true)).Status != MutationNoChange {
		t.Error("re-deferring wrote")
	}
	mustOK(t, patch(t, store, "aa000010", FieldDeferred, BoolValue(false)))
	if strings.Contains(line(t, store, "aa000010"), `"defer"`) {
		t.Errorf("defer survived: %s", line(t, store, "aa000010"))
	}
	mustInvalid(t, patch(t, store, "aa000010", FieldDeferred, TextValue("true")),
		"deferred must be true or false")
}

// Clearing the last tag deletes the key rather than storing an empty array.
func TestClearingTheLastTagDeletesTheKey(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000015", FieldContexts, ListValue([]string{})))
	mustOK(t, patch(t, store, "aa000015", FieldTags, ListValue([]string{})))
	mustOK(t, patch(t, store, "aa000015", FieldDeferred, BoolValue(false)))
	if strings.Contains(line(t, store, "aa000015"), `"tags"`) {
		t.Errorf("empty tag list kept the key: %s", line(t, store, "aa000015"))
	}
}

// -- dates -----------------------------------------------------------------------

// test_date_patch_promotes_inbox_and_clearing_final_date_retires_recurrence.
func TestDatePatchPromotesInboxAndClearingFinalDateRetiresRecurrence(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	date, _ := temporal.ParseDate("2026-07-01")
	value, err := temporal.NewValue(date, "", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, patch(t, store, "aa000014", FieldScheduled, TemporalValue(value)))
	if !strings.Contains(line(t, store, "aa000014"), `"state":"TODO"`) {
		t.Errorf("INBOX not promoted: %s", line(t, store, "aa000014"))
	}

	mustOK(t, patch(t, store, "aa000013", FieldScheduled, NoValue()))
	stored := line(t, store, "aa000013")
	if strings.Contains(stored, `"recur"`) || strings.Contains(stored, `"scheduled"`) {
		t.Errorf("a dateless task kept its cookie: %s", stored)
	}
}

func TestTimedDateWritesTheStampAndClearingRemovesBoth(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	date, _ := temporal.ParseDate("2026-07-01")
	value, err := temporal.NewValue(date, "09:30", "Europe/London", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, patch(t, store, "aa000011", FieldScheduled, TemporalValue(value)))
	stored := line(t, store, "aa000011")
	if !strings.Contains(stored, `"scheduled":"2026-07-01"`) ||
		!strings.Contains(stored, `"scheduled_time":{"local":"09:30","timezone":"Europe/London"}`) {
		t.Errorf("timed date: %s", stored)
	}
	mustOK(t, patch(t, store, "aa000011", FieldScheduled, NoValue()))
	if strings.Contains(line(t, store, "aa000011"), "scheduled") {
		t.Errorf("clearing left a time half: %s", line(t, store, "aa000011"))
	}
}

// A timed expectation covers the WHOLE stamp, so a zone-only change invalidates
// a date baseline that a bare ISO comparison would have accepted.
func TestTimedBaselineCoversTheZoneNotJustTheDate(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	date, _ := temporal.ParseDate("2026-07-01")
	london, _ := temporal.NewValue(date, "09:30", "Europe/London", 0, false)
	mustOK(t, patch(t, store, "aa000011", FieldScheduled, TemporalValue(london)))

	stale := "2026-07-01"
	newYork, _ := temporal.NewValue(date, "09:30", "America/New_York", 0, false)
	result := store.Patch(PatchRequest{
		ID: "aa000011", Field: FieldScheduled, Value: TemporalValue(newYork),
		Expected: stale, Today: "2026-06-10",
	})
	if result.Status != MutationConflict {
		t.Errorf("status = %q, want conflict on a date-only baseline", result.Status)
	}
}

// `undate` owns both dates and the cookie they anchor, in one write.
func TestDateClearOwnsBothDatesAndRefusesWhenThereIsNothingToClear(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustInvalid(t, patch(t, store, "aa000010", FieldDateClear, NoValue()), "no matching date stamp")
	mustInvalid(t, patch(t, store, "aa000011", FieldDateClear, TextValue("deadline")),
		"no matching date stamp")
	mustInvalid(t, patch(t, store, "aa000011", FieldDateClear, TextValue("bogus")),
		"date clear kind must be deadline, scheduled, or nil")
	mustOK(t, patch(t, store, "aa000013", FieldDateClear, NoValue()))
	stored := line(t, store, "aa000013")
	if strings.Contains(stored, "scheduled") || strings.Contains(stored, `"recur"`) {
		t.Errorf("undate left an intent behind: %s", stored)
	}
}

// -- lead ------------------------------------------------------------------------

func TestLeadNeedsAnAnchorAndOnlyOneTimedGate(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustInvalid(t, patch(t, store, "aa000010", FieldLead, TextValue("3w")),
		"a lead time needs a date to hide before — add a deadline or an available-from date first")
	mustInvalid(t, patch(t, store, "aa000011", FieldLead, TextValue("bogus")),
		`invalid lead time "bogus" (expected a span like 3w, 2d, 1m, 1y)`)

	// Rule 3 from both directions: setting the lead beside two dates, and
	// adding the second date beside a lead.
	date, _ := temporal.ParseDate("2026-06-20")
	value, _ := temporal.NewValue(date, "", "", 0, false)
	mustOK(t, patch(t, store, "aa000011", FieldDeadline, TemporalValue(value)))
	if result := patch(t, store, "aa000011", FieldLead, TextValue("3w")); result.Status != MutationInvalid ||
		!strings.Contains(result.FirstError(), "second, ignored gate") {
		t.Errorf("two dates + lead: %q %v", result.Status, result.Errors)
	}
	early, _ := temporal.ParseDate("2026-07-01")
	scheduled, _ := temporal.NewValue(early, "", "", 0, false)
	if result := patch(t, store, "aa000012", FieldScheduled, TemporalValue(scheduled)); result.Status != MutationInvalid ||
		!strings.Contains(result.FirstError(), "second, ignored gate") {
		t.Errorf("lead + second date: %q %v", result.Status, result.Errors)
	}
}

// Rule 5: a window that would open outside the storable years is refused rather
// than written and then rolled back forever.
func TestLeadRefusesAGateOutsideTheStorableYears(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Early","deadline":"0001-01-05"}
`)
	result := patch(t, store, "aa000010", FieldLead, TextValue("9999y"))
	if result.Status != MutationInvalid ||
		!strings.Contains(result.FirstError(), "outside the four-digit years") {
		t.Errorf("status = %q, errors = %v", result.Status, result.Errors)
	}
}

// Clearing is always allowed, and it takes lead_skip with it.
func TestClearingTheLeadRetiresTheReleasedOccurrence(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Released","scheduled":"2026-06-22","lead":"2d","lead_skip":"2026-06-22"}
`)
	mustOK(t, patch(t, store, "aa000010", FieldLead, NoValue()))
	stored := line(t, store, "aa000010")
	if strings.Contains(stored, "lead") {
		t.Errorf("lead or lead_skip survived: %s", stored)
	}
}

// A new window supersedes an occurrence a previous one released early.
func TestANewLeadWindowSupersedesTheReleasedOccurrence(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Released","scheduled":"2026-06-22","lead":"2d","lead_skip":"2026-06-22"}
`)
	mustOK(t, patch(t, store, "aa000010", FieldLead, TextValue("1w")))
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"lead":"1w"`) || strings.Contains(stored, "lead_skip") {
		t.Errorf("lead replace: %s", stored)
	}
}

// -- recurrence --------------------------------------------------------------------

// test_recurrence_patch_validates_cookie_and_fresh_dates.
func TestRecurrencePatchValidatesCookieAndFreshDates(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustInvalid(t, patch(t, store, "aa000011", FieldRecurrence, TextValue("nonsense")),
		"invalid recurrence cookie")
	mustInvalid(t, patch(t, store, "aa000010", FieldRecurrence, TextValue(".+1w")),
		"recurrence requires a scheduled date or deadline")
	// The anchor requirement holds for CLEARING too — Ruby checks it first.
	mustInvalid(t, patch(t, store, "aa000010", FieldRecurrence, NoValue()),
		"recurrence requires a scheduled date or deadline")
	mustInvalid(t, patch(t, store, "aa000016", FieldRecurrence, TextValue(".+1w")),
		"can't set recurrence on a PROPOSED task")
	mustOK(t, patch(t, store, "aa000011", FieldRecurrence, TextValue(".+1w")))
	if !strings.Contains(line(t, store, "aa000011"), `"recur":".+1w"`) {
		t.Errorf("cookie: %s", line(t, store, "aa000011"))
	}
}

// A cookie can parse and still leave a task nothing can ever complete.
func TestUnreachableAndUnstorableCookiesRefuseUpFront(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Odd year","scheduled":"2027-02-01"}
{"type":"task","id":"aa000011","parent":"aa000001","state":"TODO","title":"Far out","scheduled":"2026-06-01"}
`)
	if result := patch(t, store, "aa000010", FieldRecurrence, TextValue("2y:02:5fri")); result.Status != MutationInvalid {
		t.Errorf("unreachable cookie status = %q, errors = %v", result.Status, result.Errors)
	}
	if result := patch(t, store, "aa000011", FieldRecurrence, TextValue("+9999y")); result.Status != MutationInvalid ||
		!strings.Contains(result.FirstError(), "outside the four-digit years") {
		t.Errorf("unstorable cookie status = %q, errors = %v", result.Status, result.Errors)
	}
}

// -- state -------------------------------------------------------------------------

// test_state_patch_cascades_and_uses_lifecycle_fingerprint.
func TestStatePatchCascadesToOpenDescendants(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	result := mustOK(t, patch(t, store, "aa000017", FieldState, TextValue("DONE")))
	if len(result.TouchedIDs) != 2 {
		t.Errorf("touched = %v, want the parent and the child", result.TouchedIDs)
	}
	for _, id := range []string{"aa000017", "aa000018"} {
		if !strings.Contains(line(t, store, id), `"closed":"2026-06-10"`) {
			t.Errorf("%s not closed: %s", id, line(t, store, id))
		}
	}
}

// test_cascade_helper_returns_stable_ids_not_file_coordinates.
func TestCascadeReturnsStableIDsNotFileCoordinates(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	result := mustOK(t, patch(t, store, "aa000017", FieldState, TextValue("DONE")))
	for _, id := range result.TouchedIDs {
		if len(id) != 8 {
			t.Errorf("touched id %q is not a stable id", id)
		}
	}
}

// The lifecycle fingerprint is what a cascade is guarded by: a change ANYWHERE
// in the affected subtree invalidates the baseline.
// test_state_conflicts_when_affected_descendant_lifecycle_changes.
func TestStateConflictsWhenAffectedDescendantLifecycleChanges(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	expected, ok := store.ExpectedFor("aa000017", FieldState)
	if !ok {
		t.Fatal("no baseline")
	}
	mustOK(t, patch(t, store, "aa000018", FieldState, TextValue("WAITING")))
	result := store.Patch(PatchRequest{
		ID: "aa000017", Field: FieldState, Value: TextValue("DONE"),
		Expected: expected, Today: "2026-06-10",
	})
	if result.Status != MutationConflict {
		t.Errorf("status = %q, want conflict", result.Status)
	}
}

// test_state_adopts_an_unrelated_body_change: the fingerprint covers lifecycle,
// not every field, so an unrelated edit does NOT invalidate it.
func TestStateAdoptsAnUnrelatedBodyChange(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	expected, _ := store.ExpectedFor("aa000017", FieldState)
	mustOK(t, patch(t, store, "aa000018", FieldBody, TextValue("a note")))
	result := store.Patch(PatchRequest{
		ID: "aa000017", Field: FieldState, Value: TextValue("DONE"),
		Expected: expected, Today: "2026-06-10",
	})
	if result.Status != MutationOK {
		t.Errorf("status = %q, errors = %v; want ok", result.Status, result.Errors)
	}
}

func TestProposedTransitionsGuardTheirPreconditions(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustInvalid(t, patch(t, store, "aa000013", FieldState, TextValue("PROPOSED")),
		"remove recurrence before setting PROPOSED")
	mustInvalid(t, patch(t, store, "aa000016", FieldState, TextValue("DONE")),
		"approve the proposal before completing it")
	mustInvalid(t, patch(t, store, "aa000017", FieldState, TextValue("PROPOSED")),
		"cannot set PROPOSED while accepted descendants remain")

	delegated, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Handed off","delegation":{"kind":"human","status":"delegated","assignee":"sam@example.com","at":"2026-06-01T10:00:00Z"}}
`)
	mustInvalid(t, patch(t, delegated, "aa000010", FieldState, TextValue("PROPOSED")),
		"undelegate before setting PROPOSED")
}

// -- recurrence advance --------------------------------------------------------------

// test_state_patch_advances_recurrence_without_cascade: completing a recurring
// task is a ROLL, not a close.
func TestStatePatchAdvancesRecurrenceWithoutClosing(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	result := mustOK(t, patch(t, store, "aa000013", FieldState, TextValue("DONE")))
	stored := line(t, store, "aa000013")
	if !strings.Contains(stored, `"state":"NEXT"`) {
		t.Errorf("state changed on a roll: %s", stored)
	}
	if !strings.Contains(stored, `"scheduled":"2026-06-17"`) {
		t.Errorf("anchor did not roll: %s", stored)
	}
	if strings.Contains(stored, `"closed"`) {
		t.Errorf("a rolled task was closed: %s", stored)
	}
	if !strings.Contains(stored, `- Did [2026-06-10].`) {
		t.Errorf("completion not noted in the body: %s", stored)
	}
	if result.Summary.Action != "recurrence_advanced" {
		t.Errorf("summary action = %q", result.Summary.Action)
	}
}

// The roll keeps the paired date's offset, so a task with both dates stays the
// same length rather than collapsing onto one day.
func TestRecurrenceRollKeepsThePairedDateOffset(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Window","scheduled":"2026-06-05","deadline":"2026-06-08","recur":".+1w"}
`)
	mustOK(t, patch(t, store, "aa000010", FieldState, TextValue("DONE")))
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"scheduled":"2026-06-14"`) ||
		!strings.Contains(stored, `"deadline":"2026-06-17"`) {
		t.Errorf("offset not preserved: %s", stored)
	}
}

// A fresh occurrence is NEW work: the claim and the work reference belong to the
// cycle that just finished, and only the standing intent carries over.
func TestRecurrenceRollReArmsTheDelegation(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Agent work","scheduled":"2026-06-08","recur":".+1w","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"worker-1","at":"2026-06-01T10:00:00Z","work_ref":"https://example.invalid/1"}}
{"type":"task","id":"aa000011","parent":"aa000001","state":"TODO","title":"Human work","scheduled":"2026-06-08","recur":".+1w","delegation":{"kind":"human","status":"delegated","assignee":"sam@example.com","at":"2026-06-01T10:00:00Z"}}
`)
	mustOK(t, patch(t, store, "aa000010", FieldState, TextValue("DONE")))
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"status":"ready"`) || strings.Contains(stored, "worker-1") ||
		strings.Contains(stored, "work_ref") {
		t.Errorf("agent claim carried over: %s", stored)
	}
	mustOK(t, patch(t, store, "aa000011", FieldState, TextValue("DONE")))
	human := line(t, store, "aa000011")
	if !strings.Contains(human, `"status":"delegated"`) ||
		!strings.Contains(human, "sam@example.com") {
		t.Errorf("human delegation lost: %s", human)
	}
}

// The roll moved the anchor, so an occurrence released early is history and the
// defer marker goes with the finished cycle.
func TestRecurrenceRollRetiresLeadSkipAndDefer(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Released","scheduled":"2026-06-08","recur":".+1w","lead":"2d","lead_skip":"2026-06-08","tags":["defer","@home"]}
`)
	mustOK(t, patch(t, store, "aa000010", FieldState, TextValue("DONE")))
	stored := line(t, store, "aa000010")
	if strings.Contains(stored, "lead_skip") || strings.Contains(stored, `"defer"`) {
		t.Errorf("stale release or defer survived the roll: %s", stored)
	}
	if !strings.Contains(stored, `"lead":"2d"`) {
		t.Errorf("the window itself was dropped: %s", stored)
	}
}

// CANCELLING a recurring task closes it: only DONE rolls.
func TestCancellingARecurringTaskClosesItRatherThanRolling(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000013", FieldState, TextValue("CANCELLED")))
	stored := line(t, store, "aa000013")
	if !strings.Contains(stored, `"state":"CANCELLED"`) || !strings.Contains(stored, `"closed"`) {
		t.Errorf("cancel rolled instead of closing: %s", stored)
	}
}

// A roll that would leave the storable years refuses BEFORE writing, where Ruby
// writes and rolls back. See porting/intentional-differences.md.
func TestRecurrenceRollOutOfRangeRefusesWithoutWriting(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Last year","scheduled":"9999-12-31","recur":"+1y"}
`)
	before := readStore(t, store)
	result := patch(t, store, "aa000010", FieldState, TextValue("DONE"))
	if result.Status != MutationInvalid {
		t.Errorf("status = %q, errors = %v; want invalid", result.Status, result.Errors)
	}
	if result.RolledBack {
		t.Error("refusing before the write should not report a rollback")
	}
	if got := readStore(t, store); got != before {
		t.Error("an out-of-range roll wrote bytes")
	}
}

// -- activate ------------------------------------------------------------------------

func TestActivateClearsDeferAndAFutureAvailableFromDate(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Later","scheduled":"2026-08-08","tags":["defer","@home"]}
`)
	mustOK(t, patch(t, store, "aa000010", FieldActivate, BoolValue(true)))
	stored := line(t, store, "aa000010")
	if strings.Contains(stored, `"defer"`) || strings.Contains(stored, "scheduled") {
		t.Errorf("activate left a hold: %s", stored)
	}
	if !strings.Contains(stored, `"@home"`) {
		t.Errorf("activate ate an unrelated tag: %s", stored)
	}
	mustInvalid(t, patch(t, store, "aa000010", FieldActivate, BoolValue(false)), "activate must be true")
}

// A LEAD task releases the CURRENT OCCURRENCE and keeps every date it has: the
// anchor is what the next window is measured from.
func TestActivateReleasesOneOccurrenceOfALeadTask(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Hidden","deadline":"2026-08-08","lead":"1w"}
`)
	mustOK(t, patch(t, store, "aa000010", FieldActivate, BoolValue(true)))
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"deadline":"2026-08-08"`) ||
		!strings.Contains(stored, `"lead_skip":"2026-08-08"`) {
		t.Errorf("lead release: %s", stored)
	}
}

// Activation owns availability, not the recurrence contract.
func TestActivatePreservesRecurrence(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Weekly later","scheduled":"2026-08-08","recur":".+1w"}
`)
	mustOK(t, patch(t, store, "aa000010", FieldActivate, BoolValue(true)))
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"recur":".+1w"`) {
		t.Errorf("activate discarded the cookie: %s", stored)
	}
	if strings.Contains(stored, `"scheduled"`) {
		t.Errorf("activate kept a future date: %s", stored)
	}
}

// -- the transaction ------------------------------------------------------------------

// test_no_change_writes_no_bytes_and_records_no_history.
func TestNoChangeWritesNoBytesAndRecordsNoHistory(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	before := readStore(t, store)
	result := patch(t, store, "aa000010", FieldTitle, TextValue("Plain task"))
	if result.Status != MutationNoChange {
		t.Fatalf("status = %q, want no_change", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a no-op rewrote the file")
	}
	if _, err := os.Stat(filepath.Join(root, "journal", "index.json")); err == nil {
		t.Error("a no-op recorded history")
	}
}

// test_title_patch_conflicts_only_with_the_title_slice.
func TestPatchConflictsOnlyWithItsOwnSlice(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	expected, _ := store.ExpectedFor("aa000010", FieldTitle)
	mustOK(t, patch(t, store, "aa000010", FieldBody, TextValue("unrelated")))
	if result := (store.Patch(PatchRequest{
		ID: "aa000010", Field: FieldTitle, Value: TextValue("Renamed"),
		Expected: expected, Today: "2026-06-10",
	})); result.Status != MutationOK {
		t.Errorf("an unrelated edit invalidated a title baseline: %q", result.Status)
	}

	fresh, _ := store.ExpectedFor("aa000010", FieldTitle)
	mustOK(t, patch(t, store, "aa000010", FieldTitle, TextValue("Renamed again")))
	if result := (store.Patch(PatchRequest{
		ID: "aa000010", Field: FieldTitle, Value: TextValue("Third"),
		Expected: fresh, Today: "2026-06-10",
	})); result.Status != MutationConflict {
		t.Errorf("a stale title baseline was accepted: %q", result.Status)
	}
}

// test_successful_patch_is_one_undoable_checked_write: the label a patch records
// names the field and the task when the caller supplies none.
func TestPatchRecordsOneUndoableStepWithADefaultLabel(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldTitle, TextValue("Renamed")))
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"label": "edit title: Plain task"`) {
		t.Errorf("journal label: %s", raw)
	}
}

// Two byte-contiguous patches sharing one coalesce key are ONE undo step, which
// is what keeps a composed application operation from costing the user two.
// test_byte_contiguous_patches_with_one_session_key_are_one_undo_step.
func TestCoalescedPatchesAreOneUndoStep(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	first, _ := store.ExpectedFor("aa000010", FieldTitle)
	if result := store.PatchTaskCoalesced("aa000010", FieldTitle, "One", first, "edit", "2026-06-10",
		"session-1"); result.Status != MutationOK {
		t.Fatalf("first: %q %v", result.Status, result.Errors)
	}
	second, _ := store.ExpectedFor("aa000010", FieldTitle)
	if result := store.PatchTaskCoalesced("aa000010", FieldTitle, "Two", second, "edit", "2026-06-10",
		"session-1"); result.Status != MutationOK {
		t.Fatalf("second: %q %v", result.Status, result.Errors)
	}
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"label"`) != 1 {
		t.Errorf("coalesced patches produced more than one step:\n%s", raw)
	}
}

// test_nil_and_mismatched_keys_keep_separate_patch_entries.
func TestMismatchedCoalesceKeysKeepSeparateEntries(t *testing.T) {
	store, root := writerFixture(t, patchFixture)
	first, _ := store.ExpectedFor("aa000010", FieldTitle)
	store.PatchTaskCoalesced("aa000010", FieldTitle, "One", first, "edit", "2026-06-10", "session-1")
	second, _ := store.ExpectedFor("aa000010", FieldTitle)
	store.PatchTaskCoalesced("aa000010", FieldTitle, "Two", second, "edit", "2026-06-10", "session-2")
	raw, err := os.ReadFile(filepath.Join(root, "journal", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"label"`) != 2 {
		t.Errorf("distinct keys were coalesced:\n%s", raw)
	}
}

// test_malformed_file_is_rejected_before_write.
func TestMalformedFileIsRejectedBeforeWrite(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"NOPE","title":"Bad state"}
{"type":"task","id":"aa000011","parent":"aa000001","state":"BAD","title":"Also bad"}
`)
	before := readStore(t, store)
	result := patch(t, store, "aa000010", FieldTitle, TextValue("Renamed"))
	if result.Status != MutationStoreInvalid {
		t.Errorf("status = %q, want store_invalid", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused patch wrote to an invalid store")
	}
}

// A field-owned patch MAY fix its own invalid record — and only its own. Two
// broken records put the file out of scope for either of them.
func TestTargetedRepairFixesItsOwnRecordAndOnlyThat(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Broken lead","deadline":"2026-08-01","lead":"nonsense"}
`)
	result := store.Patch(PatchRequest{
		ID: "aa000010", Field: FieldLead, Value: NoValue(), Today: "2026-06-10",
	})
	if result.Status != MutationOK {
		t.Fatalf("repair status = %q, errors = %v", result.Status, result.Errors)
	}
	if strings.Contains(line(t, store, "aa000010"), `"lead":`) {
		t.Errorf("repair did not clear the bad value: %s", line(t, store, "aa000010"))
	}

	two, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Broken lead","deadline":"2026-08-01","lead":"nonsense"}
{"type":"task","id":"aa000011","parent":"aa000001","state":"NOPE","title":"Broken state"}
`)
	before := readStore(t, two)
	if result := (two.Patch(PatchRequest{
		ID: "aa000010", Field: FieldLead, Value: NoValue(), Today: "2026-06-10",
	})); result.Status != MutationStoreInvalid {
		t.Errorf("status = %q, want store_invalid with a second broken record", result.Status)
	}
	if got := readStore(t, two); got != before {
		t.Error("an out-of-scope repair wrote bytes")
	}
}

// A repair whose patch does NOT fix the file writes, fails validation, and
// restores the exact prior bytes.
// test_post_write_check_failure_rolls_back_and_records_no_history.
func TestRepairThatDoesNotFixTheFileRollsBackAndRecordsNoHistory(t *testing.T) {
	store, root := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Broken lead","deadline":"2026-08-01","lead":"nonsense"}
`)
	before := readStore(t, store)
	result := store.Patch(PatchRequest{
		ID: "aa000010", Field: FieldTitle, Value: TextValue("Renamed"), Today: "2026-06-10",
	})
	if result.Status != MutationStoreInvalid || !result.RolledBack {
		t.Fatalf("status = %q rolled_back = %v", result.Status, result.RolledBack)
	}
	if result.RollbackStage != RollbackValidation {
		t.Errorf("stage = %q, want validation", result.RollbackStage)
	}
	if got := readStore(t, store); got != before {
		t.Errorf("rollback did not restore the bytes\n got %q\nwant %q", got, before)
	}
	if _, err := os.Stat(filepath.Join(root, "journal", "index.json")); err == nil {
		t.Error("a rolled-back write recorded history")
	}
	reason, stage := store.LastRollback()
	if reason == "" || stage != RollbackValidation {
		t.Errorf("LastRollback = %q, %q", reason, stage)
	}
}

// The rollback record is cleared by the next clean mutation, so a caller that
// reads it after a success is not told about a failure two writes ago.
func TestLastRollbackClearsOnTheNextCleanWrite(t *testing.T) {
	store, _ := writerFixture(t, patchFixture)
	mustOK(t, patch(t, store, "aa000010", FieldTitle, TextValue("Renamed")))
	if reason, stage := store.LastRollback(); reason != "" || stage != "" {
		t.Errorf("LastRollback = %q, %q after a clean write", reason, stage)
	}
}

// -- delegation verbs ------------------------------------------------------------

func TestUndelegateClearsTheMarkerAndReportsWhatItReplaced(t *testing.T) {
	store, _ := writerFixture(t, `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Handed off","delegation":{"kind":"human","status":"delegated","assignee":"sam@example.com","at":"2026-06-01T10:00:00Z"}}
{"type":"task","id":"aa000011","parent":"aa000001","state":"TODO","title":"Plain"}
`)
	result := store.Undelegate("aa000010", "")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !strings.Contains(result.Summary.Previous, "sam@example.com") {
		t.Errorf("Summary.Previous = %q", result.Summary.Previous)
	}
	if strings.Contains(line(t, store, "aa000010"), "delegation") {
		t.Errorf("marker survived: %s", line(t, store, "aa000010"))
	}
	if got := store.Undelegate("aa000011", ""); got.Status != MutationNoChange {
		t.Errorf("undelegating an undelegated task = %q", got.Status)
	}
}

func TestReleaseRequiresTheHoldersIDUnlessForced(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Claimed","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"worker-1","at":"2026-06-01T10:00:00Z"}}
`
	store, _ := writerFixture(t, fixture)
	if result := store.Release("aa000010", "worker-2", false, ""); result.Status != MutationConflict {
		t.Errorf("status = %q, want conflict for a foreign worker", result.Status)
	}
	if result := store.Release("aa000010", "worker-1", false, ""); result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	stored := line(t, store, "aa000010")
	if !strings.Contains(stored, `"status":"ready"`) || strings.Contains(stored, "worker-1") {
		t.Errorf("release: %s", stored)
	}

	forced, _ := writerFixture(t, fixture)
	if result := forced.Release("aa000010", "", true, ""); result.Status != MutationOK {
		t.Errorf("forced release status = %q, errors = %v", result.Status, result.Errors)
	}
	unclaimed, _ := writerFixture(t, patchFixture)
	if result := unclaimed.Release("aa000010", "", true, ""); result.Status != MutationInvalid ||
		result.FirstError() != "task is not claimed" {
		t.Errorf("status = %q, errors = %v", result.Status, result.Errors)
	}
}

func TestWorkRefIsOwnedByTheOwnerAndTheMatchingWorker(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"aa000001","title":"Inbox"}
{"type":"task","id":"aa000010","parent":"aa000001","state":"TODO","title":"Claimed","delegation":{"kind":"agent","mode":"implement","status":"claimed","assignee":"worker-1","at":"2026-06-01T10:00:00Z"}}
{"type":"task","id":"aa000011","parent":"aa000001","state":"TODO","title":"Plain"}
`
	store, _ := writerFixture(t, fixture)
	if result := store.SetWorkRef("aa000010", "https://example.invalid/1", "worker-2", ""); result.Status != MutationConflict {
		t.Errorf("status = %q, want conflict for a foreign worker", result.Status)
	}
	if result := store.SetWorkRef("aa000010", "https://example.invalid/1", "worker-1", ""); result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !strings.Contains(line(t, store, "aa000010"), `"work_ref":"https://example.invalid/1"`) {
		t.Errorf("work_ref: %s", line(t, store, "aa000010"))
	}
	// The `at` stamp is untouched: a reference is not a status transition.
	if !strings.Contains(line(t, store, "aa000010"), `"at":"2026-06-01T10:00:00Z"`) {
		t.Errorf("work_ref moved the transition stamp: %s", line(t, store, "aa000010"))
	}
	if result := store.SetWorkRef("aa000010", "", "", ""); result.Status != MutationOK {
		t.Fatalf("clear status = %q, errors = %v", result.Status, result.Errors)
	}
	if strings.Contains(line(t, store, "aa000010"), "work_ref") {
		t.Errorf("work_ref survived a clear: %s", line(t, store, "aa000010"))
	}
	if result := store.SetWorkRef("aa000011", "https://example.invalid/1", "", ""); result.Status != MutationInvalid ||
		result.FirstError() != "task is not delegated" {
		t.Errorf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if result := store.SetWorkRef("aa000010", "a\nb", "", ""); result.Status != MutationInvalid {
		t.Errorf("a multi-line reference was accepted: %q", result.Status)
	}
}
