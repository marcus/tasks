package main

import (
	"strings"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

const undateUsage = "usage: tasks undate <ref> [--kind deadline|scheduled]"

// undate removes a task's date stamps.
//
// Both dates and the recurrence they anchor move in ONE write. `undate` retires
// a cookie whose last anchor it just removed, so the baseline it writes against
// covers all three — a concurrent `recur` must refuse rather than be left
// pointing at a date that no longer exists.
func (s *surfaceContext) undate(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "undate"); refusal != 0 {
		return refusal
	}
	remaining := args
	kind := ""
	// --kind is read positionally rather than through takeFlagValue because
	// bin/tasks reads it that way: `--kind --json` names an invalid kind and is
	// refused by the vocabulary check below, not by a missing-value abort.
	for index, arg := range args {
		if arg != "--kind" {
			continue
		}
		if index+1 >= len(args) {
			return abort(undateUsage)
		}
		kind = strings.ToLower(args[index+1])
		remaining = append(append([]string{}, args[:index]...), args[index+2:]...)
		break
	}
	if kind != "" && kind != "deadline" && kind != "scheduled" {
		return abort("--kind must be deadline or scheduled")
	}

	flags, rest, err := takeFlags(remaining, "--dry-run", "--json", "--include-done")
	if err != nil {
		return abort(err.Error())
	}
	ref := ""
	if len(rest) > 0 {
		ref = rest[0]
	}
	if strings.TrimSpace(ref) == "" {
		return abort(undateUsage)
	}

	queries, status := s.readQueries(args, "undate")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, ref, refScope{includeDone: flags["--include-done"]})
	if code != 0 {
		return code
	}
	if flags["--dry-run"] {
		target := "SCHEDULED/DEADLINE"
		if kind != "" {
			target = strings.ToUpper(kind)
		}
		out("would remove " + target + " from: " + taskquery.Headline(item))
		return 0
	}

	label := "remove dates: " + item.Title
	if kind != "" {
		label = "remove " + kind + ": " + item.Title
	}
	// Refusing BEFORE the write is what makes "nothing to remove" a distinct
	// answer from a stale edit: both leave the file untouched, and only the
	// wording tells the user which happened.
	present := item.Deadline != "" || item.Scheduled != ""
	if kind == "deadline" {
		present = item.Deadline != ""
	} else if kind == "scheduled" {
		present = item.Scheduled != ""
	}
	if !present {
		return abort("nothing to remove (no matching date stamp?)")
	}
	if item.ID == "" {
		return abort("task has no stable id")
	}
	today, status := s.today()
	if status != 0 {
		return status
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	writer := s.writeStore()
	result := settlePatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: store.FieldDateClear, Value: store.TextValue(kind),
		Expected: patchBaseline(writer, item.ID, store.FieldDateClear),
		Label:    label, Today: today, Context: context,
	}))
	// The two failures leave identical bytes behind, so only the sentence tells
	// them apart: the task moved under the edit, or it never carried the stamp
	// this command was asked to remove.
	summary := "nothing to remove (no matching date stamp?)"
	if result.Status == store.MutationStale {
		summary = "task changed or vanished while editing"
	}
	return s.finishPatch(result, args, item, "undate", summary, flags["--json"])
}

func init() {
	register("undate", (*surfaceContext).undate)
}
