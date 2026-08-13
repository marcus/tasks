package store

import (
	"strings"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// The two decisions.
const (
	ProposalApprove = "approve"
	ProposalReject  = "reject"
)

// DecideProposal accepts or declines one proposal in a single checked
// transaction.
//
// It is deliberately stricter than an arbitrary state patch, and the two extra
// rules are the reason it exists as its own operation:
//
//   - it only targets PROPOSED tasks, so a decision can never silently move a
//     task that was never proposed; and
//   - a proposal tree is decided LEAVES-FIRST, so approving a parent can never
//     imply approval of the undecided work beneath it.
//
// Approval moves the task to INBOX, rejection to CANCELLED. `notes` are only
// meaningful on a rejection — withdrawal rationale appended to the body in the
// SAME write, so the reason and the decision cannot come apart.
func (s *Store) DecideProposal(id, action string, notes []string, expectedRevision, today string) MutationResult {
	if action != ProposalApprove && action != ProposalReject {
		return MutationResult{Status: MutationInvalid,
			Errors: []string{"proposal action must be approve or reject"}}
	}

	var result MutationResult
	err := s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
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

		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}
		if expectedRevision != "" {
			current, err := taskRevision(records, index, siblingIDsByParent(records))
			if err != nil {
				result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
				return nil
			}
			// The decision writes `state`, so the lifecycle component is in
			// scope alongside the task's own fields — exactly the pair a
			// changeset carrying one state change would compare.
			if status := changesetRevisionError(current, expectedRevision,
				[]Change{{Field: FieldState}}); status != "" {
				result = MutationResult{Status: status}
				return nil
			}
		}

		from := records[index].String("state")
		if !contains(check.ProposedStates, from) {
			result = MutationResult{
				Status: MutationInvalid, Errors: []string{"task is " + from + ", not PROPOSED"},
				Summary: MutationSummary{Action: action, From: from},
			}
			return nil
		}

		end := subtreeEnd(records, index)
		proposedDescendants := []string{}
		for position := index + 1; position < end; position++ {
			if records[position].String("type") == "task" &&
				contains(check.ProposedStates, records[position].String("state")) {
				proposedDescendants = append(proposedDescendants, records[position].String("id"))
			}
		}
		if len(proposedDescendants) > 0 {
			result = MutationResult{
				Status: MutationConflict, Errors: []string{"decide proposed descendants first"},
				Summary: MutationSummary{Action: action, ProposedDescendantIDs: proposedDescendants},
			}
			return nil
		}

		if len(notes) > 0 && action != ProposalReject {
			result = MutationResult{Status: MutationInvalid,
				Errors: []string{"notes are only allowed when rejecting a proposal"}}
			return nil
		}
		noteText, ok := proposalNoteText(notes)
		if !ok {
			result = MutationResult{Status: MutationInvalid,
				Errors: []string{"reject notes must be valid UTF-8 text"}}
			return nil
		}

		target := "CANCELLED"
		if action == ProposalApprove {
			target = "INBOX"
		}
		context, err := s.patchContext(PatchRequest{Today: today})
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		// Approval is the one write allowed to leave an accepted task under a
		// still-proposed ancestor: the tree is decided leaves-first, so the
		// parent's own approval is the very next step.
		context.allowProposedAncestor = action == ProposalApprove

		title := records[index].String("title")
		working := record.CloneAll(records)
		applied := patchState(working, index, TextValue(target), context)
		if applied.status != MutationOK {
			result = MutationResult{Status: applied.status, Errors: applied.errors,
				Summary: applied.summary}
			return nil
		}
		if noteText != "" {
			working[index].SetOptional("body",
				record.RawString(appendBody(working[index].String("body"), noteText)))
		}
		if _, err := record.Dump(working); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, working, action+" proposal: "+title, "")
		if result.Status == MutationOK {
			result.TouchedIDs = []string{id}
			result.Summary = MutationSummary{Action: action, From: "PROPOSED", To: target}
		}
		return nil
	})
	if err != nil {
		return mutationUnavailable(err)
	}
	return result
}

// proposalNoteText joins reject notes the way `propose` joins repeatable
// `--note` values. It reports ok=false when any note is not valid UTF-8, which
// is a typed refusal rather than a store that quietly accepts bytes no reader
// can decode.
func proposalNoteText(notes []string) (string, bool) {
	if len(notes) == 0 {
		return "", true
	}
	for _, note := range notes {
		if !utf8.ValidString(note) {
			return "", false
		}
	}
	return strings.Join(notes, "\n"), true
}

// appendBody appends one line to an existing body, or starts one.
func appendBody(body, line string) string {
	if body == "" {
		return line
	}
	return body + "\n" + line
}
