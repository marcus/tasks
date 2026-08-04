package store

import (
	"regexp"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// Placement is Tasks::TaskPlacement: an immutable, transport-neutral structural
// destination for one task subtree.
//
// It is a VALUE, not a pair of record indexes, and that is the whole point. The
// store resolves the two stable ids under its own mutation lock; an adapter that
// translated them into positions first would be resolving them against a read it
// no longer holds, so a concurrent write between the read and the mutation would
// silently move a different task.
//
// An empty BeforeID means "append as the destination parent's last child".
type Placement struct {
	ParentID string
	BeforeID string
}

// Appends reports the no-anchor spelling: land at the end of the parent's
// children rather than in front of a named sibling.
func (p Placement) Appends() bool { return p.BeforeID == "" }

// stableTaskID is `stable_task_id?`: Check::ID_RE, applied to a value that must
// also be ordinary ASCII text. It is spelled here rather than borrowed from
// check because this is a question about a CALLER'S argument, not about a stored
// record, and the two would only ever be confused by sharing a name.
func stableTaskID(value string) bool { return stableIDPattern.MatchString(value) }

var stableIDPattern = regexp.MustCompile(`\A[0-9a-f]{8}\z`)

// PlacementValue is a location change expressed as a structural destination.
func PlacementValue(parentID, beforeID string) PatchValue {
	return PatchValue{kind: kindPlacement, placement: Placement{ParentID: parentID, BeforeID: beforeID}}
}

// UnnestValue is TaskChangeset::UNNEST: "move this task up to the section that
// encloses it", resolved against the file rather than named by the caller.
func UnnestValue() PatchValue { return PatchValue{kind: kindUnnest} }

// Placement is the structural destination of a placement value.
func (v PatchValue) Placement() (Placement, bool) {
	return v.placement, v.kind == kindPlacement
}

// placementTargets is `resolve_placement_targets`: the two record indexes a
// placement names, resolved ONCE against the pre-apply records.
//
// A changeset resolves them before the transaction commits to a field order so
// that a destination that does not exist refuses with `not_found` and the field
// errors naming WHICH id was wrong, rather than reaching the apply and failing
// as a generic invalid.
type placementTargets struct {
	status      MutationStatus
	errors      []string
	fieldErrors map[string][]string
	parentIndex int
	beforeIndex int
}

// resolvePlacementTargets locates the destination parent and the optional
// anchor. A parent may be a section or a task; an anchor may only be a task,
// because a section is never a sibling a task is placed in front of.
func resolvePlacementTargets(records []record.Record, placement Placement) placementTargets {
	parentIndex := -1
	for index, parsed := range records {
		if parsed.String("id") != placement.ParentID {
			continue
		}
		if kind := parsed.String("type"); kind == "section" || kind == "task" {
			parentIndex = index
		}
		break
	}
	beforeIndex := -1
	if placement.BeforeID != "" {
		for index, parsed := range records {
			if parsed.String("id") != placement.BeforeID {
				continue
			}
			if parsed.String("type") == "task" {
				beforeIndex = index
			}
			break
		}
	}
	if parentIndex < 0 {
		message := "parent_id does not identify a live task or section"
		return placementTargets{
			status: MutationNotFound, errors: []string{message},
			fieldErrors: map[string][]string{"parent_id": {message}},
		}
	}
	if placement.BeforeID != "" && beforeIndex < 0 {
		message := "before_id does not identify a live task"
		return placementTargets{
			status: MutationNotFound, errors: []string{message},
			fieldErrors: map[string][]string{"before_id": {message}},
		}
	}
	return placementTargets{status: MutationOK, parentIndex: parentIndex, beforeIndex: beforeIndex}
}

// patchLocation is `patch_location`: the whole of the move.
//
// Three spellings reach it and they are deliberately different operations. A
// Placement lands the subtree at an exact position among the destination's
// children; a bare parent id APPENDS to the end of that parent's subtree; and
// UNNEST resolves the enclosing section first and then appends there.
func patchLocation(records []record.Record, index int, value PatchValue, context patchContext,
	targets *placementTargets) patchOutcome {

	if placement, ok := value.Placement(); ok {
		return patchPlacement(records, index, placement, context, targets)
	}

	target := records[index]
	parentID := ""
	switch value.kind {
	case kindUnnest:
		parentID = enclosingSectionID(records, target)
		if parentID == "" {
			// Ruby reaches `patch_invalid("location must be a parent id")` here:
			// enclosing_section_id returned nil, and nil is not a String.
			return patchInvalid("location must be a parent id")
		}
	case kindText:
		parentID = value.text
	default:
		return patchInvalid("location must be a parent id")
	}

	from := target.String("parent")
	if !context.force && from == parentID {
		return patchOutcome{
			status: MutationOK, touchedIDs: []string{target.String("id")},
			summary: MutationSummary{From: from, To: parentID, MovedIDs: []string{}},
		}
	}

	parentIndex := locateStableIndex(records, parentID)
	if parentIndex < 0 {
		return patchInvalid("location parent does not exist")
	}
	parentType := records[parentIndex].String("type")
	if parentType != "section" && parentType != "task" {
		return patchInvalid("location parent must be a section or task")
	}
	if refusal := proposedParentRefusal(records[parentIndex], target); refusal != nil {
		return *refusal
	}

	end := subtreeEnd(records, index)
	if parentIndex >= index && parentIndex < end {
		return patchOutcome{
			status:  MutationCycle,
			summary: MutationSummary{From: from, To: parentID},
		}
	}
	if depthExceeded(records, index, parentIndex, context.maxDepth) {
		return patchOutcome{
			status:  MutationTooDeep,
			summary: MutationSummary{From: from, To: parentID},
		}
	}

	moved, rest, newParentIndex := detachSubtree(records, index, end, parentID)
	movedIDs := []string{}
	for _, parsed := range moved {
		if id := parsed.String("id"); id != "" {
			movedIDs = append(movedIDs, id)
		}
	}
	moved[0].SetString("parent", parentID)
	spliced := spliceAt(rest, subtreeEnd(rest, newParentIndex), moved)
	copy(records, spliced)

	return patchOutcome{
		status: MutationOK, touchedIDs: movedIDs,
		summary: MutationSummary{From: from, To: parentID, MovedIDs: movedIDs},
	}
}

// patchPlacement is `patch_placement`: the anchored move.
//
// Two refusals here have no counterpart in the append form, and both exist
// because an anchor is a claim about the destination's CURRENT shape. An anchor
// inside the moving subtree is a cycle even when the parent is not, and an
// anchor whose parent is not the destination is a conflict — the caller decided
// against a sibling list that has since changed underneath it.
func patchPlacement(records []record.Record, index int, placement Placement, context patchContext,
	targets *placementTargets) patchOutcome {

	target := records[index]
	from := target.String("parent")
	summary := MutationSummary{From: from, To: placement.ParentID, Before: placement.BeforeID}

	resolved := placementTargets{}
	if targets != nil {
		resolved = *targets
	} else {
		resolved = resolvePlacementTargets(records, placement)
	}
	if resolved.status != MutationOK {
		return patchOutcome{
			status: resolved.status, errors: resolved.errors,
			fieldErrors: resolved.fieldErrors, summary: summary,
		}
	}

	parentIndex, beforeIndex := resolved.parentIndex, resolved.beforeIndex
	if refusal := proposedParentRefusal(records[parentIndex], target); refusal != nil {
		return *refusal
	}

	end := subtreeEnd(records, index)
	if (parentIndex >= index && parentIndex < end) ||
		(beforeIndex >= 0 && beforeIndex >= index && beforeIndex < end) {
		return patchOutcome{status: MutationCycle, summary: summary}
	}
	if beforeIndex >= 0 && records[beforeIndex].String("parent") != placement.ParentID {
		conflict := summary
		conflict.CurrentParentID = records[beforeIndex].String("parent")
		return patchOutcome{status: MutationConflict, summary: conflict}
	}
	if depthExceeded(records, index, parentIndex, context.maxDepth) {
		return patchOutcome{status: MutationTooDeep, summary: summary}
	}

	movedIDs := []string{}
	for position := index; position < end; position++ {
		if records[position].String("type") == "task" {
			if id := records[position].String("id"); id != "" {
				movedIDs = append(movedIDs, id)
			}
		}
	}

	moved, rest, newParentIndex := detachSubtree(records, index, end, placement.ParentID)
	insertAt := subtreeEnd(rest, newParentIndex)
	if placement.BeforeID != "" {
		insertAt = locateStableIndex(rest, placement.BeforeID)
	}
	// After removing the moving span, its old physical boundary is still `index`
	// in the detached array. The placement is already satisfied only when the
	// freshly resolved insertion boundary is that exact slot.
	if from == placement.ParentID && index == insertAt {
		settled := summary
		settled.MovedIDs = []string{}
		return patchOutcome{status: MutationOK, touchedIDs: []string{}, summary: settled}
	}

	moved[0].SetString("parent", placement.ParentID)
	spliced := spliceAt(rest, insertAt, moved)
	copy(records, spliced)

	applied := summary
	applied.MovedIDs = movedIDs
	return patchOutcome{status: MutationOK, touchedIDs: movedIDs, summary: applied}
}

// proposedParentRefusal keeps accepted work out from under an undecided
// proposal. Moving a proposal under a proposal is fine — the decision cascades
// leaves-first — so only the accepted case refuses.
func proposedParentRefusal(parent, target record.Record) *patchOutcome {
	if parent.String("type") != "task" {
		return nil
	}
	if !contains(check.ProposedStates, parent.String("state")) {
		return nil
	}
	if contains(check.ProposedStates, target.String("state")) {
		return nil
	}
	refusal := patchInvalid("accepted work cannot be moved under a proposed task")
	return &refusal
}

// depthExceeded is the nesting cap, measured the way a move actually lands: the
// destination task's own depth plus the HEIGHT of the moving subtree, because
// the deepest leaf is what ends up furthest down. A section destination cannot
// exceed the cap — sections do not count toward depth at all.
func depthExceeded(records []record.Record, index, parentIndex, maxDepth int) bool {
	if records[parentIndex].String("type") != "task" {
		return false
	}
	byID := recordsByID(records)
	return taskDepth(byID, records[parentIndex])+subtreeHeight(records, index) > maxDepth
}

// subtreeHeight is the deepest TASK nesting inside one subtree, counted from
// the subtree's own root as 1.
func subtreeHeight(records []record.Record, index int) int {
	end := subtreeEnd(records, index)
	span := records[index:end]
	byID := recordsByID(span)
	height := 0
	for _, parsed := range span {
		if depth := taskDepth(byID, parsed); depth > height {
			height = depth
		}
	}
	return height
}

// enclosingSectionID walks the parent chain to the nearest SECTION, which is
// what "unnest to top level" means: a task's home heading, not the file's root.
func enclosingSectionID(records []record.Record, target record.Record) string {
	byID := recordsByID(records)
	current := target
	for {
		parent, ok := byID[current.String("parent")]
		if !ok {
			return ""
		}
		if parent.String("type") == "section" {
			return parent.String("id")
		}
		current = parent
	}
}

// detachSubtree lifts records[index:end] out and reports where the destination
// parent sits in what remains. The moving span is CLONED, so a refusal after
// this point cannot have mutated the caller's records.
func detachSubtree(records []record.Record, index, end int, parentID string) ([]record.Record, []record.Record, int) {
	moved := record.CloneAll(records[index:end])
	rest := make([]record.Record, 0, len(records)-len(moved))
	rest = append(rest, records[:index]...)
	rest = append(rest, records[end:]...)
	return moved, rest, locateStableIndex(rest, parentID)
}

func spliceAt(rest []record.Record, at int, moved []record.Record) []record.Record {
	out := make([]record.Record, 0, len(rest)+len(moved))
	out = append(out, rest[:at]...)
	out = append(out, moved...)
	out = append(out, rest[at:]...)
	return out
}
