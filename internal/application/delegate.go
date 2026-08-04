package application

import (
	"strings"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/store"
)

// The five delegation operations.
//
// Preconditions — eligibility, the worker match, the claim compare-and-set —
// stay in the store. What lives here is composition: the WAITING default behind
// a human delegation and the blocker note behind a release, each folded into
// the SAME undo step as the delegation write via the journal's coalesce key.
//
// Every operation returns one Outcome so a CLI, HTTP, or TUI adapter keeps one
// outcome vocabulary, with a DelegationSummary rich enough that the adapter
// translates rather than re-derives.

// NoteEncodingError is the typed refusal for note bytes this process cannot
// even trim.
//
// The blocker note is the whole point of a blocked agent's escape hatch, and it
// is appended AFTER the release has already been written. So unusable text has
// to degrade to a "not applied" the caller already handles, never to a panic on
// top of a completed, half-composed write.
const NoteEncodingError = "release note is not valid UTF-8 text"

// DelegateTask hands a task to a person (kind "human" plus an email assignee)
// or to the agent pool (kind "agent" plus refine/research/implement).
//
// A human delegation moves the task to WAITING — the next action is genuinely
// outside the owner's control, which is what WAITING means — unless KeepState
// opts out.
func (a *Application) DelegateTask(command DelegationCommand, operation *OperationContext) Outcome {
	command.Action = ActionDelegate
	return a.runDelegation(command, operation)
}

// UndelegateTask clears the marker, revoking any live claim. Lifecycle state is
// untouched: undelegating does not automatically leave WAITING, the owner
// decides when to.
func (a *Application) UndelegateTask(command DelegationCommand, operation *OperationContext) Outcome {
	command.Action = ActionUndelegate
	return a.runDelegation(command, operation)
}

// ClaimTask is the atomic pickup. A lost race is a conflict naming the winning
// holder and its timestamp; a win carries the full canonical task, so a worker
// claims and reads its authority in one step.
func (a *Application) ClaimTask(command DelegationCommand, operation *OperationContext) Outcome {
	command.Action = ActionClaim
	return a.runDelegation(command, operation)
}

// ReleaseTask hands a claim back to the agent-ready queue. A worker must supply
// the id matching the live claim; the owner passes Force to clear a stale one.
// A Note appends a blocker line to the body in the same undo step.
func (a *Application) ReleaseTask(command DelegationCommand, operation *OperationContext) Outcome {
	command.Action = ActionRelease
	return a.runDelegation(command, operation)
}

// SetWorkRef records where the work lives. One reference: setting overwrites,
// and an empty ref (or "off"/"none") clears it. The owner may always write it;
// a worker only while its claim matches.
func (a *Application) SetWorkRef(command DelegationCommand, operation *OperationContext) Outcome {
	command.Action = ActionWorkRef
	return a.runDelegation(command, operation)
}

func (a *Application) runDelegation(command DelegationCommand, operation *OperationContext) Outcome {
	if messages := command.validate(); len(messages) > 0 {
		return a.refuse(command, invalid(messages...))
	}
	if command.ExpectedRevision != "" {
		// An optimistic-concurrency guard the store cannot enforce must not be
		// accepted and dropped. Checking it here — read the revision, compare,
		// then write — would be a guard with a race inside it, which is worse
		// than none because it reads as protection.
		return a.refuse(command, invalid(
			"this build cannot honour expected_revision on a delegation — the store does not check it yet"))
	}

	// One key per operation, so only THIS operation's own follow-up write
	// merges into its journal entry, never an unrelated neighbouring edit.
	coalesceKey := "delegation-" + string(command.Action) + "-" + a.mintDelegationKey()

	// Ruby reads the replaced marker out of the store's own summary, inside the
	// write transaction. The Go store does not report it, so it is read here
	// first. The window between this read and the write is real but narrow, and
	// what it can affect is bounded: only whether the WAITING a human
	// delegation set is cleared when the agent pool replaces that person.
	previous := a.markerOf(command.ID)

	result := a.invokeDelegation(command, coalesceKey)
	if !result.OK() {
		return a.finish(result, command, Outcome{}, previous, operation)
	}
	followUp := a.delegationFollowUp(command, previous, coalesceKey, operation)
	return a.finish(result, command, followUp, previous, operation)
}

// refuse shapes an argument refusal so it still carries the action and the id.
// An adapter renders a refusal the same way it renders every other outcome.
func (a *Application) refuse(command DelegationCommand, outcome Outcome) Outcome {
	outcome.Summary.Action = string(command.Action)
	outcome.Summary.TaskID = command.ID
	outcome.Delegation = &DelegationSummary{Action: command.Action, TaskID: command.ID}
	return outcome
}

func (a *Application) invokeDelegation(command DelegationCommand, coalesceKey string) Outcome {
	target := a.store()
	switch command.Action {
	case ActionDelegate:
		return Outcome{MutationResult: target.Delegate(
			command.ID, command.Kind, command.Mode, command.Assignee, coalesceKey)}
	case ActionClaim:
		return Outcome{MutationResult: target.Claim(command.ID, command.Worker, coalesceKey)}
	case ActionUndelegate:
		if writer, ok := target.(Undelegator); ok {
			return Outcome{MutationResult: writer.Undelegate(command.ID, coalesceKey)}
		}
		return unsupported("undelegate a task")
	case ActionRelease:
		if writer, ok := target.(Releaser); ok {
			return Outcome{MutationResult: writer.Release(
				command.ID, command.Worker, command.Force, coalesceKey)}
		}
		return unsupported("release a claim")
	case ActionWorkRef:
		if writer, ok := target.(WorkRefWriter); ok {
			return Outcome{MutationResult: writer.SetWorkRef(
				command.ID, command.normalizedWorkRef(), command.Worker, coalesceKey)}
		}
		return unsupported("set a work reference")
	}
	return unsupported("perform this delegation")
}

// delegationFollowUp is the second half of a composed delegation action, or the
// zero Outcome when the command asked for none.
//
// It always runs AFTER the delegation write succeeded, so a refused delegation
// never leaves a stray state change or note behind.
func (a *Application) delegationFollowUp(command DelegationCommand, previous map[string]string,
	coalesceKey string, operation *OperationContext) Outcome {
	switch command.Action {
	case ActionDelegate:
		return a.delegateStateFollowUp(command, previous, coalesceKey, operation)
	case ActionRelease:
		return a.releaseNoteFollowUp(command, coalesceKey, operation)
	}
	return Outcome{}
}

// delegateStateFollowUp is the WAITING default, and its exact inverse.
//
// Handing a task to a person moves it to WAITING: the next action really is
// outside the owner's control. Replacing that person with the agent pool undoes
// exactly that — agent-ready work is actionable again, and a WAITING marker
// would describe it wrongly. Only a WAITING INHERITED from the human delegation
// is cleared; a WAITING the owner set for their own reasons on an undelegated
// task is theirs to keep.
func (a *Application) delegateStateFollowUp(command DelegationCommand, previous map[string]string,
	coalesceKey string, operation *OperationContext) Outcome {
	if command.KeepState {
		return Outcome{}
	}
	replacingHuman := previous != nil && previous["kind"] == "human"
	if !command.Human() && !replacingHuman {
		return Outcome{}
	}
	item, found := a.itemOf(command.ID)
	if !found {
		return Outcome{}
	}

	var target, label string
	if command.Human() {
		if item.State == "WAITING" {
			return Outcome{}
		}
		target = "WAITING"
		label = "delegate → " + command.Assignee + ": " + item.Title
	} else {
		if item.State != "WAITING" {
			return Outcome{}
		}
		target = "TODO"
		label = "agent-ready (" + command.Mode + "): " + item.Title
	}
	return a.patchField(command.ID, store.FieldState, target, label, coalesceKey, operation)
}

// releaseNoteFollowUp appends the blocker line to the body.
func (a *Application) releaseNoteFollowUp(command DelegationCommand, coalesceKey string,
	operation *OperationContext) Outcome {
	if command.Note == "" {
		return Outcome{}
	}
	// Go strings can hold bytes that are not valid UTF-8 — argv under LANG=C
	// reaches here unchanged. Ruby re-tags and then refuses; there is nothing to
	// re-tag here, so the refusal is the whole rule.
	if !utf8.ValidString(command.Note) {
		return Outcome{MutationResult: store.MutationResult{
			Status: store.MutationInvalid, Errors: []string{NoteEncodingError},
		}}
	}
	note := strings.TrimSpace(command.Note)
	if note == "" {
		return Outcome{}
	}

	queries, err := a.Queries(false, operation)
	if err != nil {
		return Outcome{}
	}
	item, found := findByID(queries, command.ID, false)
	if !found {
		return Outcome{}
	}
	body := strings.Join(queries.Body(item), "\n")
	value := note
	if body != "" {
		value = body + "\n" + note
	}
	return a.patchField(command.ID, FieldBody, value, "release: "+item.Title, coalesceKey, operation)
}

// patchField is the composed half of a delegation, written through the store's
// own single-field transaction.
//
// The coalesce key is the whole point: without it the composed write is a
// SECOND undo step, and one user action costs two undos. A store that cannot
// coalesce still performs the write — the composition is correct either way —
// and the granularity loss is named in the errors so it cannot pass silently.
func (a *Application) patchField(id string, field store.PatchField, value, label, coalesceKey string,
	operation *OperationContext) Outcome {
	target := a.store()
	if !PatchFieldSupported(target, field) {
		return unsupported("patch the " + string(field) + " field")
	}
	expected, found := target.ExpectedFor(id, field)
	if !found {
		return Outcome{MutationResult: store.MutationResult{Status: store.MutationNotFound}}
	}
	today := a.today(operation)
	if patcher, ok := target.(CoalescingPatcher); ok {
		return Outcome{MutationResult: patcher.PatchTaskCoalesced(
			id, field, value, expected, label, today, coalesceKey)}
	}
	result := target.PatchTask(id, field, value, expected, label, today)
	if result.Status == store.MutationOK {
		result.Errors = append(result.Errors,
			"composed write landed as a separate undo step — this store cannot coalesce")
	}
	return Outcome{MutationResult: result}
}

// finish folds the store result and any follow-up write into one Outcome.
//
// A failed follow-up never turns a successful delegation into a failure: the
// composed fact is reported as false in the summary and its error carried
// alongside, and the canonical task the caller renders shows the real
// post-write state either way.
func (a *Application) finish(result Outcome, command DelegationCommand, followUp Outcome,
	previous map[string]string, operation *OperationContext) Outcome {
	summary := &DelegationSummary{
		Action: command.Action, TaskID: command.ID, Previous: previous,
		Holder: result.Summary.Holder, At: result.Summary.At,
	}
	result.Summary.Action = string(command.Action)
	result.Summary.TaskID = command.ID
	result.Delegation = summary
	if !result.OK() {
		return result
	}

	// The canonical post-write resource is built from the snapshot the write
	// ITSELF produced. An idempotent repeat writes nothing and so carries no
	// snapshot; that case re-reads rather than leaving the caller without the
	// resource it needs to render.
	final := result
	if followUp.Changed() {
		final = followUp
	}
	if item := a.taskAfter(final, command.ID); item != nil {
		summary.Task = item
		summary.State = item.State
		summary.Delegation = decodeMarker(item.Delegation)
	}

	summary.StateChanged = command.Action == ActionDelegate && followUp.Changed()
	if command.Action == ActionRelease && command.Note != "" {
		summary.NoteRequested = true
		summary.NoteApplied = followUp.OK()
	}

	if final.ReadSnapshot != nil {
		result.ReadSnapshot = final.ReadSnapshot
	}
	if final.StoreRevision != "" {
		result.StoreRevision = final.StoreRevision
	}
	if len(result.TouchedIDs) == 0 {
		result.TouchedIDs = []string{command.ID}
	}
	// A successful delegation carries no errors of its own. What it may carry
	// is the follow-up's: a refusal (the composed half did not happen) or a
	// granularity warning (it happened, in its own undo step).
	result.Errors = nil
	if followUp.Status != "" {
		result.Errors = followUp.Errors
	}
	return result
}

// taskAfter resolves the canonical task from a write's own snapshot, falling
// back to a fresh read when the write produced none.
func (a *Application) taskAfter(result Outcome, id string) *store.Item {
	snapshot := result.ReadSnapshot
	if snapshot == nil {
		fresh, err := a.store().ReadSnapshot(false)
		if err != nil {
			return nil
		}
		snapshot = fresh
	}
	for _, item := range snapshot.Items() {
		if item.HasID && item.ID == id {
			found := item
			return &found
		}
	}
	return nil
}

// markerOf is the delegation marker a task carries right now, or nil.
func (a *Application) markerOf(id string) map[string]string {
	item, found := a.itemOf(id)
	if !found {
		return nil
	}
	return decodeMarker(item.Delegation)
}

// itemOf is one live task read fresh.
func (a *Application) itemOf(id string) (store.Item, bool) {
	snapshot, err := a.store().ReadSnapshot(false)
	if err != nil {
		return store.Item{}, false
	}
	for _, item := range snapshot.Items() {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}
