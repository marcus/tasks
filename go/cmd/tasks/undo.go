package main

import (
	"fmt"

	"tasks-go/internal/store"
)

// undo reverts the last mutation — or, in this build, refuses to.
//
// The three refusals ARE the command as far as the port goes: they are what the
// conformance corpus asserts, and they are the whole of the safety story. An
// unsupported schema is refused before the journal is consulted; an exhausted,
// missing, foreign or corrupt history is "nothing to undo"; and a store edited
// out of band since the journal's tip is a conflict that names the label it
// declined to revert, so the operator knows exactly what was NOT undone.
func (s *surfaceContext) undo(args []string) int { return s.history(args, -1, "undo", "undone") }

func (s *surfaceContext) history(args []string, delta int, verb, past string) int {
	flags, rest, _ := takeFlags(args, "--json")
	if len(rest) > 0 {
		return abort(fmt.Sprintf("usage: tasks %s [--json]", verb))
	}
	if message := s.store.UnsupportedSchemaError(); message != "" {
		return abort(unsupportedSchemaMessage(message))
	}
	outcome, label := s.store.PlanHistoryStep(delta, env)
	switch outcome {
	case store.HistoryUnsupportedSchema:
		// Belt and braces: the guard above already refused, but the store
		// re-checks under its own lock, so a store whose version changed in
		// between lands here.
		return abort(unsupportedSchemaMessage(s.store.UnsupportedSchemaError()))
	case store.HistoryEmpty:
		return abort("nothing to " + verb)
	case store.HistoryConflict:
		return abort(fmt.Sprintf("tasks.jsonl changed since that edit — refusing to %s “%s”", verb, label))
	}
	// The step is applicable, and applying it would REWRITE both task files from
	// journal blobs. That is a write, and this build has none.
	_ = flags
	_ = past
	return abort(fmt.Sprintf("%s: not implemented in the Go port — applying %q would rewrite the task "+
		"file, and this build has no write path", verb, label))
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
	register("undo", nil, (*surfaceContext).undo)
}
