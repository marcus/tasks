package main

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// rollbackHints are the two sentences a rollback can earn, keyed by the stage
// that failed. They are separate because they name different events and
// different next steps: a post-write validation failure is repairable with
// `check`, while a failed write left nothing to repair. Neither interpolates
// the underlying error — its message carries an absolute path, which would make
// the diagnostic differ per machine.
var rollbackHints = map[store.RollbackStage]string{
	store.RollbackValidation: "file failed validation after the edit — run `tasks check`",
	store.RollbackWrite: "could not write the task file — the previous contents were restored " +
		"(nothing was changed)",
}

func rollbackHint(stage store.RollbackStage) string {
	if hint, ok := rollbackHints[stage]; ok {
		return hint
	}
	return rollbackHints[store.RollbackValidation]
}

// mutationErrorCodes are the statuses spelled differently on the wire than in
// the result vocabulary. The schema refusal keeps the name the API already
// uses, so a caller branches on one spelling across every surface.
var mutationErrorCodes = map[store.MutationStatus]string{
	store.MutationUnsupportedSchema: "unsupported_schema_version",
}

func mutationErrorCode(status store.MutationStatus) string {
	if code, ok := mutationErrorCodes[status]; ok {
		return code
	}
	return string(status)
}

// mutationFailed is the adapter for a typed store refusal. The command's own
// wording stays the first line; the result supplies the exit status and the
// second line, because only it knows whether bytes were written and reverted.
//
// Under --json the refusal is a document on stdout, not only prose on stderr:
// a caller that got nothing on stdout cannot tell a refusal from an empty
// result. `rolled_back` is the load-bearing field there — a mutation that wrote
// and reverted and one that was refused before it wrote leave byte-identical
// files behind and exit the same way.
func mutationResultFailed(result store.MutationResult, args []string, action, summary string) int {
	if result.OK() {
		return 0
	}
	message := summary
	switch {
	case result.RolledBack:
		message += "\n" + rollbackHint(result.RollbackStage)
	case result.Status == store.MutationUnsupportedSchema:
		detail := result.FirstError()
		if detail == "" {
			detail = "unsupported schema version"
		}
		message += "\n" + detail + " — this build cannot read this task file (nothing was written)"
	case result.Status == store.MutationStoreInvalid:
		message += "\ntask file is already invalid — run `tasks check` (nothing was written)"
	case result.Status == store.MutationUnavailable && store.IsLockTimeoutMessage(result.FirstError()):
		message += "\n" + result.FirstError()
	}

	if slices.Contains(args, "--json") {
		out(errorDocument(mutationErrorCode(result.Status), action, message, result.RolledBack))
	}
	fmt.Fprintln(os.Stderr, message)
	return result.ExitCode()
}

// reportTouched is the report every successful mutation prints: the canonical
// task documents for the ids it touched, in file order.
//
// It reads from the snapshot the mutation itself produced rather than
// re-reading the store, so the report describes the bytes that were written
// and not whatever a concurrent writer left behind afterwards.
func (s *surfaceContext) reportTouched(result store.MutationResult, ids []string, asJSON bool) int {
	return s.reportTouchedSnapshot(result.ReadSnapshot, ids, asJSON, nil)
}

// reportTouchedSnapshot is the same report over an explicitly supplied read.
//
// Two callers need it. `recur` adds a `next` member to the structured payload —
// the occurrence the schedule now names, which is the whole answer to "what did
// that do" — and its no-op clearing path has no mutation result at all, because
// it deliberately wrote nothing.
func (s *surfaceContext) reportTouchedSnapshot(snapshot *store.Snapshot, ids []string, asJSON bool,
	extra func(*jsonout.Writer)) int {

	if snapshot == nil {
		return abort("task store unavailable")
	}
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	queries := taskquery.New(snapshot, context)

	items := []store.Item{}
	snapshotItems := snapshot.Items()
	for _, id := range ids {
		for _, item := range snapshotItems {
			if item.ID == id {
				items = append(items, item)
				break
			}
		}
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Line < items[right].Line })

	if !asJSON {
		for _, item := range items {
			out(taskquery.Headline(item))
		}
		return 0
	}
	w := jsonout.New()
	w.BeginObject()
	w.Key("touched")
	w.BeginArray()
	for _, item := range items {
		writeItemJSON(w, queries, item)
	}
	w.EndArray()
	if extra != nil {
		extra(w)
	}
	w.EndObject()
	if err := w.Err(); err != nil {
		return abort(err.Error())
	}
	out(w.String())
	return 0
}

func (s *surfaceContext) temporalContext() (temporal.Context, int) {
	instant, err := determinism.NowForAdapter(env)
	if err != nil {
		return temporal.Context{}, abort(err.Error())
	}
	context, err := temporal.NewContext(instant, s.paths.Timezone, s.paths.TimeFormat)
	if err != nil {
		return temporal.Context{}, abort(err.Error())
	}
	return context, 0
}

// readerContext is the reader's clock for RENDERING ALONE, and unlike
// temporalContext it never fails loudly: a surface that is already printing a
// refusal still has to print it, and a zero context renders stored values
// unchanged rather than swallowing the sentence.
func (s *surfaceContext) readerContext() temporal.Context {
	instant, err := determinism.NowForAdapter(env)
	if err != nil {
		return temporal.Context{}
	}
	context, err := temporal.NewContext(instant, s.paths.Timezone, s.paths.TimeFormat)
	if err != nil {
		return temporal.Context{}
	}
	return context
}

// localizedStamps projects a stored instant WHERE IT APPEARS inside a sentence
// the store wrote. The store has no reader and cannot know a zone, and its
// wording is already the right wording — so the surface translates the stamp
// rather than re-authoring the sentence around it.
func (s *surfaceContext) localizedStamps(message, stored string) string {
	if stored == "" || !strings.Contains(message, stored) {
		return message
	}
	label := s.readerContext().StampLabel(stored)
	if label == stored {
		return message
	}
	return strings.ReplaceAll(message, stored, label)
}

// today is the reader's own calendar day, which is what every `Captured [...]`
// body and every `closed` date is stamped with.
func (s *surfaceContext) today() (string, int) {
	context, status := s.temporalContext()
	if status != 0 {
		return "", status
	}
	return context.LocalDate().ISO(), 0
}

// extractValue pulls `--name <value>` out of an argument list, returning the
// remaining arguments. Ruby's extract_value has the same shape, and the same
// consequence: the value is removed from the positional stream, so a later
// join of the rest cannot pick it up as title text.
func extractValue(args []string, name string) (string, []string, bool) {
	for index, arg := range args {
		if arg != name {
			continue
		}
		if index+1 >= len(args) {
			return "", args, false
		}
		rest := append(append([]string{}, args[:index]...), args[index+2:]...)
		return args[index+1], rest, true
	}
	return "", args, false
}

func joinPositional(values []string) string {
	return strings.TrimSpace(strings.Join(values, " "))
}
