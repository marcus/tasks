package main

import (
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

const noteUsage = `usage: tasks note <ref> "text"`

// note appends a line to a task's body.
//
// The new body is composed from the store's OWN baseline for the field rather
// than from the read the ref resolved through. The two are the same string when
// nothing changed underneath, and when something did the patch refuses instead
// of rewriting a body it never saw — which is exactly what an append must do.
func (s *surfaceContext) note(args []string) int {
	if refusal := s.refuseUnsupportedSchema(args, "note"); refusal != 0 {
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
	text := ""
	if len(rest) > 1 {
		text = joinPositional(rest[1:])
	}
	if strings.TrimSpace(ref) == "" || text == "" {
		return abort(noteUsage)
	}

	queries, status := s.readQueries(args, "note")
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
		out("would add note to " + taskquery.Headline(item) + ": " + text)
		return 0
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
	// The baseline for `body` IS the body, so one read answers both questions:
	// what to append to, and what the write must find unchanged.
	body, found := writer.ExpectedFor(item.ID, store.FieldBody)
	if !found {
		return abort("cannot add note: task is missing or the file is invalid — run `tasks check`")
	}
	appended := text
	if body != "" {
		appended = body + "\n" + text
	}
	return s.finishPatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: store.FieldBody, Value: store.TextValue(appended),
		Expected: body, Label: "note: " + item.Title, Today: today, Context: context,
	}), args, item, "note", "failed to add note", flags["--json"])
}

func init() {
	register("note", (*surfaceContext).note)
}
