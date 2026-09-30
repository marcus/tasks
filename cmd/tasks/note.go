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
	// The append itself is the store's `body_append`, the same delta
	// PATCH's `append_body` sends. Its baseline is the body it appends to, so
	// this command keeps its narrow check: a body that changed between this
	// read and the write refuses, exactly as it did when the command composed
	// the new body itself.
	body, found := writer.ExpectedFor(item.ID, store.FieldBodyAppend)
	if !found {
		if message := writer.LastLockError(); message != "" {
			return abort(message)
		}
		return abort("cannot add note: task is missing or the file is invalid — run `tasks check`")
	}
	return s.finishPatch(writer.Patch(store.PatchRequest{
		ID: item.ID, Field: store.FieldBodyAppend, Value: store.TextValue(text),
		Expected: body, Label: "note: " + item.Title, Today: today, Context: context,
	}), args, item, "note", "failed to add note", flags["--json"])
}

func init() {
	register("note", (*surfaceContext).note)
}
