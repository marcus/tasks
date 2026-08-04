package application

import "github.com/marcus/tasks/internal/store"

// Patch is one single-field command — the Go shape of lib/tasks/task_patch.rb.
//
// A single-field command keeps the field-owned expectation while sharing the
// store transaction a whole-task changeset uses. That is what lets an editor
// save one field without turning an unrelated concurrent edit into a whole-task
// conflict.
//
// Expected is the baseline the caller read before deciding. Ruby carries it
// inside an EditSnapshot that also holds every other field and the task's
// revision; here it is the one value the conflict check actually compares,
// which is also the only value the store publishes for the purpose.
type Patch struct {
	ID       string
	Field    store.PatchField
	Value    string
	Expected string
	// HistoryLabel names the undo step. Empty means the store's default.
	HistoryLabel string
	// CoalesceKey merges this write into a neighbouring journal entry — the
	// editor-session behavior, where a burst of field saves is one undo step.
	CoalesceKey string
	// Typed carries the value's REAL shape for the fields a string cannot
	// express: `deferred` (a bool) and `contexts`/`tags` (ordered lists).
	//
	// When it is set, Value is ignored. Both spellings exist because the CLI
	// and the API only ever send text, and making them construct a typed value
	// would be ceremony; the editor genuinely cannot.
	Typed *store.PatchValue
}

// TypedPatch builds a patch carrying a value shape a string cannot express.
func TypedPatch(id string, field store.PatchField, value store.PatchValue,
	expected, label, coalesceKey string) Patch {

	return Patch{
		ID: id, Field: field, Expected: expected, HistoryLabel: label,
		CoalesceKey: coalesceKey, Typed: &value,
	}
}

// Baseline is the value a caller reads before proposing a patch, and false when
// the task or the field has none.
//
// It is exported because the conflict check is only meaningful when the SAME
// expression produces the value on both sides of the decision.
func (a *Application) Baseline(id string, field store.PatchField) (string, bool) {
	target := a.store()
	if !PatchFieldSupported(target, field) {
		return "", false
	}
	return target.ExpectedFor(id, field)
}

// PatchTask applies one field-owned semantic change.
func (a *Application) PatchTask(patch Patch, operation *OperationContext) Outcome {
	if trimmed(patch.ID) == "" {
		return invalid("task id is required")
	}
	target := a.store()
	if !PatchFieldSupported(target, patch.Field) {
		return unsupported("patch the " + string(patch.Field) + " field")
	}
	today := a.today(operation)
	if patch.Typed != nil {
		patcher, ok := target.(TypedPatcher)
		if !ok {
			return unsupported("patch the " + string(patch.Field) + " field with a typed value")
		}
		return Outcome{MutationResult: patcher.Patch(store.PatchRequest{
			ID: patch.ID, Field: patch.Field, Value: *patch.Typed,
			Expected: patch.Expected, Label: patch.HistoryLabel, Today: today,
			CoalesceKey: patch.CoalesceKey,
		})}
	}
	if patch.CoalesceKey != "" {
		if patcher, ok := target.(CoalescingPatcher); ok {
			return Outcome{MutationResult: patcher.PatchTaskCoalesced(
				patch.ID, patch.Field, patch.Value, patch.Expected,
				patch.HistoryLabel, today, patch.CoalesceKey)}
		}
	}
	return Outcome{MutationResult: target.PatchTask(
		patch.ID, patch.Field, patch.Value, patch.Expected, patch.HistoryLabel, today)}
}

// DeleteTask is the undoable hard delete of one live task.
//
// A task with descendants is refused unless Cascade is set. ExpectedRevision is
// optional — empty skips the concurrency check, which is the CLI convenience,
// and a supplied value guards the whole subtree.
func (a *Application) DeleteTask(command DeleteCommand, _ *OperationContext) Outcome {
	if trimmed(command.ID) == "" {
		return invalid("task id is required")
	}
	writer, ok := a.store().(Deleter)
	if !ok {
		return unsupported("delete a task")
	}
	return Outcome{MutationResult: writer.DeleteTask(
		command.ID, command.Cascade, command.ExpectedRevision, command.HistoryLabel)}
}

// ApproveTask accepts a proposal.
func (a *Application) ApproveTask(id, expectedRevision string, operation *OperationContext) Outcome {
	return a.DecideProposal(ProposalDecision{
		ID: id, Action: ProposalApprove, ExpectedRevision: expectedRevision,
	}, operation)
}

// RejectTask declines a proposal into CANCELLED. Notes append withdrawal
// rationale to the body in the same write — the application-level mirror of the
// repeatable CLI `reject --note`.
func (a *Application) RejectTask(id, expectedRevision string, notes []string, operation *OperationContext) Outcome {
	return a.DecideProposal(ProposalDecision{
		ID: id, Action: ProposalReject, ExpectedRevision: expectedRevision, Notes: notes,
	}, operation)
}

// DecideProposal is the typed decision seam both verbs share.
//
// A malformed decision is a REFUSAL rather than a panic. Ruby raises
// ArgumentError from the command constructor and the application rescues it
// into an :invalid result; the rescue is the behavior that matters, so this
// build simply never raises.
func (a *Application) DecideProposal(decision ProposalDecision, operation *OperationContext) Outcome {
	if messages := decision.validate(); len(messages) > 0 {
		return invalid(messages...)
	}
	writer, ok := a.store().(ProposalDecider)
	if !ok {
		return unsupported("decide a proposal")
	}
	return Outcome{MutationResult: writer.DecideProposal(
		decision.ID, string(decision.Action), copyOf(decision.Notes),
		decision.ExpectedRevision, a.today(operation))}
}

// -- the list-wide operations the TUI drives ------------------------------------
//
// Each is a thin pass-through to an optional store capability, in the same
// shape as every other application operation: a typed refusal when the store
// cannot do it, never a silently different behavior.

// ArchivePreview is what a sweep would move right now.
func (a *Application) ArchivePreview(operation *OperationContext) (store.ArchivePreview, bool) {
	sweeper, ok := a.store().(ArchiveSweeper)
	if !ok {
		return store.ArchivePreview{}, false
	}
	return sweeper.ArchivePreviewFor(a.today(operation)), true
}

// ArchiveSweep moves every fully closed subtree to the archive file, pinned to
// the preview the caller showed the user.
func (a *Application) ArchiveSweep(expected *store.ArchivePreview,
	operation *OperationContext) (ArchiveOutcome, bool) {

	target := a.store()
	sweeper, ok := target.(ArchiveSweeper)
	if !ok {
		return ArchiveOutcome{}, false
	}
	outcome := ArchiveOutcome{ArchiveResult: sweeper.ArchiveSweep(a.today(operation), expected)}
	// A sweep that wrote and rolled back reports only `Failed`; the REASON is
	// recorded on the store, and it is the only evidence the write happened at
	// all — the files look identical either way.
	if outcome.Failed {
		if writer, hasRollback := target.(ProjectWriter); hasRollback {
			outcome.RollbackReason, outcome.RollbackStage = writer.LastRollback()
		}
	}
	return outcome, true
}

// ArchiveOutcome is a sweep plus the rollback record a failed one leaves.
type ArchiveOutcome struct {
	store.ArchiveResult
	RollbackReason string
	RollbackStage  store.RollbackStage
}

// HistoryStep applies one undo (-1) or redo (+1).
func (a *Application) HistoryStep(delta int) (store.HistoryOutcome, string, bool) {
	stepper, ok := a.store().(HistoryStepper)
	if !ok {
		return "", "", false
	}
	outcome, label := stepper.HistoryStep(delta)
	return outcome, label, true
}

// UpdateTask applies several field changes to one task ATOMICALLY, guarded by
// the revision the caller read.
//
// Atomicity is the point, not a convenience. `z now` clears the hold AND the
// available-from date; landing those as two patches would leave an observable
// intermediate state where the task is released but still dated, and would cost
// the user two undos for one keystroke.
func (a *Application) UpdateTask(id string, changes []store.Change, label string,
	operation *OperationContext) (Outcome, bool) {

	placer, ok := a.store().(Placer)
	if !ok {
		return Outcome{}, false
	}
	revision, _ := placer.TaskRevision(id)
	return Outcome{MutationResult: placer.ApplyChangeset(store.Changeset{
		ID: id, Changes: changes, ExpectedRevision: revision, HistoryLabel: label,
		Today: a.today(operation), Context: a.contextFor(operation),
	})}, true
}

// MoveTask relocates one subtree, guarded by the revision the caller read.
func (a *Application) MoveTask(id string, placement store.Placement, label string,
	operation *OperationContext) (Outcome, bool) {

	placer, ok := a.store().(Placer)
	if !ok {
		return Outcome{}, false
	}
	revision, _ := placer.TaskRevision(id)
	return Outcome{MutationResult: placer.ApplyChangeset(store.Changeset{
		ID:               id,
		Changes:          []store.Change{{Field: store.FieldLocation, Value: store.PlacementValue(placement.ParentID, placement.BeforeID)}},
		ExpectedRevision: revision, HistoryLabel: label,
		Today: a.today(operation), Context: a.contextFor(operation),
	})}, true
}
