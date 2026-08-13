package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// CheckLive is the default `tasks check` report: the live file's structural
// lint, plus the archive's schema-version header.
//
// Structural errors stay file-scoped — the archive's own records are what
// `--all-files` is for. The version gate is the deliberate exception, because
// it is the one condition that is store-wide rather than file-scoped: a v1
// archive under a v2 live file makes every read and every mutation refuse the
// whole store, so a check that could not see it would answer "no structural
// errors" to a user the refusal had just sent here.
func (s *Store) CheckLive() check.Result {
	result := check.Check(s.org)
	source, declared := s.unsupportedSchemaSource()
	if source != SourceArchive {
		return result
	}
	errors := append([]check.Entry{{Line: 1, Message: "archive.jsonl: " + check.UnsupportedVersionMessage(declared)}}, result.Errors...)
	return check.Result{Errors: errors, Warnings: result.Warnings}
}

// CheckFiles is `--all-files`: coherent validation over live and archive under
// ONE store lock, plus the store-wide id invariant. Unlike the API's checked
// read this deliberately rejects even a retry-safe id shared across both files
// — a git push must wait until an archive operation converges to one durable
// location.
func (s *Store) CheckFiles() check.Result {
	var result check.Result
	err := s.withSharedLock(func() error {
		live, err := captureReadSource(s.org, false, true)
		if err != nil {
			return err
		}
		archive, err := captureReadSource(s.archive, true, true)
		if err != nil {
			return err
		}
		errors := annotateCheck(live.check.Errors, "tasks.jsonl")
		errors = append(errors, annotateCheck(archive.check.Errors, "archive.jsonl")...)
		errors = append(errors, crossFileDuplicates(live.records, archive.records)...)
		warnings := annotateCheck(live.check.Warnings, "tasks.jsonl")
		warnings = append(warnings, annotateCheck(archive.check.Warnings, "archive.jsonl")...)
		result = check.Result{Errors: sortByLine(errors), Warnings: sortByLine(warnings)}
		return nil
	})
	if err != nil {
		return check.Result{Errors: []check.Entry{{Line: 0, Message: UnavailableMessage(err)}}, Warnings: []check.Entry{}}
	}
	return result
}

func annotateCheck(entries []check.Entry, source string) []check.Entry {
	out := make([]check.Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, check.Entry{Line: entry.Line, Message: source + ": " + entry.Message})
	}
	return out
}

// crossFileDuplicates reports an id visible in BOTH files, keyed on the archive
// line so the diagnostic points at the copy a sweep would have to resolve. Ruby
// sorts the shared ids before reporting, so two stores with the same defect
// report it in the same order.
func crossFileDuplicates(live, archive []record.Record) []check.Entry {
	liveIDs := idLines(live)
	archiveIDs := idLines(archive)
	shared := []string{}
	for id := range liveIDs {
		if _, both := archiveIDs[id]; both {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	entries := make([]check.Entry, 0, len(shared))
	for _, id := range shared {
		entries = append(entries, check.Entry{
			Line: archiveIDs[id],
			Message: fmt.Sprintf("id %q appears in both tasks.jsonl line %d and archive.jsonl line %d",
				id, liveIDs[id], archiveIDs[id]),
		})
	}
	return entries
}

func idLines(records []record.Record) map[string]int {
	ids := map[string]int{}
	for _, parsed := range records {
		for _, field := range parsed.Fields {
			if field.Key != "id" {
				continue
			}
			var id string
			if json.Unmarshal(field.Value, &id) == nil {
				if _, seen := ids[id]; !seen {
					ids[id] = parsed.Line
				}
			}
		}
	}
	return ids
}

func sortByLine(entries []check.Entry) []check.Entry {
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].Line < entries[right].Line })
	return entries
}

// unsupportedSchemaSource names the file whose FIRST line declares a schema
// version this build cannot read, if either does. Only the first line is read:
// the meta record is the header, and a store whose header this build cannot
// interpret is refused before anything else is parsed.
func (s *Store) unsupportedSchemaSource() (Source, json.RawMessage) {
	for _, candidate := range []struct {
		source   Source
		path     string
		optional bool
	}{{SourceLive, s.org, false}, {SourceArchive, s.archive, true}} {
		if candidate.optional {
			if _, err := os.Stat(candidate.path); err != nil {
				continue
			}
		}
		if declared, skewed := declaredMetaSkew(candidate.path); skewed {
			return candidate.source, declared
		}
	}
	return "", nil
}

func declaredMetaSkew(path string) (json.RawMessage, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	first, err := readLine(file)
	if err != nil || len(first) == 0 {
		return nil, false
	}
	return check.UnsupportedVersion(record.Parse(first).Records)
}

// readLine is Ruby's IO#gets: bytes through the first newline, inclusive.
func readLine(reader io.Reader) ([]byte, error) {
	line := make([]byte, 0, 256)
	buffer := make([]byte, 1)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			line = append(line, buffer[0])
			if buffer[0] == '\n' {
				return line, nil
			}
		}
		if err == io.EOF {
			return line, nil
		}
		if err != nil {
			return line, err
		}
	}
}

// CreatePreflightFailure is the gate a creating mutation passes before it may
// inspect or extend the store: both files must already validate. It returns the
// FIRST error's message, which is what the refusal quotes.
//
// An empty or missing live file is the deliberate exception — that is the
// first-run state, and creation bootstraps its meta and Inbox records. Any
// non-empty file, including an archive, has to be valid first: extending a file
// that is already broken is how one bad record becomes two.
func (s *Store) CreatePreflightFailure() (string, bool) {
	message, ok := "", true
	// Under the store lock, exactly as the mutation that follows it would be:
	// the preflight has to describe the bytes another writer cannot be changing
	// underneath it, and taking the lock is itself an observable effect.
	if err := s.withSharedLock(func() error {
		message, ok = s.createPreflightFailure()
		return nil
	}); err != nil {
		return UnavailableMessage(err), false
	}
	return message, ok
}

func (s *Store) createPreflightFailure() (string, bool) {
	paths := []string{s.org}
	if _, err := os.Stat(s.archive); err == nil {
		paths = append(paths, s.archive)
	}
	for _, path := range paths {
		if path == s.org && emptyOrMissing(path) {
			continue
		}
		result := check.Check(path)
		if result.OK() {
			continue
		}
		if len(result.Errors) > 0 {
			return result.Errors[0].Message, false
		}
		return "validation failed", false
	}
	return "", true
}

func emptyOrMissing(path string) bool {
	info, err := os.Stat(path)
	return err != nil || info.Size() == 0
}
