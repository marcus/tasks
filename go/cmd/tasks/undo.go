package main

import (
	"fmt"

	"tasks-go/internal/store"
)

// undo reverts the last mutation; redo replays it.
//
// History is the on-disk journal shared with the TUI and across CLI runs, so
// this reaches back past the current invocation — a `done` from one process is
// undoable from another, and from a cold start.
//
// The three refusals are the safety story, and their ORDER is contract. An
// unsupported schema is refused before the journal is consulted; an exhausted,
// missing, foreign or corrupt history is "nothing to undo"; and a store edited
// out of band since the journal's tip is a conflict that NAMES the label it
// declined to revert, so the operator knows exactly what was not undone. A
// conflict is also what an application that could not complete reports: an undo
// that half-applied would be worse than one that refused, so the files are put
// back and the same sentence is printed.
func (s *surfaceContext) undo(args []string) int { return s.history(args, -1, "undo", "undid") }

func (s *surfaceContext) redo(args []string) int { return s.history(args, 1, "redo", "redid") }

func (s *surfaceContext) history(args []string, delta int, verb, past string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort(fmt.Sprintf("usage: tasks %s [--json]", verb))
	}
	asJSON := flags["--json"]
	if message := s.store.UnsupportedSchemaError(); message != "" {
		return s.historyFailed(asJSON, "unsupported_schema_version", verb, "",
			unsupportedSchemaMessage(message))
	}

	outcome, label := s.writeStore().HistoryStep(delta)
	switch outcome {
	case store.HistoryUnsupportedSchema:
		// Belt and braces: the guard above already refused, but the store
		// re-checks under its own lock, so a store whose version changed in
		// between lands here.
		return s.historyFailed(asJSON, "unsupported_schema_version", verb, "",
			unsupportedSchemaMessage(s.store.UnsupportedSchemaError()))
	case store.HistoryEmpty:
		return s.historyFailed(asJSON, "empty", verb, "", "nothing to "+verb)
	case store.HistoryConflict:
		return s.historyFailed(asJSON, "conflict", verb, label,
			fmt.Sprintf("tasks.jsonl changed since that edit — refusing to %s “%s”", verb, label))
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("action", verb)
		w.KeyStr("label", label)
		w.EndObject()
		out(w.String())
		return 0
	}
	out(past + ": " + label)
	return 0
}

// historyFailed prints the refusal in both dialects. The label rides on the
// conflict document only, because it is the only refusal that has one.
func (s *surfaceContext) historyFailed(asJSON bool, code, verb, label, message string) int {
	if asJSON {
		// The envelope's own key order: any extra payload FIRST, then the three
		// discriminators, so a payload key can never shadow one of them.
		w := jsonWriter()
		w.BeginObject()
		if label != "" {
			w.KeyStr("label", label)
		}
		w.KeyStr("error", code)
		w.KeyStr("action", verb)
		w.KeyStr("message", message)
		w.EndObject()
		out(w.String())
	}
	return abort(message)
}

// unsupportedSchemaMessage is one sentence for one condition, on every command.
// It leads with Check's own wording — the version found and the version
// expected — because that is the only part an operator can act on. The suffix
// is the CLI's promise: this build declined, and the file is as it was.
func unsupportedSchemaMessage(detail string) string {
	if detail == "" {
		detail = "unsupported schema version"
	}
	return detail + " — this build cannot read this task file (nothing was written)"
}

func init() {
	register("undo", (*surfaceContext).undo)
	register("redo", (*surfaceContext).redo)
}
