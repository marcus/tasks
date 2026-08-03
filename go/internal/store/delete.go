package store

import (
	"tasks-go/internal/check"
	"tasks-go/internal/record"
)

// DeleteTask hard-deletes a task's subtree from the live file in one checked
// transaction.
//
// It follows the same transaction shape every other mutation uses — lock,
// schema gate, preflight refusal, atomic write, post-write rollback, one
// journal entry, re-read — with two rules of its own:
//
//   - Deletion is never a repair route. A field patch may fix its own invalid
//     record; a delete may not, because the record it would fix is the one it
//     is about to remove, and "the file validates now" would then be true for
//     the wrong reason. Any preflight failure refuses outright.
//   - The archive is never consulted or written. An archived-only id is simply
//     not found here, and this is not an alias for CANCELLED.
//
// `expectedRevision` is optional; empty skips the concurrency check, which is
// the CLI's convenience. A supplied revision guards ALL THREE components,
// unlike an ordinary field edit, because a cascading delete must be refused if
// the task, its siblings, or any descendant changed since it was read.
func (s *Store) DeleteTask(id string, cascade bool, expectedRevision, historyLabel string) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		if id == "" {
			result = MutationResult{Status: MutationInvalid, Errors: []string{"task id is required"}}
			return nil
		}
		if expectedRevision != "" {
			if _, ok := revisionParts(expectedRevision); !ok {
				result = MutationResult{Status: MutationInvalid,
					Errors: []string{"malformed expected_revision"}}
				return nil
			}
		}

		preflight := check.Check(s.org)
		if !preflight.OK() {
			messages := []string{}
			for _, entry := range preflight.Errors {
				messages = append(messages, entry.Message)
			}
			result = MutationResult{Status: MutationStoreInvalid, Errors: messages}
			return nil
		}

		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}
		if records[index].String("type") != "task" {
			result = MutationResult{Status: MutationInvalid, Errors: []string{"delete targets tasks"}}
			return nil
		}

		if expectedRevision != "" {
			current, err := taskRevision(records, index, siblingIDsByParent(records))
			if err != nil {
				result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
				return nil
			}
			if status := deleteRevisionError(current, expectedRevision); status != "" {
				result = MutationResult{Status: status}
				return nil
			}
		}

		end := subtreeEnd(records, index)
		removedIDs := []string{}
		descendants := 0
		openDescendants := 0
		for position := index; position < end; position++ {
			if records[position].String("type") != "task" {
				continue
			}
			removedIDs = append(removedIDs, records[position].String("id"))
			if position == index {
				continue
			}
			descendants++
			if contains(check.OpenStates, records[position].String("state")) {
				openDescendants++
			}
		}

		summary := MutationSummary{
			Removed: len(removedIDs), Descendants: descendants, OpenDescendants: openDescendants,
		}
		if !cascade && descendants > 0 {
			result = MutationResult{Status: MutationConflict, Summary: summary}
			return nil
		}

		title := records[index].String("title")
		working := record.CloneAll(records)
		working = append(working[:index:index], working[end:]...)
		// Serialize before replacing the file so an encoding or JSON error is an
		// invalid result, never a half-removed subtree.
		if _, err := record.Dump(working); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		label := historyLabel
		if label == "" {
			label = deleteHistoryLabel(title, len(removedIDs))
		}
		result = s.commit(before, working, label, "")
		if result.Status == MutationOK {
			result.TouchedIDs = removedIDs
			result.Summary = summary
		}
		return nil
	})
	if err != nil {
		return MutationResult{Status: MutationUnavailable, Errors: []string{"task store unavailable"}}
	}
	return result
}

// deleteRevisionError compares all three revision components. A cascading
// delete removes a whole subtree and changes its parent's sibling list, so
// every part of the fingerprint is in scope — unlike a field edit, which
// compares only the components its own fields can invalidate.
func deleteRevisionError(current, expected string) MutationStatus {
	want, ok := revisionParts(expected)
	if !ok {
		return MutationInvalid
	}
	have, ok := revisionParts(current)
	if !ok {
		return MutationInvalid
	}
	if have != want {
		return MutationStale
	}
	return ""
}

// deleteHistoryLabel names the undo step. A cascade says how many tasks went
// with the root, because "delete: Plan the trip" undoing eleven tasks is a
// surprise the history line should have prevented.
func deleteHistoryLabel(title string, removed int) string {
	if removed <= 1 {
		return "delete: " + title
	}
	return "delete " + itoa(removed) + " tasks: " + title
}
