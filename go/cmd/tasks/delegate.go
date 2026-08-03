package main

import (
	"fmt"
	"os"
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

const delegateUsage = "usage: tasks delegate <ref> <refine|research|implement>  |  " +
	"tasks delegate <ref> --to <email> [--keep-state]"

const workerHint = "pass --worker <id> or set TASKS_WORKER_ID"

// delegate hands a task to the agent pool or to a named person.
//
// Refs resolve across proposed and closed tasks on purpose, so an ineligible
// target reports the STATE that refuses it rather than a bare no-match: "task
// is DONE" is actionable, "no match" for a task the user can see is not.
func (s *surfaceContext) delegate(args []string) int {
	to, rest, _ := extractValue(args, "--to")
	flags, rest, err := takeFlags(rest, "--json", "--keep-state")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort(delegateUsage)
	}
	ref := rest[0]
	mode := joinPositional(rest[1:])
	if to == "" && mode == "" {
		return abort(delegateUsage)
	}
	if to != "" && mode != "" {
		return abort("delegate takes a mode or --to <email>, not both\n" + delegateUsage)
	}
	if flags["--keep-state"] {
		return notPorted("delegate --keep-state")
	}

	queries, status := s.readQueries(args, "delegate")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: true})
	if code != 0 {
		return code
	}

	kind, assignee := "agent", ""
	if to != "" {
		kind, mode, assignee = "human", "", to
	}
	if kind == "human" {
		// Handing a task to a person also moves it to WAITING, and that second
		// write is a separate patch this build has not ported. Refusing keeps
		// the composed operation all-or-nothing.
		return notPorted("delegate --to")
	}

	result := s.writeStore().Delegate(item.ID, kind, mode, assignee, s.delegationCoalesceKey("delegate"))
	if status := s.delegationFailed(result, args, "delegate"); status != 0 {
		return status
	}
	if flags["--json"] {
		return s.reportTouched(result, []string{item.ID}, true)
	}
	return s.printDelegationHeadline(result, item.ID)
}

// claim takes an agent-pool task for one worker.
func (s *surfaceContext) claim(args []string) int {
	workerFlag, rest, _ := extractValue(args, "--worker")
	flags, rest, err := takeFlags(rest, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks claim <ref> --worker <id> [--json]")
	}
	worker := strings.TrimSpace(workerFlag)
	if worker == "" {
		worker = strings.TrimSpace(env.Get("TASKS_WORKER_ID"))
	}
	if worker == "" {
		return abort("missing worker id — " + workerHint)
	}

	queries, status := s.readQueries(args, "claim")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, rest[0], refScope{includeDone: true})
	if code != 0 {
		return code
	}

	// The key is minted even though `claim` does not pass one to the store: the
	// mint is per-OPERATION and shared with the other delegation verbs, so
	// skipping the draw would shift every later key by one.
	s.delegationCoalesceKey("claim")
	result := s.writeStore().Claim(item.ID, worker, "")
	if status := s.delegationFailed(result, args, "claim"); status != 0 {
		return status
	}
	return s.claimResource(result, item.ID, flags["--json"])
}

// delegationFailed is the delegation-shaped refusal, and it differs from every
// other mutation refusal in exactly one way: a CONFLICT names who holds the
// work and since when.
//
// That is the whole content of the answer for an agent that lost a race. A
// message alone would make it re-parse prose to learn whether to back off, so
// the holder and the instant are their own fields.
func (s *surfaceContext) delegationFailed(result store.MutationResult, args []string, action string) int {
	if result.OK() {
		return 0
	}
	if result.Status == store.MutationConflict {
		message := "conflict: " + defaultText(result.FirstError(), "delegation conflict")
		if action == "claim" && result.Summary.Holder != "" {
			message = fmt.Sprintf("conflict: already claimed by %s at %s",
				result.Summary.Holder, result.Summary.At)
		}
		if jsonRequested(args) {
			w := jsonWriter()
			w.BeginObject()
			w.KeyStr("error", "conflict")
			w.KeyStr("action", result.Summary.Action)
			w.KeyStrOrNull("id", result.Summary.TaskID)
			w.KeyStrOrNull("holder", result.Summary.Holder)
			w.KeyStrOrNull("at", result.Summary.At)
			w.KeyStr("message", message)
			w.EndObject()
			out(w.String())
		}
		fmt.Fprintln(os.Stderr, message)
		return result.ExitCode()
	}
	// An `invalid` refusal already carries the sentence that explains it, and
	// it is printed bare — not wrapped in the generic summary, and without a
	// JSON envelope, which is the recorded Ruby behaviour.
	if result.Status == store.MutationInvalid && result.FirstError() != "" {
		return abort(result.FirstError())
	}
	return mutationResultFailed(result, args, action, "failed to update delegation")
}

// claimResource prints the WHOLE canonical resource, not just the marker: a
// worker claims the task and reads the authority it is working under in one
// step.
func (s *surfaceContext) claimResource(result store.MutationResult, id string, asJSON bool) int {
	if !asJSON {
		return s.printDelegationHeadline(result, id)
	}
	// The extra members `claim --json` adds over the standard task document —
	// closed, notes, project and links — are not ported yet, and emitting the
	// standard document under a name that promises more would be a silent
	// difference rather than a stated one.
	return notPorted("claim --json success payload")
}

func (s *surfaceContext) printDelegationHeadline(result store.MutationResult, id string) int {
	snapshot := result.ReadSnapshot
	if snapshot == nil {
		return abort("task store unavailable")
	}
	for _, item := range snapshot.Items {
		if item.ID != id {
			continue
		}
		out(taskquery.Headline(item))
		return 0
	}
	return 0
}

// delegationCoalesceKey mints one key per operation, so only this operation's
// own follow-up write can merge into its journal entry — never an unrelated
// neighbouring edit.
func (s *surfaceContext) delegationCoalesceKey(action string) string {
	return "delegation-" + action + "-" + s.mintDelegationKey()
}

func defaultText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func init() {
	register("delegate", (*surfaceContext).delegate)
	register("claim", (*surfaceContext).claim)
}
