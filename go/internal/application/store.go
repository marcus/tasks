package application

import "tasks-go/internal/store"

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

// Deleter is the undoable hard delete of one live task.
type Deleter interface {
	DeleteTask(id string, cascade bool, expectedRevision, historyLabel string) store.MutationResult
}

// ProposalDecider approves or declines one proposal.
type ProposalDecider interface {
	DecideProposal(id, action string, notes []string, expectedRevision, today string) store.MutationResult
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
	ArchiveProject(id string) (moved []string, proposedDescendants bool, found bool)
	LastRollback() (reason string, stage store.RollbackStage)
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
