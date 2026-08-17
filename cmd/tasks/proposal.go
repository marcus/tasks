package main

import (
	"strings"

	"github.com/marcus/tasks/internal/store"
)

// approve accepts a proposal into INBOX; reject declines it into CANCELLED.
//
// Both are thin adapters over one store transaction. Everything that makes a
// decision safe — the PROPOSED-only target, the leaves-first descendant gate,
// the note that lands in the SAME write as the state change — belongs to the
// store, so the two verbs cannot drift apart from each other or from the API.
func (s *surfaceContext) approve(args []string) int {
	return s.decideProposal(args, store.ProposalApprove)
}

func (s *surfaceContext) reject(args []string) int {
	return s.decideProposal(args, store.ProposalReject)
}

// unreject returns a declined proposal to PROPOSED, in place.
//
// `approve` is the wrong verb after a reject — it would accept work nobody has
// re-reviewed — and `undo` only rewinds the LAST write, so a reject from
// yesterday needs a verb of its own. The row keeps its id, title, notes and
// links: this restores a decision, it does not recapture the task.
func (s *surfaceContext) unreject(args []string) int {
	flags, positional, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return abort("usage: tasks unreject <ref> [--json]")
	}
	queries, status := s.readQueries(args, store.ProposalUnreject)
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, positional[0], refScope{includeDone: true})
	if code != 0 {
		return code
	}
	// An archived reject is cold storage: restoring it would mean moving records
	// between files, which this verb deliberately does not do. Say so, rather
	// than reporting the id as missing.
	if item.Source == store.SourceArchive {
		return abort("that rejected proposal is archived; restore is only available while it is live")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}

	result := s.writeStore().UnrejectProposal(item.ID, "", today)
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return mutationFailed(result, result.FirstError())
	}
	if status := mutationResultFailed(result, args, store.ProposalUnreject,
		"failed to restore rejected proposal"); status != 0 {
		return status
	}
	if flags["--json"] {
		return s.reportTouched(result, result.TouchedIDs, true)
	}
	out("restored → PROPOSED: " + item.Title)
	return 0
}

func (s *surfaceContext) decideProposal(args []string, action string) int {
	notes := []string{}
	rest := args
	if action == store.ProposalReject {
		var ok bool
		notes, rest, ok = extractRepeatableFlag(args, "--note")
		if !ok {
			return abort("missing value for --note")
		}
	}
	flags, positional, err := takeFlags(rest, "--json")
	if err != nil {
		return abort(err.Error())
	}
	usage := "usage: tasks " + action + " <ref> [--json]"
	if action == store.ProposalReject {
		usage = `usage: tasks reject <ref> [--note "reason"] [--json]`
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return abort(usage)
	}

	queries, status := s.readQueries(args, action)
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, positional[0], refScope{includeDone: true})
	if code != 0 {
		return code
	}
	today, status := s.today()
	if status != 0 {
		return status
	}

	result := s.writeStore().DecideProposal(item.ID, action, notes, "", today)
	if result.Status == store.MutationConflict && len(result.Summary.ProposedDescendantIDs) > 0 {
		return mutationFailed(result, "decide proposed descendants first")
	}
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return mutationFailed(result, result.FirstError())
	}
	if status := mutationResultFailed(result, args, action,
		"failed to "+action+" proposal"); status != 0 {
		return status
	}

	if flags["--json"] {
		return s.reportTouched(result, result.TouchedIDs, true)
	}
	target, verb := "INBOX", "approved"
	if action == store.ProposalReject {
		target, verb = "CANCELLED", "rejected"
	}
	out(verb + " → " + target + ": " + item.Title)
	return 0
}

// mutationFailed is Ruby's mutation_failed: the command's own wording, plus the
// rollback hint when — and only when — bytes were written and restored. The two
// leave identical files behind, so the recorded rollback is the only thing that
// can tell them apart.
func mutationFailed(result store.MutationResult, message string) int {
	if result.RolledBack {
		return abort(message + "\n" + rollbackHint(result.RollbackStage))
	}
	return abort(message)
}

// extractRepeatableFlag pulls every `--name <value>` pair out of the argument
// list, returning the values in order and the remaining arguments. It reports
// ok=false for a trailing flag with no value, which is a usage error rather than
// a silently dropped note.
func extractRepeatableFlag(args []string, name string) (values, rest []string, ok bool) {
	values, rest = []string{}, []string{}
	for index := 0; index < len(args); index++ {
		if args[index] != name {
			rest = append(rest, args[index])
			continue
		}
		index++
		if index >= len(args) {
			return nil, nil, false
		}
		values = append(values, args[index])
	}
	return values, rest, true
}

func init() {
	register("approve", (*surfaceContext).approve)
	register("reject", (*surfaceContext).reject)
	register("unreject", (*surfaceContext).unreject)
}
