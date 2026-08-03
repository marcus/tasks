package store

import (
	"os"

	"tasks-go/internal/check"

	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
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
	// HistoryConflict means the live files no longer match the state the step
	// expects: an out-of-band edit landed after the journal's tip, and replaying
	// across it would clobber that edit.
	HistoryConflict HistoryOutcome = "conflict"
	// HistoryReady means the step is applicable.
	HistoryReady HistoryOutcome = "ready"
)

// PlanHistoryStep resolves an undo (delta -1) or redo (delta +1) as far as the
// decision to apply it, under the store lock, and stops there. Everything up to
// that point is a read; the application is the write this build does not have.
//
// The ORDER of the three refusals is the contract. An unsupported schema is
// refused before the journal is even consulted, because a history that would
// restore bytes this build cannot read is not a history it may act on. Then
// "nothing to undo", then the conflict — which is the only one that names a
// label, because it is the only one where the caller could have been about to
// undo something specific.
func (s *Store) PlanHistoryStep(delta int, env determinism.Env) (HistoryOutcome, string) {
	var outcome HistoryOutcome
	var label string
	err := s.withLock(func() error {
		if source, _ := s.unsupportedSchemaSource(); source != "" {
			outcome = HistoryUnsupportedSchema
			return nil
		}
		history := journal.Open(journal.DirFor(s.org, env), s.org)
		step, ok := history.Plan(delta)
		if !ok {
			outcome = HistoryEmpty
			return nil
		}
		label = step.Label
		if !s.FileSnapshot().Equal(step.Expect) {
			outcome = HistoryConflict
			return nil
		}
		outcome = HistoryReady
		return nil
	})
	if err != nil {
		return HistoryEmpty, ""
	}
	return outcome, label
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
