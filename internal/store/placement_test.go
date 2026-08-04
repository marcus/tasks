package store

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// The placement tree from test/test_task_placement.rb: two populated sections,
// one empty one, and a three-deep subtree under A so a move can be tested
// against the destination's depth rather than only the moving record's.
const placementTree = `{"type":"meta","version":2}
{"type":"section","id":"10000001","title":"One"}
{"type":"task","id":"a0000001","parent":"10000001","state":"TODO","title":"A"}
{"type":"task","id":"a0000002","parent":"a0000001","state":"TODO","title":"A child"}
{"type":"task","id":"a0000003","parent":"a0000002","state":"TODO","title":"A grandchild"}
{"type":"task","id":"b0000001","parent":"10000001","state":"TODO","title":"B"}
{"type":"task","id":"b0000002","parent":"b0000001","state":"TODO","title":"B child"}
{"type":"task","id":"c0000001","parent":"10000001","state":"TODO","title":"C"}
{"type":"task","id":"d0000001","parent":"10000001","state":"TODO","title":"D"}
{"type":"section","id":"20000001","title":"Two"}
{"type":"task","id":"e0000001","parent":"20000001","state":"TODO","title":"E"}
{"type":"task","id":"e0000002","parent":"e0000001","state":"TODO","title":"E child"}
{"type":"task","id":"f0000001","parent":"20000001","state":"TODO","title":"F"}
{"type":"section","id":"30000001","title":"Three"}
`

// MIXED_TREE: one parent whose children interleave tasks and SECTIONS. It
// exists because a section subtree sits between task siblings in the file, so
// the insertion slot a placement resolves to is not simply "the next task".
const placementMixedTree = `{"type":"meta","version":2}
{"type":"section","id":"40000001","title":"Parent"}
{"type":"task","id":"41000001","parent":"40000001","state":"TODO","title":"A"}
{"type":"section","id":"42000001","parent":"40000001","title":"Child section"}
{"type":"task","id":"42000002","parent":"42000001","state":"TODO","title":"Section child"}
{"type":"task","id":"43000001","parent":"40000001","state":"TODO","title":"B"}
{"type":"section","id":"44000001","parent":"40000001","title":"Trailing section"}
{"type":"task","id":"44000002","parent":"44000001","state":"TODO","title":"Trailing child"}
`

// place is the placement changeset a caller would submit: read the revision,
// then ask for the move guarded by it.
func place(t *testing.T, target *Store, id, parentID, beforeID string) MutationResult {
	t.Helper()
	revision, _ := target.TaskRevision(id)
	return placeAt(t, target, id, parentID, beforeID, revision)
}

func placeAt(t *testing.T, target *Store, id, parentID, beforeID, revision string) MutationResult {
	t.Helper()
	return target.ApplyChangeset(Changeset{
		ID: id, ExpectedRevision: revision, Today: "2026-06-10",
		Changes: []Change{{Field: FieldLocation, Value: PlacementValue(parentID, beforeID)}},
	})
}

// childIDs is the TASK children of a parent, in file order.
func childIDs(t *testing.T, target *Store, parentID string) []string {
	t.Helper()
	ids := []string{}
	for _, parsed := range parseStore(t, target) {
		if parsed.String("type") == "task" && parsed.String("parent") == parentID {
			ids = append(ids, parsed.String("id"))
		}
	}
	return ids
}

// directChildIDs is every child, sections included — the order a mixed subtree
// is actually stored in.
func directChildIDs(t *testing.T, target *Store, parentID string) []string {
	t.Helper()
	ids := []string{}
	for _, parsed := range parseStore(t, target) {
		if parsed.String("parent") == parentID {
			ids = append(ids, parsed.String("id"))
		}
	}
	return ids
}

func parseStore(t *testing.T, target *Store) []record.Record {
	t.Helper()
	raw, err := os.ReadFile(target.org)
	if err != nil {
		t.Fatal(err)
	}
	return record.Parse(raw).Records
}

func assertChecked(t *testing.T, target *Store) {
	t.Helper()
	if result := check.Check(target.org); !result.OK() {
		t.Fatalf("store failed validation: %v", result.Errors)
	}
}

func assertIDs(t *testing.T, got, want []string, what string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// test_reorders_first_middle_and_last_siblings_before_an_anchor.
func TestReordersFirstMiddleAndLastSiblingsBeforeAnAnchor(t *testing.T) {
	for _, testCase := range []struct {
		moving, before string
		want           []string
	}{
		{"a0000001", "c0000001", []string{"b0000001", "a0000001", "c0000001", "d0000001"}},
		{"c0000001", "a0000001", []string{"c0000001", "a0000001", "b0000001", "d0000001"}},
		{"d0000001", "b0000001", []string{"a0000001", "d0000001", "b0000001", "c0000001"}},
	} {
		target, _ := writerFixture(t, placementTree)
		result := place(t, target, testCase.moving, "10000001", testCase.before)
		if result.Status != MutationOK {
			t.Fatalf("%s before %s: status = %q, errors = %v",
				testCase.moving, testCase.before, result.Status, result.Errors)
		}
		assertIDs(t, childIDs(t, target, "10000001"), testCase.want, "children")
		assertChecked(t, target)
	}
}

// test_same_parent_append_reorders_instead_of_taking_legacy_early_noop: a
// PLACEMENT to the parent a task already has is a reorder to the end, not the
// legacy append form's early "already there".
func TestSameParentAppendReordersInsteadOfTakingLegacyEarlyNoop(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	result := place(t, target, "b0000001", "10000001", "")

	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, childIDs(t, target, "10000001"),
		[]string{"a0000001", "c0000001", "d0000001", "b0000001"}, "children")
	assertIDs(t, result.TouchedIDs, []string{"b0000001", "b0000002"}, "touched")
	assertIDs(t, result.Summary.MovedIDs, []string{"b0000001", "b0000002"}, "moved_ids")
	if result.Summary.From != "10000001" || result.Summary.To != "10000001" ||
		result.Summary.Before != "" {
		t.Errorf("summary = %+v", result.Summary)
	}
	assertChecked(t, target)
}

// test_cross_parent_move_inserts_full_subtree_before_anchor.
func TestCrossParentMoveInsertsFullSubtreeBeforeAnchor(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	result := place(t, target, "a0000001", "20000001", "f0000001")

	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, childIDs(t, target, "20000001"),
		[]string{"e0000001", "a0000001", "f0000001"}, "children")
	assertIDs(t, result.TouchedIDs, []string{"a0000001", "a0000002", "a0000003"}, "touched")
	assertIDs(t, result.Summary.MovedIDs, result.TouchedIDs, "moved_ids")

	// The subtree's INTERNAL parentage is untouched — only its root reparents.
	byID := map[string]record.Record{}
	for _, parsed := range parseStore(t, target) {
		byID[parsed.String("id")] = parsed
	}
	for _, pair := range [][2]string{
		{"a0000001", "20000001"}, {"a0000002", "a0000001"}, {"a0000003", "a0000002"},
	} {
		if got := byID[pair[0]].String("parent"); got != pair[1] {
			t.Errorf("%s parent = %q, want %q", pair[0], got, pair[1])
		}
	}
	assertChecked(t, target)
}

// test_moves_to_empty_section_and_from_section_level_under_a_task.
func TestMovesToEmptySectionAndFromSectionLevelUnderATask(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	first := place(t, target, "c0000001", "30000001", "")
	second := place(t, target, "d0000001", "c0000001", "")

	if first.Status != MutationOK || second.Status != MutationOK {
		t.Fatalf("statuses = %q, %q", first.Status, second.Status)
	}
	assertIDs(t, childIDs(t, target, "30000001"), []string{"c0000001"}, "section children")
	assertIDs(t, childIDs(t, target, "c0000001"), []string{"d0000001"}, "task children")
	assertChecked(t, target)
}

// test_moves_a_nested_task_to_a_section.
func TestMovesANestedTaskToASection(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	result := place(t, target, "a0000002", "30000001", "")

	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, result.TouchedIDs, []string{"a0000002", "a0000003"}, "touched")
	assertIDs(t, childIDs(t, target, "30000001"), []string{"a0000002"}, "section children")
	assertIDs(t, childIDs(t, target, "a0000002"), []string{"a0000003"}, "kept children")
	assertChecked(t, target)
}

// test_missing_parent_and_anchor_are_field_specific_and_resolved_before_cycles:
// every id resolves BEFORE the descendant-parent cycle check, so a caller with
// two bad arguments learns which one is wrong rather than being told about a
// cycle it did not create.
func TestMissingParentAndAnchorAreFieldSpecificAndResolvedBeforeCycles(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	before := readStore(t, target)

	missingParent := place(t, target, "a0000001", "deadbeef", "a0000002")
	if missingParent.Status != MutationNotFound {
		t.Fatalf("missing parent status = %q", missingParent.Status)
	}
	assertIDs(t, missingParent.FieldErrors["parent_id"],
		[]string{"parent_id does not identify a live task or section"}, "parent_id errors")

	missingAnchor := place(t, target, "a0000001", "a0000002", "deadbeef")
	if missingAnchor.Status != MutationNotFound {
		t.Fatalf("missing anchor status = %q, want not_found — all ids resolve "+
			"before the descendant-parent cycle check", missingAnchor.Status)
	}
	assertIDs(t, missingAnchor.FieldErrors["before_id"],
		[]string{"before_id does not identify a live task"}, "before_id errors")

	if readStore(t, target) != before {
		t.Error("a refused placement wrote bytes")
	}
}

// test_self_and_descendant_parents_or_anchors_are_cycles_before_parentage_conflicts.
func TestSelfAndDescendantParentsOrAnchorsAreCyclesBeforeParentageConflicts(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	before := readStore(t, target)

	for _, testCase := range [][2]string{
		{"a0000001", ""},         // itself as parent
		{"a0000002", ""},         // its own child as parent
		{"10000001", "a0000001"}, // itself as anchor
		{"20000001", "a0000002"}, // its own child as anchor, under another parent
	} {
		result := place(t, target, "a0000001", testCase[0], testCase[1])
		if result.Status != MutationCycle {
			t.Errorf("parent=%q before=%q: status = %q, want cycle",
				testCase[0], testCase[1], result.Status)
		}
		if readStore(t, target) != before {
			t.Fatal("a refused placement wrote bytes")
		}
	}
}

// test_unrelated_wrong_parent_anchor_is_validated_before_same_parent_noop: the
// anchor's REAL parent comes back in the summary, because the caller decided
// against a sibling list that has since changed.
func TestUnrelatedWrongParentAnchorIsValidatedBeforeSameParentNoop(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	before := readStore(t, target)

	result := place(t, target, "a0000001", "10000001", "e0000001")
	if result.Status != MutationConflict {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	if result.Summary.CurrentParentID != "20000001" {
		t.Errorf("current_parent_id = %q, want 20000001", result.Summary.CurrentParentID)
	}
	if readStore(t, target) != before {
		t.Error("a refused placement wrote bytes")
	}
}

// test_full_subtree_height_is_checked_against_destination_depth: the cap is the
// destination's depth plus the moving subtree's HEIGHT, not plus one. A is three
// deep, so landing it under a depth-2 task would put its grandchild at 5.
func TestFullSubtreeHeightIsCheckedAgainstDestinationDepth(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	before := readStore(t, target)

	result := place(t, target, "a0000001", "e0000002", "")
	if result.Status != MutationTooDeep {
		t.Fatalf("status = %q, want too_deep", result.Status)
	}
	if readStore(t, target) != before {
		t.Error("a refused placement wrote bytes")
	}
}

// test_exact_anchor_and_append_slots_are_noops_without_writes_or_history: a
// placement that names the slot the task already occupies writes nothing and
// burns no undo slot.
func TestExactAnchorAndAppendSlotsAreNoopsWithoutWritesOrHistory(t *testing.T) {
	for _, testCase := range [][2]string{{"a0000001", "b0000001"}, {"d0000001", ""}} {
		target, _ := writerFixture(t, placementTree)
		before := readStore(t, target)

		result := place(t, target, testCase[0], "10000001", testCase[1])
		if result.Status != MutationNoChange {
			t.Fatalf("%v: status = %q, want no_change", testCase, result.Status)
		}
		if len(result.Summary.MovedIDs) != 0 {
			t.Errorf("%v: moved_ids = %v, want empty", testCase, result.Summary.MovedIDs)
		}
		if readStore(t, target) != before {
			t.Errorf("%v: a no-op wrote bytes", testCase)
		}
		if status, _ := target.HistoryStep(-1); status != HistoryEmpty {
			t.Errorf("%v: a no-op recorded history (%v)", testCase, status)
		}
	}
}

// test_before_slot_moves_across_child_section_subtree_then_becomes_noop: the
// insertion slot is a FILE position, and a child section's whole subtree sits
// between two task siblings. Moving across it is a real move; repeating it is
// a no-op.
func TestBeforeSlotMovesAcrossChildSectionSubtreeThenBecomesNoop(t *testing.T) {
	target, _ := writerFixture(t, placementMixedTree)
	original := readStore(t, target)

	result := place(t, target, "41000001", "40000001", "43000001")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	moved := readStore(t, target)
	assertIDs(t, directChildIDs(t, target, "40000001"),
		[]string{"42000001", "41000001", "43000001", "44000001"}, "direct children")
	assertChecked(t, target)

	repeat := place(t, target, "41000001", "40000001", "43000001")
	if repeat.Status != MutationNoChange {
		t.Fatalf("repeat status = %q, want no_change", repeat.Status)
	}
	if readStore(t, target) != moved {
		t.Error("a repeated placement wrote bytes")
	}
	if status, _ := target.HistoryStep(-1); status != HistoryOK {
		t.Fatalf("undo status = %v", status)
	}
	if readStore(t, target) != original {
		t.Error("undo did not restore the original bytes")
	}
}

// test_append_slot_moves_after_trailing_child_section_then_becomes_noop.
func TestAppendSlotMovesAfterTrailingChildSectionThenBecomesNoop(t *testing.T) {
	target, _ := writerFixture(t, placementMixedTree)
	original := readStore(t, target)

	result := place(t, target, "43000001", "40000001", "")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	moved := readStore(t, target)
	assertIDs(t, directChildIDs(t, target, "40000001"),
		[]string{"41000001", "42000001", "44000001", "43000001"}, "direct children")
	assertChecked(t, target)

	repeat := place(t, target, "43000001", "40000001", "")
	if repeat.Status != MutationNoChange {
		t.Fatalf("repeat status = %q, want no_change", repeat.Status)
	}
	if readStore(t, target) != moved {
		t.Error("a repeated placement wrote bytes")
	}
	if status, _ := target.HistoryStep(-1); status != HistoryOK {
		t.Fatalf("undo status = %v", status)
	}
	if readStore(t, target) != original {
		t.Error("undo did not restore the original bytes")
	}
}

// test_undo_and_redo_restore_byte_identical_placement_states.
func TestUndoAndRedoRestoreByteIdenticalPlacementStates(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	original := readStore(t, target)

	if result := place(t, target, "a0000001", "20000001", "f0000001"); result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	moved := readStore(t, target)
	assertChecked(t, target)

	if status, _ := target.HistoryStep(-1); status != HistoryOK {
		t.Fatalf("undo status = %v", status)
	}
	if readStore(t, target) != original {
		t.Error("undo did not restore the original bytes")
	}
	if status, _ := target.HistoryStep(1); status != HistoryOK {
		t.Fatalf("redo status = %v", status)
	}
	if readStore(t, target) != moved {
		t.Error("redo did not restore the moved bytes")
	}
	assertChecked(t, target)
}

// test_revision_own_component_excludes_location_but_order_changes_location_components:
// reordering siblings must not invalidate an in-flight TITLE edit on any of
// them, and must invalidate a legacy move on all of them.
func TestRevisionOwnComponentExcludesLocationButOrderChangesLocationComponents(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	ids := []string{"a0000001", "b0000001", "c0000001", "d0000001"}
	before := map[string]string{}
	for _, id := range ids {
		before[id], _ = target.TaskRevision(id)
	}

	if result := place(t, target, "c0000001", "10000001", "a0000001"); result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	for _, id := range ids {
		after, _ := target.TaskRevision(id)
		beforeParts := strings.Split(before[id], ".")
		afterParts := strings.Split(after, ".")
		if beforeParts[1] != afterParts[1] {
			t.Errorf("own digest changed for %s", id)
		}
		if beforeParts[2] == afterParts[2] {
			t.Errorf("location digest did not change for %s", id)
		}
	}
}

// test_placement_stales_on_own_edit_but_legacy_move_still_stales_on_sibling_order:
// the two spellings guard different things on purpose. A placement carries its
// own precondition (the anchor's parent), so it only stales on an OWN-field
// edit; a legacy move has no anchor and so must stale on sibling order.
func TestPlacementStalesOnOwnEditButLegacyMoveStalesOnSiblingOrder(t *testing.T) {
	target, _ := writerFixture(t, placementTree)

	stale, _ := target.TaskRevision("c0000001")
	edited := target.ApplyChangeset(Changeset{
		ID: "c0000001", ExpectedRevision: stale, Today: "2026-06-10",
		Changes: []Change{{Field: FieldTitle, Value: TextValue("C edited")}},
	})
	if edited.Status != MutationOK {
		t.Fatalf("edit status = %q, errors = %v", edited.Status, edited.Errors)
	}
	if result := placeAt(t, target, "c0000001", "20000001", "f0000001", stale); result.Status != MutationStale {
		t.Errorf("placement over an own edit = %q, want stale", result.Status)
	}

	legacy, _ := target.TaskRevision("b0000001")
	if result := place(t, target, "d0000001", "10000001", "b0000001"); result.Status != MutationOK {
		t.Fatalf("reorder status = %q, errors = %v", result.Status, result.Errors)
	}
	result := target.ApplyChangeset(Changeset{
		ID: "b0000001", ExpectedRevision: legacy, Today: "2026-06-10",
		Changes: []Change{{Field: FieldLocation, Value: TextValue("20000001")}},
	})
	if result.Status != MutationStale {
		t.Errorf("legacy move over a sibling reorder = %q, want stale", result.Status)
	}
}

// test_missing_placement_targets_precede_stale_own_revision_but_legacy_order_is_unchanged:
// a destination that does not exist is reported BEFORE the precondition, so a
// caller fixes the argument it got wrong rather than re-reading a revision and
// submitting the same bad id again.
func TestMissingPlacementTargetsPrecedeStaleOwnRevision(t *testing.T) {
	target, _ := writerFixture(t, placementTree)

	stale, _ := target.TaskRevision("c0000001")
	edited := target.ApplyChangeset(Changeset{
		ID: "c0000001", ExpectedRevision: stale, Today: "2026-06-10",
		Changes: []Change{{Field: FieldTitle, Value: TextValue("C edited")}},
	})
	if edited.Status != MutationOK {
		t.Fatalf("edit status = %q", edited.Status)
	}

	missingParent := placeAt(t, target, "c0000001", "deadbeef", "", stale)
	if missingParent.Status != MutationNotFound ||
		len(missingParent.FieldErrors["parent_id"]) != 1 {
		t.Errorf("missing parent = %q %v", missingParent.Status, missingParent.FieldErrors)
	}
	missingAnchor := placeAt(t, target, "c0000001", "20000001", "deadbeef", stale)
	if missingAnchor.Status != MutationNotFound ||
		len(missingAnchor.FieldErrors["before_id"]) != 1 {
		t.Errorf("missing anchor = %q %v", missingAnchor.Status, missingAnchor.FieldErrors)
	}
	if result := placeAt(t, target, "c0000001", "20000001", "f0000001", stale); result.Status != MutationStale {
		t.Errorf("valid destination over a stale revision = %q, want stale", result.Status)
	}

	// The legacy spelling keeps its own order: the revision is checked first,
	// so a missing parent behind a stale revision reports stale.
	legacy := target.ApplyChangeset(Changeset{
		ID: "c0000001", ExpectedRevision: stale, Today: "2026-06-10",
		Changes: []Change{{Field: FieldLocation, Value: TextValue("deadbeef")}},
	})
	if legacy.Status != MutationStale {
		t.Errorf("legacy missing parent behind a stale revision = %q, want stale", legacy.Status)
	}
	assertChecked(t, target)
}

// test_ordinary_field_edit_ignores_a_concurrent_location_change: a title edit
// held across someone else's move still applies. That is the entire reason the
// revision has three components rather than one.
func TestOrdinaryFieldEditIgnoresAConcurrentLocationChange(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	original, _ := target.TaskRevision("a0000001")

	if moved := placeAt(t, target, "a0000001", "20000001", "f0000001", original); moved.Status != MutationOK {
		t.Fatalf("move status = %q, errors = %v", moved.Status, moved.Errors)
	}
	result := target.ApplyChangeset(Changeset{
		ID: "a0000001", ExpectedRevision: original, Today: "2026-06-10",
		Changes: []Change{{Field: FieldTitle, Value: TextValue("A renamed")}},
	})
	if result.Status != MutationOK {
		t.Fatalf("edit status = %q, errors = %v", result.Status, result.Errors)
	}
	for _, parsed := range parseStore(t, target) {
		if parsed.String("id") != "a0000001" {
			continue
		}
		if parsed.String("title") != "A renamed" || parsed.String("parent") != "20000001" {
			t.Errorf("record = %s", parsed.String("title")+" under "+parsed.String("parent"))
		}
	}
}

// A multi-field changeset that MOVES the record must still apply the later
// fields to the same task. The index is re-resolved per field for exactly this:
// `location` sorts before `state` in FIELD_ORDER and physically relocates the
// row, so a cached position would patch whatever slid into the old slot.
func TestAMultiFieldPlacementAppliesEveryFieldToTheMovedRecord(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	revision, _ := target.TaskRevision("a0000001")

	result := target.ApplyChangeset(Changeset{
		ID: "a0000001", ExpectedRevision: revision, Today: "2026-06-10",
		Changes: []Change{
			{Field: FieldTitle, Value: TextValue("A moved")},
			{Field: FieldLocation, Value: PlacementValue("20000001", "f0000001")},
		},
	})
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, result.TouchedIDs, []string{"a0000001", "a0000002", "a0000003"}, "touched")
	for _, parsed := range parseStore(t, target) {
		if parsed.String("id") != "a0000001" {
			continue
		}
		if parsed.String("title") != "A moved" {
			t.Errorf("title = %q, want %q", parsed.String("title"), "A moved")
		}
		if parsed.String("parent") != "20000001" {
			t.Errorf("parent = %q, want 20000001", parsed.String("parent"))
		}
	}
	assertChecked(t, target)
}

// -- the legacy append form ------------------------------------------------------

// A bare parent id APPENDS, and without force a task already under that parent
// is satisfied without a write. `move --top` depends on that: unnesting a task
// that is already at section level must not burn an undo slot.
func TestALegacyMoveToTheCurrentParentIsSatisfiedWithoutForce(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	before := readStore(t, target)

	result := patch(t, target, "b0000001", FieldLocation, TextValue("10000001"))
	if result.Status != MutationNoChange {
		t.Fatalf("status = %q, want no_change", result.Status)
	}
	if readStore(t, target) != before {
		t.Error("a satisfied move wrote bytes")
	}
}

// With force the same move REORDERS: the subtree is re-appended after its
// siblings, which is what `move <ref> "Section"` means for a task already in
// that section.
func TestAForcedLegacyMoveToTheCurrentParentReappends(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	expected, _ := target.ExpectedFor("b0000001", FieldLocation)
	result := target.Patch(PatchRequest{
		ID: "b0000001", Field: FieldLocation, Value: TextValue("10000001"),
		Expected: expected, Today: "2026-06-10", Force: true,
	})
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, childIDs(t, target, "10000001"),
		[]string{"a0000001", "c0000001", "d0000001", "b0000001"}, "children")
	assertChecked(t, target)
}

// UNNEST resolves the ENCLOSING SECTION from the file rather than taking a
// caller-named parent, which is what makes `move --top` mean "up to my heading"
// and not "up to the file root".
func TestUnnestResolvesTheEnclosingSection(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	result := patch(t, target, "a0000003", FieldLocation, UnnestValue())

	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	assertIDs(t, childIDs(t, target, "10000001"),
		[]string{"a0000001", "b0000001", "c0000001", "d0000001", "a0000003"}, "children")
	assertChecked(t, target)
}

// The legacy form's refusals, each named. They are separate from the placement
// refusals because the append form has no anchor to blame.
func TestLegacyMoveRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name, moving, parent string
		status               MutationStatus
		message              string
	}{
		{"a parent that does not exist", "a0000001", "deadbeef",
			MutationInvalid, "location parent does not exist"},
		{"itself", "a0000001", "a0000001", MutationCycle, ""},
		{"its own descendant", "a0000001", "a0000003", MutationCycle, ""},
		{"past the nesting cap", "a0000001", "e0000002", MutationTooDeep, ""},
	} {
		target, _ := writerFixture(t, placementTree)
		before := readStore(t, target)
		result := patch(t, target, testCase.moving, FieldLocation, TextValue(testCase.parent))
		if result.Status != testCase.status {
			t.Errorf("%s: status = %q, want %q", testCase.name, result.Status, testCase.status)
		}
		if testCase.message != "" && result.FirstError() != testCase.message {
			t.Errorf("%s: error = %q, want %q", testCase.name, result.FirstError(), testCase.message)
		}
		if readStore(t, target) != before {
			t.Errorf("%s: a refused move wrote bytes", testCase.name)
		}
	}
}

// Accepted work cannot be moved under an undecided proposal, in either
// spelling. A proposal moving under a proposal is fine — the decision cascades
// leaves-first — so only the accepted case refuses.
func TestAcceptedWorkCannotBeMovedUnderAProposedTask(t *testing.T) {
	const tree = `{"type":"meta","version":2}
{"type":"section","id":"50000001","title":"Inbox"}
{"type":"task","id":"51000001","parent":"50000001","state":"PROPOSED","title":"Proposal"}
{"type":"task","id":"52000001","parent":"50000001","state":"TODO","title":"Accepted"}
{"type":"task","id":"53000001","parent":"50000001","state":"PROPOSED","title":"Other proposal"}
`
	target, _ := writerFixture(t, tree)
	refused := patch(t, target, "52000001", FieldLocation, TextValue("51000001"))
	mustInvalid(t, refused, "accepted work cannot be moved under a proposed task")

	allowed := patch(t, target, "53000001", FieldLocation, TextValue("51000001"))
	if allowed.Status != MutationOK {
		t.Fatalf("proposal under proposal = %q, errors = %v", allowed.Status, allowed.Errors)
	}
	assertChecked(t, target)
}

// The value shape is checked before the file is read, so a caller that passed a
// title or a line number where a stable id belongs learns it without a
// transaction.
func TestPlacementValueShapeIsRefusedBeforeTheTransaction(t *testing.T) {
	target, _ := writerFixture(t, placementTree)
	for _, testCase := range []struct {
		name  string
		value PatchValue
		want  string
	}{
		{"a parent that is not an id", PlacementValue("Next Actions", ""),
			"parent_id must be a stable id"},
		{"an anchor that is not an id", PlacementValue("10000001", "L7"),
			"before_id must be a stable id or nil"},
		{"a legacy value that is not an id", TextValue("Next Actions"),
			"location must be a stable parent id, UNNEST, or Tasks::TaskPlacement"},
	} {
		revision, _ := target.TaskRevision("a0000001")
		result := target.ApplyChangeset(Changeset{
			ID: "a0000001", ExpectedRevision: revision, Today: "2026-06-10",
			Changes: []Change{{Field: FieldLocation, Value: testCase.value}},
		})
		if result.Status != MutationInvalid || result.FirstError() != testCase.want {
			t.Errorf("%s: %q / %q", testCase.name, result.Status, result.FirstError())
		}
	}
}
