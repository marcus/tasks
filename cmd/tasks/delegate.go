package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
)

// delegateUsage quotes the mode vocabulary the store will actually enforce, so
// a configured set reaches the usage line without a second literal here.
func delegateUsage(modes record.ModeVocabulary) string {
	return "usage: tasks delegate <ref> <" + strings.Join(record.Modes(modes).Modes(), "|") + ">  |  " +
		"tasks delegate <ref> --to <email> [--keep-state]"
}

const workerHint = "pass --worker <id> or set TASKS_WORKER_ID"

// delegate hands a task to the agent pool or to a named person.
//
// Refs resolve across proposed and closed tasks on purpose, so an ineligible
// target reports the STATE that refuses it rather than a bare no-match: "task
// is DONE" is actionable, "no match" for a task the user can see is not.
func (s *surfaceContext) delegate(args []string) int {
	usage := delegateUsage(s.writeStore().Modes())
	to, rest, _ := extractValue(args, "--to")
	flags, rest, err := takeFlags(rest, "--json", "--keep-state")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort(usage)
	}
	ref := rest[0]
	mode := joinPositional(rest[1:])
	if to == "" && mode == "" {
		return abort(usage)
	}
	if to != "" && mode != "" {
		return abort("delegate takes a mode or --to <email>, not both\n" + usage)
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

	// The marker this write will REPLACE, read before the write. Whether an
	// inherited WAITING is cleared depends on what the marker was at the moment
	// it changed, and the store does not report it.
	previous := delegationFields(item.Delegation)

	coalesceKey := s.delegationCoalesceKey("delegate")
	writer := s.writeStore()
	result := writer.Delegate(item.ID, kind, mode, assignee, coalesceKey)
	if status := s.delegationFailed(result, args, "delegate"); status != 0 {
		return status
	}

	// The state follow-up, and its exact inverse. Handing a task to a person
	// moves it to WAITING: the next action really is outside the owner's
	// control. Replacing that person with the agent pool undoes exactly that —
	// agent-ready work is actionable again. Only a WAITING INHERITED from the
	// human delegation is cleared; a WAITING the owner set for their own reasons
	// is theirs to keep. Both land in the SAME undo step as the delegation.
	stateChanged := false
	if !flags["--keep-state"] {
		stateChanged = s.delegateStateFollowUp(writer, item, kind, mode, assignee,
			previous, coalesceKey, &result)
	}

	if flags["--json"] {
		return s.reportTouchedSnapshot(s.delegationSnapshot(result), []string{item.ID}, true, nil)
	}
	return s.printDelegationHeadlineChanged(result, item.ID, stateChanged)
}

// delegationSnapshot is the read a delegation report renders from: the write's
// own snapshot when it wrote, and a fresh read when it deliberately did not.
//
// An idempotent repeat is the case that matters. It carries no snapshot because
// it changed nothing, and reporting "task store unavailable" for a call that
// succeeded would be the wrong answer to the right question.
func (s *surfaceContext) delegationSnapshot(result store.MutationResult) *store.Snapshot {
	if result.ReadSnapshot != nil {
		return result.ReadSnapshot
	}
	snapshot, err := s.store.ReadSnapshot(false)
	if err != nil {
		return nil
	}
	return snapshot
}

// delegateStateFollowUp performs the WAITING default, or its inverse, and
// reports whether it wrote. A failed follow-up never turns a successful
// delegation into a failure: the composed fact is simply false.
func (s *surfaceContext) delegateStateFollowUp(writer *store.Store, item store.Item,
	kind, mode, assignee string, previous map[string]string, coalesceKey string,
	result *store.MutationResult) bool {

	replacingHuman := previous != nil && previous["kind"] == "human"
	if kind != "human" && !replacingHuman {
		return false
	}
	current, found := s.delegationItem(*result, item.ID)
	if !found {
		return false
	}

	target, label := "", ""
	if kind == "human" {
		if current.State == "WAITING" {
			return false
		}
		target = "WAITING"
		label = "delegate → " + assignee + ": " + current.Title
	} else {
		if current.State != "WAITING" {
			return false
		}
		target = "TODO"
		label = "agent-ready (" + mode + "): " + current.Title
	}

	today, status := s.today()
	if status != 0 {
		return false
	}
	patched := writer.PatchTaskCoalesced(item.ID, store.FieldState, target,
		patchBaseline(writer, item.ID, store.FieldState), label, today, coalesceKey)
	if !patched.Changed() {
		return false
	}
	if patched.ReadSnapshot != nil {
		result.ReadSnapshot = patched.ReadSnapshot
	}
	return true
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
// step. That is why it carries the four members the standard row omits — the
// closed date, the notes, the project and the links.
func (s *surfaceContext) claimResource(result store.MutationResult, id string, asJSON bool) int {
	if !asJSON {
		return s.printDelegationHeadline(result, id)
	}
	queries, status := s.readQueries(nil, "claim")
	if status != 0 {
		return status
	}
	item, found := s.delegationItem(result, id)
	if !found {
		return abort("task store unavailable")
	}
	links := queries.Links(item)
	notes := taskNotes(queries, item)

	w := jsonWriter()
	writeItemJSONWith(w, queries, item, func(w *jsonout.Writer) {
		w.KeyStrOrNull("closed", item.Closed)
		w.Key("notes")
		w.Strings(notes)
		// `project` is deliberately not written again: the standard row already
		// carries it, and Ruby's merge overwrites in place rather than appending.
		w.Key("links")
		w.BeginArray()
		for _, link := range links {
			w.BeginObject()
			writeLinkMembers(w, link)
			w.EndObject()
		}
		w.EndArray()
	})
	if err := w.Err(); err != nil {
		return abort(err.Error())
	}
	out(w.String())
	return 0
}

// printDelegationHeadline prints the DELEGATION line, not the task headline:
// who holds the task and in what capacity, which is the question the verb was
// asked. `delegate`, `claim`, `release` and `list --delegated` all print through
// the same function so the four surfaces cannot drift apart.
func (s *surfaceContext) printDelegationHeadline(result store.MutationResult, id string) int {
	return s.printDelegationHeadlineChanged(result, id, false)
}

// printDelegationHeadlineChanged is the same line with the state appended when
// THIS write moved it. Replacing a person with the agent pool leaves WAITING
// alone, and printing a state change that did not happen would surprise.
func (s *surfaceContext) printDelegationHeadlineChanged(result store.MutationResult, id string,
	stateChanged bool) int {

	queries, status := s.readQueries(nil, "delegate")
	if status != 0 {
		return status
	}
	item, found := s.delegationItem(result, id)
	if !found {
		return abort("task store unavailable")
	}
	headline := delegationHeadline(queries, item)
	if stateChanged {
		marker := delegationFields(item.Delegation)
		if marker != nil && marker["status"] == "ready" {
			headline = "agent-ready (" + marker["mode"] + ") → " + item.State + ": " + item.Title
		}
	}
	out(headline)
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
