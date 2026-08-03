package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// release hands a claim back to the agent-ready queue: claimed → ready, with the
// assignee dropped.
//
// A worker must supply the id that matches the live claim; the owner passes
// --force, which stands in for a worker id, to clear a stale one. A --note
// appends a blocker line to the body in the SAME undo step, so one user action
// costs one undo.
func (s *surfaceContext) release(args []string) int {
	workerFlag, rest, _ := extractValue(args, "--worker")
	note, rest, hasNote := extractValue(rest, "--note")
	flags, rest, err := takeFlags(rest, "--json", "--force")
	if err != nil {
		return abort(err.Error())
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return abort(`usage: tasks release <ref> --worker <id> [--note "blocker"] [--force]`)
	}
	worker := strings.TrimSpace(workerFlag)
	if worker == "" {
		worker = strings.TrimSpace(env.Get("TASKS_WORKER_ID"))
	}
	if worker == "" && !flags["--force"] {
		return abort("missing worker id — " + workerHint)
	}

	queries, status := s.readQueries(args, "release")
	if status != 0 {
		return status
	}
	item, code := resolveRef(queries, rest[0], refScope{includeDone: true})
	if code != 0 {
		return code
	}

	// One key per operation, so only THIS release's own note write merges into
	// its journal entry and never an unrelated neighbouring edit.
	coalesceKey := s.delegationCoalesceKey("release")
	writer := s.writeStore()
	result := writer.Release(item.ID, worker, flags["--force"], coalesceKey)
	if status := s.delegationFailed(result, args, "release"); status != 0 {
		return status
	}

	// The note is appended only AFTER the release succeeded, so a refused
	// release never leaves a stray blocker line behind. A failed note never
	// turns a successful release into a failure — it is reported alongside.
	if hasNote && note != "" {
		if reason := s.appendReleaseNote(writer, queries, item, note, coalesceKey); reason != "" {
			fmt.Fprintln(os.Stderr, "note was not appended: "+reason)
		} else if fresh, ok := s.reReadAfterNote(item.ID); ok {
			result.ReadSnapshot = fresh
		}
	}

	if flags["--json"] {
		return s.reportTouched(result, []string{item.ID}, true)
	}
	written, ok := s.delegationItem(result, item.ID)
	if !ok {
		written = item
	}
	out("released → " + delegationHeadline(queries, written))
	return 0
}

// appendReleaseNote is the composed half of a release, written through the
// store's own single-field transaction under the release's coalesce key.
//
// It returns the reason it did not apply, or "" on success. Unusable text
// degrades to a stated "not applied" rather than to a panic on top of a
// completed write.
func (s *surfaceContext) appendReleaseNote(writer *store.Store, queries *taskquery.Queries,
	item store.Item, note, coalesceKey string) string {

	if !utf8.ValidString(note) {
		return "release note is not valid UTF-8 text"
	}
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return ""
	}
	fresh, ok := queries.FindLive(item.ID)
	if !ok {
		fresh = item
	}
	body := strings.Join(queries.Body(fresh), "\n")
	value := trimmed
	if body != "" {
		value = body + "\n" + trimmed
	}
	today, status := s.today()
	if status != 0 {
		return "task store unavailable"
	}
	result := writer.PatchTaskCoalesced(item.ID, store.FieldBody, value,
		patchBaseline(writer, item.ID, store.FieldBody), "release: "+item.Title, today, coalesceKey)
	if result.OK() {
		return ""
	}
	return defaultText(result.FirstError(), string(result.Status))
}

// reReadAfterNote re-reads so the report describes the task AFTER both halves
// of the composed write, not after only the first.
func (s *surfaceContext) reReadAfterNote(id string) (*store.Snapshot, bool) {
	snapshot, err := s.store.ReadSnapshot(false)
	if err != nil {
		return nil, false
	}
	for _, item := range snapshot.Items {
		if item.ID == id {
			return snapshot, true
		}
	}
	return nil, false
}

func init() {
	register("release", (*surfaceContext).release)
}
