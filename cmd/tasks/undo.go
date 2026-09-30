package main

import (
	"fmt"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
)

// undo reverts the last mutation; redo replays it.
//
// History is the on-disk journal shared with the TUI and across CLI runs, so
// this reaches back past the current invocation — a `done` from one process is
// undoable from another, and from a cold start.
//
// The refusals are the safety story, and their ORDER is contract. An
// unsupported schema is refused before the journal is consulted; an exhausted,
// missing, foreign or corrupt history is "nothing to undo"; a store lock that
// cannot be taken is unavailable rather than empty; and a store edited
// out of band since the journal's tip is a conflict that NAMES the label it
// declined to revert, so the operator knows exactly what was not undone. A
// conflict is also what an application that could not complete reports: an undo
// that half-applied would be worse than one that refused, so the files are put
// back and the same sentence is printed.
//
// `--store-revision REV` pins the step to the store revision the caller last
// saw (`tasks history --json` reports it), exactly as the HTTP routes require:
// a store that moved since is refused as stale before the journal is
// consulted, so a script cannot undo a write it never saw.
func (s *surfaceContext) undo(args []string) int { return s.historyStep(args, -1, "undo", "undid") }

func (s *surfaceContext) redo(args []string) int { return s.historyStep(args, 1, "redo", "redid") }

func (s *surfaceContext) historyStep(args []string, delta int, verb, past string) int {
	expected, args, pinned, status := takeFlagValue(args, "--store-revision")
	if status != 0 {
		return status
	}
	// An empty pin is a script whose variable came back blank, not a request
	// to act unguarded — which is what "" means to the store.
	if pinned && expected == "" {
		return abort("--store-revision needs a revision (see `tasks history --json`)")
	}
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort(fmt.Sprintf("usage: tasks %s [--store-revision REV] [--json]", verb))
	}
	asJSON := flags["--json"]
	if message := s.store.UnsupportedSchemaError(); message != "" {
		return s.historyFailed(asJSON, "unsupported_schema_version", verb, "",
			unsupportedSchemaMessage(message))
	}

	result := s.writeStore().GuardedHistoryStep(delta, expected)
	outcome, label := result.Outcome, result.Label
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
		return s.historyRefused(asJSON, "journal_conflict", verb, func(w *jsonout.Writer) {
			w.KeyStr("label", label)
		}, fmt.Sprintf("tasks.jsonl changed since that edit — refusing to %s “%s”", verb, label))
	case store.HistoryStale:
		return s.historyRefused(asJSON, string(store.HistoryStale), verb, func(w *jsonout.Writer) {
			w.KeyStr("store_revision", result.StoreRevision)
		}, fmt.Sprintf("the task store changed since revision %s — refusing to %s; re-read `tasks history` and retry",
			expected, verb))
	case store.HistoryUnavailable:
		message := label
		if message == "" {
			message = "task store unavailable"
		}
		return s.historyFailed(asJSON, "unavailable", verb, "", message)
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("action", verb)
		w.KeyStr("label", label)
		w.KeyStr("store_revision", result.StoreRevision)
		w.EndObject()
		out(w.String())
		return 0
	}
	out(past + ": " + label)
	return 0
}

// historyRefused is a `conflict` refusal with a `reason`, in the archive
// sweep's envelope shape: payload first, then `reason`, then the three
// discriminators. The reasons are the HTTP routes' `details.reason` words.
func (s *surfaceContext) historyRefused(asJSON bool, reason, verb string, extra func(*jsonout.Writer),
	message string) int {
	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		extra(w)
		w.KeyStr("reason", reason)
		w.KeyStr("error", "conflict")
		w.KeyStr("action", verb)
		w.KeyStr("message", message)
		w.EndObject()
		out(w.String())
	}
	return abort(message)
}

// history is `tasks history`: the next undo and redo labels and the store
// revision they apply to, without applying either — GET /api/v1/history, as a
// command. A caller that wants to act on exactly what it read passes that
// revision back as `undo --store-revision`.
func (s *surfaceContext) history(args []string) int {
	flags, rest, err := takeFlags(args, "--json")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) > 0 {
		return abort("usage: tasks history [--json]")
	}
	asJSON := flags["--json"]
	if message := s.store.UnsupportedSchemaError(); message != "" {
		return s.historyFailed(asJSON, "unsupported_schema_version", "history", "",
			unsupportedSchemaMessage(message))
	}
	peek := s.writeStore().PeekHistory()
	if peek.UnsupportedSchema {
		return s.historyFailed(asJSON, "unsupported_schema_version", "history", "",
			unsupportedSchemaMessage(s.store.UnsupportedSchemaError()))
	}
	if peek.Unavailable != "" {
		return s.historyFailed(asJSON, "unavailable", "history", "", peek.Unavailable)
	}
	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		for _, direction := range []struct {
			key   string
			label *string
		}{{"undo", peek.Undo}, {"redo", peek.Redo}} {
			w.Key(direction.key)
			if direction.label == nil {
				w.Null()
			} else {
				w.Str(*direction.label)
			}
		}
		w.KeyStr("store_revision", peek.StoreRevision)
		w.EndObject()
		out(w.String())
		return 0
	}
	for _, direction := range []struct {
		verb  string
		label *string
	}{{"undo", peek.Undo}, {"redo", peek.Redo}} {
		if direction.label == nil {
			out(direction.verb + ": nothing to " + direction.verb)
			continue
		}
		out(direction.verb + ": " + *direction.label)
	}
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
	register("history", (*surfaceContext).history)
	register("undo", (*surfaceContext).undo)
	register("redo", (*surfaceContext).redo)
}
