package application

import "tasks-go/internal/store"

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
