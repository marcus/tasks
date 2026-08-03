package main

import (
	"strings"

	"tasks-go/internal/recur"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
)

// priority sets or clears a task's priority cookie.
func (s *surfaceContext) priority(args []string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks priority <ref> <A|B|C|none>")
	}
	ref := rest[0]
	requested := joinPositional(rest[1:])
	if requested == "" {
		return abort("usage: tasks priority <ref> <A|B|C|none>")
	}
	value := strings.ToUpper(requested)
	if value == "NONE" || value == "CLEAR" || value == "-" {
		value = ""
	}
	if value != "" && value != "A" && value != "B" && value != "C" {
		return abort("priority must be A, B, C, or none")
	}

	queries, status := s.readQueries(args, "priority")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{
		includeDone: flags["--include-done"], includeProposed: true,
	})
	if code != 0 {
		return code
	}
	if flags["--dry-run"] {
		shown := "(none)"
		if value != "" {
			shown = "[#" + value + "]"
		}
		out("would set priority " + shown + " on: " + taskquery.Headline(item))
		return 0
	}

	label := "clear priority: " + item.Title
	if value != "" {
		label = "priority [#" + value + "]: " + item.Title
	}
	return s.patchAndReport(args, item, store.FieldPriority, value, label,
		"priority", "failed to set priority", flags["--json"])
}

// done marks a matching open task DONE.
//
// Ref resolution runs FIRST and refuses with exit 2 before any write is
// contemplated, because "your ref was wrong, refine it" is an answer an agent
// can act on and "the command failed" is not.
func (s *surfaceContext) done(args []string) int {
	return s.changeState(args, stateOptions{
		target: "DONE", usage: `tasks done "<ref>"`, action: "done",
		dryVerb: "mark DONE", archiveHint: true, recurAware: true,
	})
}

// stateOptions is what distinguishes the three state verbs. `done` and `cancel`
// are sugar for one transition each and resolve OPEN tasks only; `state` takes
// its target as an argument and resolves closed ones too, because reopening a
// DONE task is the whole reason it exists.
type stateOptions struct {
	// target is the state to move to, or "" when the positional argument names
	// it (the `state` verb).
	target string
	usage  string
	// action is the canonical command name a --json refusal reports back.
	action string
	// resolveClosed widens ref resolution unconditionally.
	resolveClosed bool
	// dryVerb is the preview's verb phrase; "" takes "set state <TARGET> on".
	dryVerb string
	// archiveHint prints the follow-up `tasks archive` line.
	archiveHint bool
	// recurAware makes DONE on a repeating task roll instead of close.
	recurAware bool
	// notes is `cancel --note`: withdrawal rationale appended in the SAME write
	// as the transition, so an undo takes back both or neither.
	notes []string
}

func (s *surfaceContext) changeState(args []string, options stateOptions) int {
	if refusal := s.refuseUnsupportedSchema(args, options.action); refusal != 0 {
		return refusal
	}
	flags, rest, err := takeFlags(args, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	ref := ""
	if len(rest) > 0 {
		ref = rest[0]
	}
	target := options.target
	if target == "" && len(rest) > 1 {
		target = strings.ToUpper(joinPositional(rest[1:]))
	}
	if strings.TrimSpace(ref) == "" || target == "" {
		return abort("usage: " + options.usage)
	}
	if !contains(allStates, target) {
		return abort("unknown state: " + target + " (want one of " + strings.Join(allStates, ", ") + ")")
	}

	queries, status := s.readQueries(args, options.action)
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{
		includeDone: options.resolveClosed || flags["--include-done"],
	})
	if code != 0 {
		return code
	}

	// Completing a RECURRING task rolls it to its next occurrence rather than
	// closing it, so the two reports are mutually exclusive: the roll announces
	// the new date, and the archive hint is suppressed. Telling the user to
	// archive a task that is still open and now scheduled would be wrong.
	recurringDone := options.recurAware && target == "DONE" && recur.Cookie(item.Recur)
	noteText := strings.Join(options.notes, "\n")

	if flags["--dry-run"] {
		return s.previewStateChange(queries, item, target, options, recurringDone, noteText)
	}

	label := "state → " + target + ": " + item.Title
	roll := func(result store.MutationResult) {
		if recurringDone && !flags["--json"] {
			out(recurrenceRollLine(item, result))
		}
	}
	if noteText != "" {
		status = s.changeStateWithNote(args, item, target, noteText, label, options, flags["--json"])
	} else {
		status = s.patchAndReport(args, item, store.FieldState, target, label,
			options.action, "failed to set state", flags["--json"], roll)
	}
	if status == 0 && options.archiveHint && !flags["--json"] && !recurringDone {
		out("Run `tasks archive` to move it out of tasks.jsonl.")
	}
	return status
}

// changeStateWithNote is `cancel --note`: the transition and the rationale in
// ONE checked transaction. Two patches would cost two undo steps and could
// leave a cancelled task with no reason attached if the second one failed.
func (s *surfaceContext) changeStateWithNote(args []string, item store.Item, target, noteText,
	label string, options stateOptions, asJSON bool) int {

	if item.ID == "" {
		return abort("task has no stable id")
	}
	// No readable editor snapshot means there is no body to append to and no
	// revision to guard the write with, so the command stops here rather than
	// submitting a changeset it cannot state a precondition for.
	body, found := s.writeStore().ExpectedFor(item.ID, store.FieldBody)
	if !found {
		return abort("failed to set state")
	}
	appended := noteText
	if body != "" {
		appended = body + "\n" + noteText
	}
	return s.changesetAndReport(args, item, []store.Change{
		{Field: store.FieldState, Value: store.TextValue(target)},
		{Field: store.FieldBody, Value: store.TextValue(appended)},
	}, label, options.action, "failed to set state", asJSON, nil)
}

// previewStateChange is --dry-run's whole output. It names the CASCADE as well
// as the transition: completing an open parent closes its open descendants, and
// a `done` on a project that silently took four subtasks with it is the one
// surprise this preview exists to prevent.
func (s *surfaceContext) previewStateChange(queries *taskquery.Queries, item store.Item,
	target string, options stateOptions, recurringDone bool, noteText string) int {

	if recurringDone {
		next, ok := s.recurNext(queries, item)
		if !ok {
			return abort("recurrence could not find a valid local date/time")
		}
		out("would recur → " + next.ISO() + ": " + taskquery.Headline(item))
		return 0
	}
	verb := options.dryVerb
	if verb == "" {
		verb = "set state " + target + " on"
	}
	out("would " + verb + ": " + taskquery.Headline(item))
	if target == "DONE" && contains(taskquery.OpenStates(), item.State) {
		if count := openDescendantCount(queries, item); count > 0 {
			out("would also close " + pluralize(count, "open descendant"))
		}
	}
	if noteText != "" {
		out("would add note: " + noteText)
	}
	return 0
}

// openDescendantCount is how many open tasks sit below an item — the cascade a
// `done` would close. Zero for an item with no live node (an archive item), so
// the caller stays silent rather than guessing.
func openDescendantCount(queries *taskquery.Queries, item store.Item) int {
	node := queries.NodeFor(item)
	if node == nil {
		return 0
	}
	count := 0
	var walk func(*taskquery.Node)
	walk = func(current *taskquery.Node) {
		for _, child := range current.Children {
			if child.Item != nil && contains(taskquery.OpenStates(), child.Item.State) {
				count++
			}
			walk(child)
		}
	}
	walk(node)
	return count
}

// recurNext is the date a recurring task's completion would roll it to — the
// preview half of the roll `done` performs.
//
// The paired-date veto is what keeps the two stamps' offset intact: when a task
// carries BOTH a deadline and an available-from date, a candidate whose paired
// value names no real local time is skipped rather than written.
func (s *surfaceContext) recurNext(queries *taskquery.Queries, item store.Item) (temporal.Date, bool) {
	deadline, hasDeadline := queries.DeadlineValue(item)
	scheduled, hasScheduled := queries.ScheduledValue(item)
	value, kind := scheduled, recur.Scheduled
	if hasDeadline {
		value, kind = deadline, recur.Deadline
	} else if !hasScheduled {
		return temporal.Date{}, false
	}
	context := queries.Context()
	var veto func(temporal.Date) bool
	if hasDeadline && hasScheduled {
		delta := scheduled.Date.Sub(deadline.Date)
		veto = func(candidate temporal.Date) bool {
			paired, err := scheduled.WithDate(candidate.AddDays(delta))
			if err != nil {
				return false
			}
			_, err = paired.Instant(context)
			return err == nil
		}
	}
	next, err := recur.NextTemporalDate(item.Recur, value, kind, context, veto)
	if err != nil {
		return temporal.Date{}, false
	}
	return next, true
}

// recurrenceRollLine is bin/tasks' "↻ title → next 2026-08-10 (Mon)", read from
// the snapshot the write returned. The date half is omitted when the rolled
// task carries neither stamp, which is Ruby's `d ? … : ""`.
func recurrenceRollLine(item store.Item, result store.MutationResult) string {
	line := "↻ " + item.Title
	if result.ReadSnapshot == nil {
		return line
	}
	for _, fresh := range result.ReadSnapshot.Items {
		if fresh.ID != item.ID {
			continue
		}
		// DEADLINE over SCHEDULED, matching the store's own precedence.
		stamp := fresh.Deadline
		if stamp == "" {
			stamp = fresh.Scheduled
		}
		if date, ok := temporal.ParseDate(stamp); ok {
			line += " → next " + date.ISO() + " (" + date.Weekday().String()[:3] + ")"
		}
		break
	}
	return line
}

// patchAndReport is the shared tail of every field patch: read the baseline,
// apply under the lock against it, and report.
//
// Reading the baseline in a SEPARATE lock acquisition is Ruby's shape, not an
// oversight. The expectation is what makes the write refuse when another writer
// changed the field in between, so it has to be captured before the write takes
// its own lock rather than derived inside it.
//
// beforeReport, when set, runs after a successful write and BEFORE the touched
// report, which is where bin/tasks prints the recurrence roll.
func (s *surfaceContext) patchAndReport(args []string, item store.Item, field store.PatchField,
	value, label, action, summary string, asJSON bool, beforeReport ...func(store.MutationResult)) int {
	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	// An absent baseline is not itself the answer. The task disappearing and the
	// FILE ceasing to validate both produce one, and they need different
	// sentences — "your edit went stale" against "run `tasks check`". Only the
	// store can tell them apart, so the patch is submitted with whatever
	// precondition there is and the store's own status is what gets reported.
	result := settlePatch(writer.PatchTask(item.ID, field, value,
		patchBaseline(writer, item.ID, field), label, today))
	if !result.OK() {
		return mutationResultFailed(result, args, action, summary)
	}
	for _, hook := range beforeReport {
		hook(result)
	}
	touched := result.TouchedIDs
	if len(touched) == 0 {
		touched = []string{item.ID}
	}
	return s.reportTouched(result, touched, asJSON)
}

func init() {
	register("priority", (*surfaceContext).priority)
	register("done", (*surfaceContext).done)
}
