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

// ProposalUnreject is the restore verb's action name. It is deliberately NOT a
// third value of the approve/reject vocabulary: those two decide an undecided
// proposal, while this one undoes a decision, with rules of its own.
const ProposalUnreject = "unreject"

// ProposalApproveComplete names the compound decision in a mutation summary. It
// is not a fourth decision the caller may ASK for by name — approve+complete has
// its own entry point — but the summary has to be able to say that the write did
// both, because "approve" alone would understate what changed.
const ProposalApproveComplete = "approve_complete"

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
	return s.decideProposal(id, action, notes, expectedRevision, today, false)
}

// ApproveAndCompleteProposal accepts a proposal AND completes the accepted task
// in ONE transaction, one journal entry, and one revision check.
//
// It exists because "approve, then complete" is the single most common answer to
// a proposal for work that is already finished, and composing it from two writes
// would give a half-applied result its own reachable state: a second write that
// refuses would leave the proposal accepted-but-open, and `undo` would rewind
// only half of what the user asked for. Rejecting is NOT the same answer — it
// records that the work was declined, which is the wrong history for work that
// was done.
//
// The store invariant that a PROPOSED task can never be completed is untouched:
// the approval lands first, in the working copy, and the completion runs against
// the now-accepted task.
func (s *Store) ApproveAndCompleteProposal(id, expectedRevision, today string) MutationResult {
	return s.decideProposal(id, ProposalApprove, nil, expectedRevision, today, true)
}

func (s *Store) decideProposal(id, action string, notes []string,
	expectedRevision, today string, complete bool) MutationResult {

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

		preflight := check.CheckWith(s.org, s.checkOptions())
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
		// The decline marker lands in the SAME write as the state change, which is
		// the whole reason it exists: CANCELLED alone cannot say whether a task was
		// abandoned mid-flight or declined at review, and `list --rejected` /
		// `unreject` both need that distinction to be a fact in the file rather
		// than a guess from history.
		if action == ProposalReject {
			working[index].SetDefault("rejected", record.RawString(context.today.ISO()))
		}
		if noteText != "" {
			working[index].SetOptional("body",
				record.RawString(appendBody(working[index].String("body"), noteText)))
		}
		touched := []string{id}
		label := action + " proposal: " + title
		summaryAction := action
		if complete {
			// The task is INBOX in the working copy now, so this is an ordinary
			// completion — the "approve before completing" invariant holds, and the
			// DONE cascade over any accepted descendants comes with it.
			finished := patchState(working, index, TextValue("DONE"), context)
			if finished.status != MutationOK {
				result = MutationResult{Status: finished.status, Errors: finished.errors,
					Summary: finished.summary}
				return nil
			}
			// A recurring task does not COMPLETE: patchState rolls its anchor
			// forward and leaves it open. Approve+complete has no coherent
			// meaning there — the caller asked for one finished task and would
			// get an open INBOX occurrence — so it is refused rather than
			// reported under a name that would be a lie. Nothing is committed,
			// so the proposal is untouched.
			//
			// `check` now forbids a recurring PROPOSED task outright, so a file
			// carrying one is refused by this transaction's own preflight before
			// reaching here. This guard is what keeps that a REFUSAL rather than
			// a silent lie if the preflight is ever narrowed: the alternative is
			// committing a rolled-forward, still-open task and reporting DONE.
			if finished.summary.Action == "recurrence_advanced" {
				result = MutationResult{Status: MutationInvalid,
					Errors: []string{"remove recurrence before approving as done"}}
				return nil
			}
			touched = finished.touchedIDs
			target = "DONE"
			label = "approve + complete proposal: " + title
			summaryAction = ProposalApproveComplete
		}
		if _, err := record.Dump(working); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, working, label, "")
		if result.Status == MutationOK {
			result.TouchedIDs = touched
			result.Summary = MutationSummary{Action: summaryAction, From: "PROPOSED", To: target}
		}
		return nil
	})
	if err != nil {
		return mutationUnavailable(err)
	}
	return result
}

// UnrejectProposal returns one declined proposal to PROPOSED, in place.
//
// This is the inverse of a reject, not a re-capture: the row keeps its id, its
// title, its body — including the withdrawal note the reject appended, which is
// history and not a mistake — and its links. Nothing is created, so nothing can
// acquire a second id for work that already has one.
//
// It targets only a CANCELLED task carrying the `rejected` marker. An ordinary
// cancellation was never a proposal decision, and putting it back into the
// approval queue would be inventing review intent that never existed.
func (s *Store) UnrejectProposal(id, expectedRevision, today string) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		preflight := check.CheckWith(s.org, s.checkOptions())
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
			if status := changesetRevisionError(current, expectedRevision,
				[]Change{{Field: FieldState}}); status != "" {
				result = MutationResult{Status: status}
				return nil
			}
		}

		from := records[index].String("state")
		if from != "CANCELLED" || records[index].String("rejected") == "" {
			result = MutationResult{
				Status:  MutationInvalid,
				Errors:  []string{"task is " + from + ", not a rejected proposal"},
				Summary: MutationSummary{Action: ProposalUnreject, From: from},
			}
			return nil
		}

		context, err := s.patchContext(PatchRequest{Today: today})
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		title := records[index].String("title")
		working := record.CloneAll(records)
		// patchState carries the rest: it clears `closed`, drops the decline
		// marker on the way out of CANCELLED, and enforces the PROPOSED rules —
		// no recurrence, no delegation, no accepted descendants — so a restore
		// cannot produce a file `check` would reject.
		applied := patchState(working, index, TextValue("PROPOSED"), context)
		if applied.status != MutationOK {
			result = MutationResult{Status: applied.status, Errors: applied.errors,
				Summary: applied.summary}
			return nil
		}
		if _, err := record.Dump(working); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, working, "unreject proposal: "+title, "")
		if result.Status == MutationOK {
			result.TouchedIDs = []string{id}
			result.Summary = MutationSummary{Action: ProposalUnreject, From: "CANCELLED", To: "PROPOSED"}
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
