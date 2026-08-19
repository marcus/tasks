package store

import (
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/temporal"
)

// Change is one field and the value it takes.
type Change struct {
	Field PatchField
	Value PatchValue
}

// Changeset is an immutable, transport-neutral request to change one task in
// one checked transaction.
//
// `ExpectedRevision` is an opaque store-produced value: a caller carries it
// forward from TaskRevision rather than constructing it from a file coordinate
// or a wall-clock timestamp, because neither of those is a statement about what
// the task SAID when the caller decided.
//
// Changes is a SLICE rather than a map for two reasons. Ruby applies a
// changeset in `TaskChangeset::FIELD_ORDER` rather than in the order a caller
// happened to write it — several fields interact, and the order is part of the
// command contract — and a slice is also the only shape in which a caller can
// name the same field twice, which is a refusal rather than a last-one-wins.
type Changeset struct {
	// ID is the target task's stable id.
	ID string
	// Changes is the set of field changes, in any order.
	Changes []Change
	// ExpectedRevision is the token the caller read before deciding. Empty
	// means "no precondition", which only an adapter with its own guard should
	// send.
	ExpectedRevision string
	// HistoryLabel is the undo entry's text; empty takes Ruby's spelling.
	HistoryLabel string
	// CoalesceKey groups byte-contiguous edits into one undo step.
	CoalesceKey string
	// Today is the ISO day a close stamps and a roll measures from.
	Today string
	// Context is the clock a temporal field resolves against.
	Context temporal.Context
}

// fieldOrder is TaskChangeset::FIELD_ORDER. Several fields interact — dates and
// recurrence, moves and lifecycle state — so the store applies a changeset in
// THIS sequence rather than in the caller's, and a field outside the list sorts
// last by name so the order is still total.
var fieldOrder = []PatchField{
	FieldTitle, FieldPriority, FieldLinks, FieldBody,
	FieldContexts, FieldTags, FieldDeferred, FieldTagDelta,
	FieldActivate,
	FieldScheduled, FieldDeadline, FieldDateClear,
	FieldRecurrence,
	FieldLead,
	FieldLocation,
	FieldState,
}

// normalizeChangesetField is TaskChangeset#normalize_field: `recur` is a
// spelling of `recurrence`, and the two must collide rather than both apply.
func normalizeChangesetField(field PatchField) PatchField {
	if field == "recur" {
		return FieldRecurrence
	}
	return field
}

// orderedChanges is `ordered_fields`: FIELD_ORDER first, then anything unknown
// by name, so a refusal names fields in a stable order too.
func orderedChanges(changes []Change) []Change {
	position := map[PatchField]int{}
	for index, field := range fieldOrder {
		position[field] = index
	}
	ordered := make([]Change, len(changes))
	copy(ordered, changes)
	sort.SliceStable(ordered, func(left, right int) bool {
		leftIndex, leftKnown := position[ordered[left].Field]
		rightIndex, rightKnown := position[ordered[right].Field]
		if !leftKnown {
			leftIndex = len(fieldOrder)
		}
		if !rightKnown {
			rightIndex = len(fieldOrder)
		}
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		return ordered[left].Field < ordered[right].Field
	})
	return ordered
}

// validateChangeset is Store#validate_changeset: everything refusable before
// the file is read. Each rule exists because the two fields it names own
// OVERLAPPING state, and applying both would make the result depend on the
// order rather than on what the caller asked for.
func validateChangeset(changeset Changeset, ordered []Change) []string {
	errors := []string{}
	if changeset.ID == "" {
		errors = append(errors, "task id is required")
	}
	if len(ordered) == 0 {
		errors = append(errors, "changes must be a non-empty mapping")
		return errors
	}

	seen := map[PatchField]bool{}
	duplicates := []string{}
	for _, change := range ordered {
		if seen[change.Field] {
			duplicates = append(duplicates, rubyInspectText(string(change.Field)))
			continue
		}
		seen[change.Field] = true
	}
	if len(duplicates) > 0 {
		errors = append(errors, "changes repeat "+strings.Join(duplicates, ", "))
	}
	unknown := []string{}
	for _, change := range ordered {
		if !patchableFields[change.Field] {
			unknown = append(unknown, "unknown editable field "+rubyInspectText(string(change.Field)))
		}
	}
	errors = append(errors, unknown...)

	if seen[FieldTagDelta] && (seen[FieldContexts] || seen[FieldTags] || seen[FieldDeferred]) {
		errors = append(errors, "tag_delta cannot be combined with tag slice changes")
	}
	if seen[FieldDateClear] && (seen[FieldScheduled] || seen[FieldDeadline]) {
		errors = append(errors, "date_clear cannot be combined with scheduled or deadline")
	}
	if seen[FieldActivate] && (seen[FieldDeferred] || seen[FieldScheduled]) {
		errors = append(errors, "activate cannot be combined with deferred or scheduled")
	}
	for _, change := range ordered {
		if change.Field == FieldLocation {
			errors = append(errors, locationValueErrors(change.Value)...)
		}
	}
	return errors
}

// locationValueErrors is `validate_changeset_location`: the shape check that
// runs before the file is even read, so a caller that passed a line number or a
// title where a stable id belongs learns it without a transaction.
func locationValueErrors(value PatchValue) []string {
	if value.kind == kindUnnest {
		return nil
	}
	if placement, ok := value.Placement(); ok {
		errors := []string{}
		if !stableTaskID(placement.ParentID) {
			errors = append(errors, "parent_id must be a stable id")
		}
		if placement.BeforeID != "" && !stableTaskID(placement.BeforeID) {
			errors = append(errors, "before_id must be a stable id or nil")
		}
		return errors
	}
	if value.kind == kindText && stableTaskID(value.text) {
		return nil
	}
	return []string{"location must be a stable parent id, UNNEST, or Tasks::TaskPlacement"}
}

// TaskRevision is the opaque token a caller carries into a changeset. It is
// taken under the store lock, so it describes a task at one coherent moment
// rather than at whichever moment each read happened to land on.
func (s *Store) TaskRevision(id string) (string, bool) {
	var revision string
	found := false
	_ = s.withSharedLock(func() error {
		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			return nil
		}
		value, err := taskRevision(records, index, siblingIDsByParent(records))
		if err != nil {
			return nil
		}
		revision, found = value, true
		return nil
	})
	return revision, found
}

// revisionParts splits a revision into its three semantic components, or
// reports that the token is not one this store produced.
func revisionParts(revision string) ([3]string, bool) {
	parts := strings.Split(revision, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return [3]string{}, false
	}
	for _, part := range parts[1:] {
		if len(part) != 64 {
			return [3]string{}, false
		}
		for index := 0; index < len(part); index++ {
			char := part[index]
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return [3]string{}, false
			}
		}
	}
	return [3]string{parts[1], parts[2], parts[3]}, true
}

// changesetRevisionError compares only the components the changeset's fields
// can actually invalidate.
//
// That narrowing is the whole point of a three-part revision: a title edit must
// not fail because an unrelated sibling was renamed, while a move or a
// cascading state change must fail for exactly those reasons.
func changesetRevisionError(current, expected string, ordered []Change) MutationStatus {
	want, ok := revisionParts(expected)
	if !ok {
		return MutationInvalid
	}
	have, ok := revisionParts(current)
	if !ok {
		return MutationInvalid
	}
	required := []int{0}
	for _, change := range ordered {
		// A PLACEMENT deliberately does not require the location component. An
		// anchored move states its own precondition — the anchor must still be a
		// child of the destination — and that is a sharper check than "no sibling
		// anywhere has changed", which an unrelated capture elsewhere in the file
		// would otherwise fail.
		if change.Field == FieldLocation {
			if _, anchored := change.Value.Placement(); !anchored {
				required = append(required, 1)
			}
		}
		if change.Field == FieldState {
			required = append(required, 2)
		}
	}
	for _, part := range required {
		if have[part] != want[part] {
			return MutationStale
		}
	}
	return ""
}

// ApplyChangeset applies an atomic multi-field semantic change.
//
// Every field is applied to a DETACHED copy of the records first, so an invalid
// later field cannot leak a partial in-memory mutation into a file write or a
// journal step. The transaction is otherwise the one every other mutation runs
// in: lock, schema gate, preflight, precondition, apply, write, post-write
// validation, rollback, one journal step, re-read.
func (s *Store) ApplyChangeset(changeset Changeset) MutationResult {
	ordered := orderedChanges(changeset.Changes)
	for index := range ordered {
		ordered[index].Field = normalizeChangesetField(ordered[index].Field)
	}
	ordered = orderedChanges(ordered)
	if problems := validateChangeset(changeset, ordered); len(problems) > 0 {
		return MutationResult{Status: MutationInvalid, Errors: problems}
	}

	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		// A strict-revision changeset never repairs: a precondition built over
		// malformed bytes is not a precondition.
		preflight := check.CheckWith(s.org, s.checkOptions())
		if !preflight.OK() {
			messages := []string{}
			for _, entry := range preflight.Errors {
				messages = append(messages, entry.Message)
			}
			result = MutationResult{Status: MutationStoreInvalid, Errors: messages}
			return nil
		}

		records := freshRecords(s.org)
		index := locateStableIndex(records, changeset.ID)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}
		// The destination is resolved against the PRE-apply records, before any
		// field runs. A missing parent or anchor is then a not_found naming the
		// argument that was wrong, rather than a generic invalid raised halfway
		// through a partially applied changeset.
		var targets *placementTargets
		for _, change := range ordered {
			placement, anchored := change.Value.Placement()
			if change.Field != FieldLocation || !anchored {
				continue
			}
			resolved := resolvePlacementTargets(records, placement)
			if resolved.status != MutationOK {
				result = MutationResult{
					Status: resolved.status, Errors: resolved.errors,
					FieldErrors: resolved.fieldErrors,
					Summary: MutationSummary{
						From: records[index].String("parent"),
						To:   placement.ParentID, Before: placement.BeforeID,
					},
				}
				return nil
			}
			targets = &resolved
		}

		if changeset.ExpectedRevision != "" {
			current, err := taskRevision(records, index, siblingIDsByParent(records))
			if err != nil {
				result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
				return nil
			}
			if status := changesetRevisionError(current, changeset.ExpectedRevision, ordered); status != "" {
				errors := []string(nil)
				if status == MutationInvalid {
					errors = []string{"malformed expected_revision"}
				}
				result = MutationResult{Status: status, Errors: errors}
				return nil
			}
		}

		original, err := record.Dump(records)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		context, err := s.patchContext(PatchRequest{Today: changeset.Today, Context: changeset.Context})
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		context.placementTargets = targets

		working := record.CloneAll(records)
		touched := []string{}
		var summary MutationSummary
		datesTouched := false
		for _, change := range ordered {
			// The index is re-resolved per field, not carried: a `location`
			// change earlier in FIELD_ORDER physically relocates the record, so a
			// cached position would apply the NEXT field to whatever row slid
			// into the old slot.
			position := locateStableIndex(working, changeset.ID)
			if position < 0 {
				result = MutationResult{Status: MutationNotFound}
				return nil
			}
			applied := applyFieldPatch(working, position, change.Field, change.Value, context)
			if applied.status != MutationOK {
				result = MutationResult{
					Status: applied.status, Errors: applied.errors,
					FieldErrors: applied.fieldErrors, Summary: applied.summary,
				}
				return nil
			}
			touched = appendUnique(touched, applied.touchedIDs...)
			// Ruby reports the ONE field's summary for a single-field changeset
			// and a by-field map otherwise. A typed summary cannot hold the map,
			// so a multi-field changeset keeps the arm that names an action —
			// the only member any adapter reads out of it.
			if len(ordered) == 1 || applied.summary.Action != "" {
				summary = applied.summary
			}
			if containsField(dateOwningFields, change.Field) {
				datesTouched = true
			}
		}
		// Recurrence and lead are intents ABOUT a date, so a write that clears
		// the last one retires them — judged after the WHOLE changeset, never
		// mid-flight, because a change that MOVES the anchor passes through a
		// momentary dateless state and would otherwise lose a window the user
		// was relocating.
		if datesTouched {
			if position := locateStableIndex(working, changeset.ID); position >= 0 {
				clearDatelessIntent(&working[position])
			}
		}

		proposed, err := record.Dump(working)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		if proposed == original {
			snapshot, revision := s.readAfterWrite()
			result = MutationResult{
				Status: MutationNoChange, ReadSnapshot: snapshot, StoreRevision: revision,
				Summary: summary,
			}
			return nil
		}

		label := changeset.HistoryLabel
		if label == "" {
			label = changesetLabel(ordered, records[index].String("title"))
		}
		result = s.commit(before, working, label, changeset.CoalesceKey)
		if result.Status == MutationOK {
			result.TouchedIDs = touched
			result.Summary = summary
		}
		return nil
	})
	if err != nil {
		return mutationUnavailable(err)
	}
	return result
}

// changesetLabel is `changeset_history_label`: the fields, in applied order.
func changesetLabel(ordered []Change, title string) string {
	names := make([]string, 0, len(ordered))
	for _, change := range ordered {
		names = append(names, string(change.Field))
	}
	return "edit " + strings.Join(names, ", ") + ": " + title
}

func appendUnique(values []string, extra ...string) []string {
	for _, value := range extra {
		if !contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}
