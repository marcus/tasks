package store

import (
	"os"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/journal"
)

// FileSnapshot is the raw bytes of both task files right now. It is what the
// journal's states are compared against, so absence has to be representable: an
// archive that does not exist yet is a different state from an empty one.
func (s *Store) FileSnapshot() journal.Snapshot {
	return journal.Snapshot{Org: readOptional(s.org), Archive: readOptional(s.archive)}
}

func readOptional(path string) *string {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(bytes)
	return &text
}

// HistoryOutcome is what a planned undo or redo would do, without doing it.
type HistoryOutcome string

const (
	// HistoryUnsupportedSchema means this build cannot read the store at all.
	HistoryUnsupportedSchema HistoryOutcome = "unsupported_schema"
	// HistoryEmpty means there is no step in that direction — no journal, an
	// exhausted one, or one whose blobs no longer verify.
	HistoryEmpty HistoryOutcome = "empty"
	// HistoryConflict means the step could not be applied: the live files no
	// longer match the state it expects — an out-of-band edit landed after the
	// journal's tip and replaying across it would clobber that edit — or the
	// application itself failed and the files were put back.
	HistoryConflict HistoryOutcome = "conflict"
	// HistoryOK means the step was applied and the cursor committed.
	HistoryOK HistoryOutcome = "ok"
	// HistoryUnavailable means the store lock could not be taken in time.
	// It is not HistoryEmpty: there may well be a step, and claiming otherwise
	// would send the caller looking for a missing journal.
	HistoryUnavailable HistoryOutcome = "unavailable"
	// HistoryStale means the caller pinned the step to a store revision and
	// the files have moved since: another surface wrote, so the step the
	// caller saw labelled is no longer necessarily the one that would apply.
	// Nothing was consulted beyond the schema gate and nothing was written.
	HistoryStale HistoryOutcome = "stale_store_revision"
)

// HistoryPeek is what the next undo and redo would do, read without applying
// either. A nil label is "nothing in that direction".
//
// The labels are the journal's plan, not a promise: a store edited out of band
// after the journal's tip still peeks a label, and the step itself then refuses
// as a conflict. StoreRevision is the revision the labels were read against,
// which is exactly the precondition a guarded step takes.
type HistoryPeek struct {
	Undo          *string
	Redo          *string
	StoreRevision string
	// UnsupportedSchema is set when this build cannot read the store; the
	// journal is not consulted at all then, matching HistoryStep's order.
	UnsupportedSchema bool
	// Unavailable is the lock diagnostic when the peek could not be taken.
	Unavailable string
}

// HistoryStepResult is one guarded undo or redo: the outcome, the label of the
// step it concerned, and the store revision the caller should hold next — the
// new revision after a step that applied, or the current one after a stale
// refusal, so a client can re-peek without guessing.
type HistoryStepResult struct {
	Outcome       HistoryOutcome
	Label         string
	StoreRevision string
}

// PeekHistory plans both directions under the shared lock, so the two labels
// and the revision describe one moment of the journal and the files.
func (s *Store) PeekHistory() HistoryPeek {
	var peek HistoryPeek
	err := s.withSharedLock(func() error {
		if source, _ := s.unsupportedSchemaSource(); source != "" {
			peek = HistoryPeek{UnsupportedSchema: true}
			return nil
		}
		history := s.journal()
		if step, ok := history.Plan(-1); ok {
			label := step.Label
			peek.Undo = &label
		}
		if step, ok := history.Plan(1); ok {
			label := step.Label
			peek.Redo = &label
		}
		peek.StoreRevision = revisionOfSnapshot(s.FileSnapshot())
		return nil
	})
	if err != nil {
		return HistoryPeek{Unavailable: UnavailableMessage(err)}
	}
	return peek
}

// StoreRevision is the global revision of the two files as they are right now:
// the same digest CheckedReadSnapshot reports, taken over the same bytes, but
// without parsing, validating, or building a snapshot. It is what a change
// poll or a conditional GET compares, and it is cheap precisely because it
// asserts nothing about whether the bytes are valid.
//
// It takes the shared lock so the two files are read as one moment: an archive
// sweep rewrites both, and a digest of the old live file beside the new
// archive would name a state that never existed.
func (s *Store) StoreRevision() (string, error) {
	var revision string
	err := s.withSharedLock(func() error {
		revision = revisionOfSnapshot(s.FileSnapshot())
		return nil
	})
	if err != nil {
		return "", err
	}
	return revision, nil
}

// revisionOfSnapshot is StoreRevisionForContents over a captured snapshot. An
// absent half stays nil and an empty file stays a present empty slice, because
// the digest distinguishes the two exactly as the checked read does.
func revisionOfSnapshot(snapshot journal.Snapshot) string {
	return StoreRevisionForContents(snapshotBytes(snapshot.Org), snapshotBytes(snapshot.Archive))
}

func snapshotBytes(text *string) []byte {
	if text == nil {
		return nil
	}
	raw := []byte(*text)
	if raw == nil {
		raw = []byte{}
	}
	return raw
}

// HistoryStep applies an undo (delta -1) or redo (delta +1) planned by the
// journal, under the lock so the plan and its commit cannot race another
// writer. It is Store#history_step, whole: restore, gate, commit, roll back.
//
// The ORDER of the refusals is the contract. An unsupported schema is
// refused before the journal is even consulted, because a history that would
// restore bytes this build cannot read is not a history it may act on. Then
// "nothing to undo", then the conflict — which is the only one that names a
// label, because it is the only one where the caller could have been about to
// undo something specific.
//
// Everything after the conflict check is the write half, and its shape is one
// promise: an undo either lands completely or leaves the files as it found
// them. The cursor commit is LAST, so a failed file restore never has a cursor
// to undo; and the cursor is rolled back before the files are, so a rollback
// that cannot finish leaves a stale cursor pointing at a step that still
// exists rather than a committed cursor pointing at bytes that never landed.
func (s *Store) HistoryStep(delta int) (HistoryOutcome, string) {
	result := s.GuardedHistoryStep(delta, "")
	return result.Outcome, result.Label
}

// GuardedHistoryStep is HistoryStep pinned to the store revision the caller
// last saw. An empty expectedRevision is no precondition at all, which is
// what the CLI and TUI pass: they act on the journal as it stands.
//
// The revision check sits between the schema gate and the plan. After the
// schema gate because a store this build cannot read has no revision worth
// comparing; before the plan because "nothing to undo" and "conflict" are
// answers about a store the caller has not seen, and the honest answer to a
// caller holding stale state is that it is stale.
func (s *Store) GuardedHistoryStep(delta int, expectedRevision string) HistoryStepResult {
	var outcome HistoryOutcome
	var label, revision string
	err := s.withLock(func() error {
		if source, _ := s.unsupportedSchemaSource(); source != "" {
			outcome = HistoryUnsupportedSchema
			return nil
		}
		if expectedRevision != "" {
			if current := revisionOfSnapshot(s.FileSnapshot()); current != expectedRevision {
				outcome, revision = HistoryStale, current
				return nil
			}
		}
		history := s.journal()
		step, ok := history.Plan(delta)
		if !ok {
			outcome = HistoryEmpty
			return nil
		}
		label = step.Label
		before := s.FileSnapshot()
		if !before.Equal(step.Expect) {
			outcome = HistoryConflict
			return nil
		}

		if err := s.restore(step.Target); err != nil {
			// Nothing was committed, so only the files need putting back.
			s.rollbackHistoryFiles(before)
			outcome = HistoryConflict
			return nil
		}
		// A journaled snapshot can pre-date a repair: restoring it would write a
		// state that fails today's invariants. Gate the restored live file the
		// same way a forward mutation is gated. A nil target org is the empty
		// first-run state — no file to validate — so the gate is skipped there.
		//
		// A step marked `repair` is the exception: it recorded a deliberate
		// repair whose before-state was the malformed record the user asked to
		// fix, so undo must faithfully restore those invalid bytes rather than
		// refuse. The automatic id repair is never so marked, so its undo stays
		// gated.
		if step.Target.Org != nil && !step.Repair && !check.CheckWith(s.org, s.checkOptions()).OK() {
			s.rollbackHistoryFiles(before)
			outcome = HistoryConflict
			return nil
		}
		if !history.Commit(step.To) {
			if s.rollbackHistoryCursor(history, step) {
				s.rollbackHistoryFiles(before)
			}
			outcome = HistoryConflict
			return nil
		}
		outcome = HistoryOK
		revision = revisionOfSnapshot(s.FileSnapshot())
		return nil
	})
	if err != nil {
		return HistoryStepResult{Outcome: HistoryUnavailable, Label: UnavailableMessage(err)}
	}
	return HistoryStepResult{Outcome: outcome, Label: label, StoreRevision: revision}
}

// rollbackHistoryCursor puts the journal cursor back after a failed commit.
// The cursor commit is last, so a failed file restore never needs this path.
// One retry: an atomic replace that failed transiently usually succeeds on the
// second attempt, and a cursor left forward of the files is the one state that
// makes the next undo silently wrong rather than merely refused.
func (s *Store) rollbackHistoryCursor(history *journal.Journal, step journal.Step) bool {
	for attempt := 0; attempt < 2; attempt++ {
		if history.Rollback(step) {
			return true
		}
	}
	return false
}

// rollbackHistoryFiles puts both files back to a captured snapshot.
//
// Atomic replacement means a failed attempt leaves either the complete old file
// or the complete new one, so retrying once is safe and covers a transient
// failure. The exact snapshot comparison — including a nil half, which is a
// file that must not exist — avoids rewriting a path that never changed and
// keeps a persistent failure loss-safe rather than torn.
func (s *Store) rollbackHistoryFiles(before journal.Snapshot) bool {
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.restore(before); err != nil {
			continue
		}
		if s.FileSnapshot().Equal(before) {
			return true
		}
	}
	return false
}

// UnsupportedSchemaError is the diagnostic for a store this build cannot read,
// or "" when it can. Every refusal leads with it, because the version found and
// the version expected are the only part an operator can act on.
func (s *Store) UnsupportedSchemaError() string {
	source, declared := s.unsupportedSchemaSource()
	if source == "" {
		return ""
	}
	prefix := ""
	if source == SourceArchive {
		prefix = "archive: "
	}
	return prefix + check.UnsupportedVersionMessage(declared)
}
