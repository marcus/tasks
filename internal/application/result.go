package application

import (
	"encoding/json"

	"github.com/marcus/tasks/internal/store"
)

// ReadStatus is ApplicationReadResult::STATUSES.
type ReadStatus string

// The read vocabulary. An HTTP adapter maps these to 200/404/503 without ever
// receiving a store, a path, or a second change-token read.
const (
	ReadOK                ReadStatus = "ok"
	ReadNotFound          ReadStatus = "not_found"
	ReadUnsupportedSchema ReadStatus = "unsupported_schema"
	ReadStoreInvalid      ReadStatus = "store_invalid"
	ReadUnavailable       ReadStatus = "unavailable"
)

// ReadResult is the immutable outcome of a coherent application read —
// lib/tasks/application_read_result.rb.
//
// Ruby's version carries an untyped `data` and deep-freezes it. This one is
// generic instead: the immutability Ruby buys with freeze is bought here by the
// data being a value the caller owns a copy of, and what is gained is that a
// caller reading `result.Data.Title` is checked by the compiler rather than by
// a test.
type ReadResult[T any] struct {
	Status        ReadStatus
	Data          T
	StoreRevision string
	Errors        []store.Entry
	Warnings      []store.Entry
}

// OK reports a read that produced data.
func (r ReadResult[T]) OK() bool { return r.Status == ReadOK }

// NotFound reports a well-formed read whose subject does not exist.
func (r ReadResult[T]) NotFound() bool { return r.Status == ReadNotFound }

// UnsupportedSchema reports a store this build must not interpret.
func (r ReadResult[T]) UnsupportedSchema() bool { return r.Status == ReadUnsupportedSchema }

// StoreInvalid reports bytes that parsed but failed validation.
func (r ReadResult[T]) StoreInvalid() bool { return r.Status == ReadStoreInvalid }

// Unavailable reports a store that could not be read at all.
func (r ReadResult[T]) Unavailable() bool { return r.Status == ReadUnavailable }

// readStatusOf maps a checked-read status onto the read vocabulary. The two
// vocabularies agree name for name; the mapping exists so a store status that
// gains a member cannot silently become "ok" here.
func readStatusOf(status store.Status) ReadStatus {
	switch status {
	case store.StatusOK:
		return ReadOK
	case store.StatusUnsupportedSchema:
		return ReadUnsupportedSchema
	case store.StatusStoreInvalid:
		return ReadStoreInvalid
	case store.StatusUnavailable:
		return ReadUnavailable
	}
	return ReadUnavailable
}

// Outcome is the application-level result of a mutation.
//
// It embeds the store's MutationResult unchanged — status, errors, touched ids,
// the post-write snapshot, the global revision, and the rollback record all
// keep one vocabulary across the whole system — and adds only what the
// application itself composed.
//
// Ruby merges the composed facts into MutationResult's untyped `summary` Hash.
// That is not available here: the store's Summary is a typed struct this
// package does not own and must not widen. Naming the composed half separately
// is also the more honest shape — Delegation is present exactly when a
// delegation verb produced it.
type Outcome struct {
	store.MutationResult

	// Delegation is the rich per-operation summary of a delegation verb, or nil
	// for every other operation.
	Delegation *DelegationSummary

	// Project is the count a project lifecycle command reports, or nil.
	Project *ProjectSummary
}

// The refusal predicates an adapter branches on. The store publishes OK,
// Changed, ExitCode and FirstError; these are the remaining members of the same
// vocabulary, named here so no surface has to compare status strings.
func (o Outcome) NoChange() bool { return o.Status == store.MutationNoChange }
func (o Outcome) NotFound() bool { return o.Status == store.MutationNotFound }
func (o Outcome) Stale() bool    { return o.Status == store.MutationStale }
func (o Outcome) Invalid() bool  { return o.Status == store.MutationInvalid }
func (o Outcome) Conflict() bool { return o.Status == store.MutationConflict }
func (o Outcome) RolledBackWrite() bool {
	return o.RolledBack && o.RollbackStage == store.RollbackWrite
}

// RolledBackValidation reports the write that LANDED and was refused by the
// post-write check. `check` is the actionable next step only in this case.
func (o Outcome) RolledBackValidation() bool {
	return o.RolledBack && o.RollbackStage == store.RollbackValidation
}

// ProjectSummary is what a project lifecycle command counted: the tasks a
// completion closed, or the records an archive sweep moved.
type ProjectSummary struct {
	Closed   int
	Archived int
	State    string
	ClosedAt string
}

// DelegationSummary is the composed outcome of one delegation verb, rich enough
// that a CLI, HTTP, or TUI adapter TRANSLATES rather than re-derives.
type DelegationSummary struct {
	// Action is the verb performed, and TaskID the stable id it touched. Both
	// are always set, including on a refusal.
	Action DelegationAction
	TaskID string

	// Previous is the delegation marker as it stood BEFORE the write, and nil
	// when the task carried none.
	Previous map[string]string
	// Delegation is the marker after the write, and nil when it was cleared.
	Delegation map[string]string

	// Task is the canonical post-write task, and nil only when the task
	// vanished under a concurrent write. State is its lifecycle state.
	Task  *store.Item
	State string

	// StateChanged reports that this operation moved the task to (or out of)
	// WAITING as the composed half of a delegation.
	StateChanged bool

	// NoteRequested reports that a release carried a blocker note at all;
	// NoteApplied whether that note was appended. The pair is Ruby's
	// conditionally-present `note_applied` key made explicit: a false
	// NoteApplied means something different when no note was asked for.
	NoteRequested bool
	NoteApplied   bool

	// Holder and At describe the claim that BLOCKED this operation. They are
	// the whole content of a conflict.
	Holder string
	At     string
}

// decodeMarker reads a stored delegation object into flat strings. Nested and
// non-string members are dropped rather than stringified: every member the
// application reasons about (kind, status, mode, assignee, at, work_ref) is a
// string, and an unknown nested key is explicitly not an error.
func decodeMarker(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return nil
	}
	marker := map[string]string{}
	for key, value := range decoded {
		if text, ok := value.(string); ok {
			marker[key] = text
		}
	}
	return marker
}
