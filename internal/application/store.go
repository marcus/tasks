package application

import "github.com/marcus/tasks/internal/store"

// Store is the persistence seam every application operation runs through.
//
// It is deliberately an interface owned by THIS package rather than a concrete
// *store.Store. The application layer composes store operations; it must never
// re-implement the store's validation, transaction, or rollback rules, and an
// interface it declares is the cheapest way to keep that honest — anything the
// application could only do by reaching past this interface is, by
// construction, store work.
//
// The method set here is what `*store.Store` implements TODAY. Capabilities the
// store is still growing are declared below as small optional interfaces the
// application type-asserts for, so a store that gains one is picked up without
// a change here, and a store that lacks one produces an explicit typed refusal
// rather than a silently different behavior.
type Store interface {
	// Org and Archive are the resolved paths. The application needs Org to
	// answer ReadModel.Stale, which is a question about the file rather than
	// about any snapshot.
	Org() string
	Archive() string

	ReadSnapshot(includeArchive bool) (*store.Snapshot, error)
	CheckedReadSnapshot() (store.CheckedRead, error)

	CreateTask(command store.CreateCommand, today string) store.MutationResult
	PatchTask(id string, field store.PatchField, value, expected, label, today string) store.MutationResult
	ExpectedFor(id string, field store.PatchField) (string, bool)

	Delegate(id, kind, mode, assignee, coalesceKey string) store.MutationResult
	Claim(id, worker, coalesceKey string) store.MutationResult
}

// The production store satisfies the seam. A compile error here is the signal
// that a store change moved the boundary, which is exactly when the application
// owner should look rather than find out from a failing test.
var _ Store = (*store.Store)(nil)

// -- optional capabilities ----------------------------------------------------
//
// Each interface below names ONE thing `lib/tasks/application.rb` composes that
// the Go store has not grown yet. They are separate interfaces rather than one
// big one so that a store which gains three of the five is immediately better,
// not still entirely unsupported.

// CoalescingPatcher is a patch that carries a journal coalesce key.
//
// This is the single most load-bearing gap. Every composed application
// operation — the WAITING default behind a human delegation, the blocker note
// behind a release — is a SECOND write that must land in the SAME undo step as
// the first. Ruby does that with `coalesce_key:`; without it here, one user
// action costs two undos.
type CoalescingPatcher interface {
	PatchTaskCoalesced(id string, field store.PatchField, value, expected, label, today, coalesceKey string) store.MutationResult
}

// TypedPatcher applies a patch whose value is not a string.
//
// The string spelling `Store.PatchTask` takes covers every field whose value IS
// a string, with "" meaning nil for the clearable ones. It cannot express the
// three shapes the task editor needs: `deferred` is a bool, and `contexts` and
// `tags` are ordered lists. Sending those as text reaches the store's own
// refusal ("contexts must be a list of tags") about a value the user never
// typed — so the editor either has to refuse the field, which is half-work, or
// the application layer has to carry the value's real shape.
//
// This is the narrow way to carry it: `store.PatchRequest` is the store's OWN
// typed entry point and already exists, so no store file changes and no
// existing string caller moves. A store that lacks the method produces a typed
// refusal rather than a silently different behavior, exactly like every other
// optional capability here.
type TypedPatcher interface {
	Patch(request store.PatchRequest) store.MutationResult
}

// DelegationWriter is the whole delegation surface in one call: every verb, the
// three-part marker including its note, and the optimistic-concurrency token an
// HTTP If-Match carries.
//
// It supersedes the four narrow interfaces below for a store that has it. They
// stay because a store that implements only some of them is still usefully
// better than one that implements none, and because dropping them would be an
// unrelated breaking change to a seam this package owns.
//
// Two capabilities exist ONLY here. Writing the delegation note in the same
// store transaction as the delegation is what makes a three-part delegate one
// undo step instead of two writes an adapter composed; and honouring
// ExpectedRevision is what lets the HTTP delegation routes keep their mandatory
// If-Match rather than refuse.
type DelegationWriter interface {
	WriteDelegation(request store.DelegationRequest) store.MutationResult
}

// Undelegator clears a delegation marker, revoking any live claim.
type Undelegator interface {
	Undelegate(id, coalesceKey string) store.MutationResult
}

// Releaser hands a claim back to the agent-ready queue.
type Releaser interface {
	Release(id, worker string, force bool, coalesceKey string) store.MutationResult
}

// WorkRefWriter records (or, with an empty ref, clears) where the work lives.
type WorkRefWriter interface {
	SetWorkRef(id, workRef, worker, coalesceKey string) store.MutationResult
}

// ArchiveSweeper is the list-wide archive: preview what a sweep would move,
// then move it, pinned to the preview the user was shown.
//
// The pin is the whole safety property. A user reads "would move 3 roots and 7
// descendants", walks away, comes back, and presses y — and by then the list
// may have changed. Passing the preview back means the store refuses rather
// than archiving a set the user never saw.
type ArchiveSweeper interface {
	ArchivePreviewFor(today string) store.ArchivePreview
	ArchiveSweep(today string, expected *store.ArchivePreview) store.ArchiveResult
}

// HistoryStepper is undo and redo over the journal.
type HistoryStepper interface {
	HistoryStep(delta int) (store.HistoryOutcome, string)
}

// Placer is a whole-task atomic changeset plus the revision that guards it.
//
// Ordering needs both: a move is a `location` change guarded by the revision
// the caller read, so a task that changed underneath refuses instead of landing
// somewhere the user did not choose.
type Placer interface {
	ApplyChangeset(changeset store.Changeset) store.MutationResult
	TaskRevision(id string) (string, bool)
}

// Deleter is the undoable hard delete of one live task.
type Deleter interface {
	DeleteTask(id string, cascade bool, expectedRevision, historyLabel string) store.MutationResult
}

// ProposalDecider approves or declines one proposal.
type ProposalDecider interface {
	DecideProposal(id, action string, notes []string, expectedRevision, today string) store.MutationResult
}

// ProposalApproveCompleter accepts a proposal and completes it in one write.
type ProposalApproveCompleter interface {
	ApproveAndCompleteProposal(id, expectedRevision, today string) store.MutationResult
}

// ProposalUnrejecter returns a declined proposal to PROPOSED in place.
type ProposalUnrejecter interface {
	UnrejectProposal(id, expectedRevision, today string) store.MutationResult
}

// ProjectWriter is the four project lifecycle operations.
//
// They are the only store calls in the whole application layer that report
// failure through a bare boolean or count rather than a MutationResult, which
// is why LastRollback exists: for these, the store's recorded rollback is the
// ONLY evidence that a mutation wrote and reverted.
type ProjectWriter interface {
	CreateProject(title string) store.MutationResult
	RenameSection(id, title string) (touched string, found bool)
	CompleteProject(id, today string) (closed int, found bool)
	DropProject(id, today string) (closed int, found bool)
	ReopenProject(id string) (reopened bool, found bool)
	ArchiveProject(id, today string) (moved []string, proposedDescendants bool, found bool)
	LastRollback() (reason string, stage store.RollbackStage)
	LastLockError() string
}

// -- capability probes --------------------------------------------------------

// supportedPatchFields is the last-resort assumption for a Store that does NOT
// publish its own vocabulary through FieldPatcher.
//
// It is deliberately the two fields every store has always had, not a mirror
// of the current one. *store.Store publishes its set now, so this is reached
// only by an alternative Store — a test double, or a future adapter — and
// guessing wide on its behalf would refuse nothing and let an unpatchable
// field reach a transaction that falls through its switch.
var supportedPatchFields = map[store.PatchField]bool{
	store.FieldPriority: true,
	store.FieldState:    true,
}

// FieldBody is the body patch the release-note composition needs.
const FieldBody = store.FieldBody

// FieldPatcher is a store that publishes its own patchable field set. A store
// that implements it is believed over the default above, which is what lets the
// vocabulary widen from the side that owns it — as *store.Store now does,
// across all fourteen fields it patches.
type FieldPatcher interface {
	PatchesField(field store.PatchField) bool
}

// PatchFieldSupported reports whether a store can patch a field at all.
//
// Asking BEFORE the transaction is what keeps an unimplemented field from
// falling through the store's switch and returning a status no vocabulary
// contains — a failure that would reach the user as an empty error.
func PatchFieldSupported(target Store, field store.PatchField) bool {
	if patcher, ok := target.(FieldPatcher); ok {
		return patcher.PatchesField(field)
	}
	return supportedPatchFields[field]
}
