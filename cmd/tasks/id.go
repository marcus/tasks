package main

import (
	"strings"

	"github.com/marcus/tasks/internal/taskquery"
)

// id reports a task's stable id, minting one for a legacy record that has none.
//
// It resolves across closed tasks unconditionally: an id is a REFERENCE, and a
// task stays referenceable whatever its state. It is idempotent — a task that
// already has an id is reported without a write and without burning an undo
// slot.
func (s *surfaceContext) taskID(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return abort("usage: tasks id <ref>")
	}

	queries, status := s.readQueries(args, "id")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, rest[0], refScope{includeDone: true, includeProposed: true})
	if code != 0 {
		return code
	}

	assigned, ok := s.writeStore().EnsureID(item.Line, item.ID, item.Title)
	if !ok || assigned == "" {
		return abort("failed to assign id")
	}

	// The report is taken from a FRESH read rather than from the resolved item:
	// a mint rewrites the record, and the headline must describe what is on disk
	// now.
	fresh, status := s.readQueries(args, "id")
	if status != 0 {
		return status
	}
	found, present := fresh.FindLive(assigned)

	if flags["--json"] {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("id", assigned)
		w.Key("touched")
		w.BeginArray()
		if present {
			writeItemJSON(w, fresh, found)
		}
		w.EndArray()
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		return 0
	}
	out(assigned)
	if present {
		out(taskquery.Headline(found))
	}
	return 0
}

func init() {
	register("id", (*surfaceContext).taskID)
}
