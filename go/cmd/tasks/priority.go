package main

import (
	"strings"

	"tasks-go/internal/store"
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

	queries, status := s.readQueries()
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
		return notPorted("priority --dry-run")
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
	return s.changeState(args, "DONE", `tasks done "<ref>"`)
}

func (s *surfaceContext) changeState(args []string, target, usage string) int {
	flags, rest, err := takeFlags(args, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: " + usage)
	}

	queries, status := s.readQueries()
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, rest[0], refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	if flags["--dry-run"] {
		return notPorted("done --dry-run")
	}

	label := "state → " + target + ": " + item.Title
	status = s.patchAndReport(args, item, store.FieldState, target, label,
		"done", "failed to set state", flags["--json"])
	if status == 0 && !flags["--json"] {
		out("Run `tasks archive` to move it out of tasks.jsonl.")
	}
	return status
}

// patchAndReport is the shared tail of every field patch: read the baseline,
// apply under the lock against it, and report.
//
// Reading the baseline in a SEPARATE lock acquisition is Ruby's shape, not an
// oversight. The expectation is what makes the write refuse when another writer
// changed the field in between, so it has to be captured before the write takes
// its own lock rather than derived inside it.
func (s *surfaceContext) patchAndReport(args []string, item store.Item, field store.PatchField,
	value, label, action, summary string, asJSON bool) int {
	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	expected, found := writer.ExpectedFor(item.ID, field)
	if !found {
		// The task disappeared, or the file stopped validating, between ref
		// resolution and the write. That is a STALE mutation (exit 1), not a
		// fresh ref-resolution miss (exit 2).
		return mutationResultFailed(store.MutationResult{Status: store.MutationStale},
			args, action, summary)
	}
	result := writer.PatchTask(item.ID, field, value, expected, label, today)
	if !result.OK() {
		return mutationResultFailed(result, args, action, summary)
	}
	touched := result.TouchedIDs
	if len(touched) == 0 {
		touched = []string{item.ID}
	}
	return s.reportTouched(result, touched, asJSON)
}
