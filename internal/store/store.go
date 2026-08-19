// Package store owns the READ path over tasks.jsonl and archive.jsonl: the
// coherent snapshot, the API-grade checked read, and the revision tokens
// derived from the exact bytes that read captured.
//
// There is deliberately no write path here — no journal append, no atomic
// replace, no mutation. What there IS, and what a read-only package would not
// obviously need, is the lock: a read takes the same advisory sidecar lock a
// mutation does, so `.tasks.jsonl.lock` is a real observable effect of reading
// and the conformance corpus compares its presence and its mode. Reads take a
// shared lock; mutations take exclusive. Acquire waits briefly and then fails
// with the holder's pid rather than blocking forever.
package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/record"
)

// Status is the outcome of a checked read.
type Status string

const (
	// StatusOK means both files parsed and validated; a snapshot is present.
	StatusOK Status = "ok"
	// StatusUnsupportedSchema means a file declares a schema version this
	// binary cannot read. Its records are never interpreted: reading v1 or a
	// future v3 as if it were v2 is how a store gets silently corrupted.
	StatusUnsupportedSchema Status = "unsupported_schema"
	// StatusStoreInvalid means the bytes parsed but failed validation.
	StatusStoreInvalid Status = "store_invalid"
	// StatusUnavailable means the bytes could not be read at all.
	StatusUnavailable Status = "unavailable"
)

// Source names which file an item came from.
type Source string

const (
	// SourceLive is tasks.jsonl.
	SourceLive Source = "live"
	// SourceArchive is archive.jsonl.
	SourceArchive Source = "archive"
)

// Entry is one validation diagnostic. It carries only source, line and a safe
// message — never a configured filesystem path.
type Entry struct {
	Source  Source `json:"source,omitempty"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// CheckedRead is the typed result of the API-grade read path. Unlike Snapshot
// it can represent an invalid or unavailable store without attempting to build
// a tree from untrusted records.
type CheckedRead struct {
	Status        Status
	Snapshot      *Snapshot
	StoreRevision string
	Errors        []Entry
	Warnings      []Entry
}

// OK reports whether the read produced a snapshot.
func (c CheckedRead) OK() bool { return c.Status == StatusOK }

// Store owns one live/archive pair. A store built with New can only read; one
// built with NewWriter carries the Options a mutation needs — the journal
// directory, the clock, the device, and the id mint.
type Store struct {
	org     string
	archive string
	options Options

	// rollbackMu guards the last-rollback pair. A Store is shared by a surface
	// that may serve two requests at once, and the pair is written on a failure
	// path where a torn read would misattribute the stage.
	rollbackMu        sync.Mutex
	lastRollback      string
	lastRollbackStage RollbackStage
	lastLockError     string
}

// New builds a store over the two resolved paths.
func New(org, archive string) *Store { return &Store{org: org, archive: archive} }

// Org is the resolved live path, as the caller spelled it.
func (s *Store) Org() string { return s.org }

// Archive is the resolved archive path, as the caller spelled it.
func (s *Store) Archive() string { return s.archive }

// LockPath is the per-file advisory sidecar beside the resolved live file.
// The path is canonicalized first, so two spellings of the same file (a
// symlink, a relative path) lock in common rather than each taking a lock the
// other cannot see.
func (s *Store) LockPath() string {
	target := journal.Canonical(s.org)
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".lock")
}

// ReadSnapshot captures live records and, when requested, archive records
// together under the store lock. The result never changes in place, so a
// caller can hold it across a render without mixing fields from a later
// reload.
func (s *Store) ReadSnapshot(includeArchive bool) (*Snapshot, error) {
	var snapshot *Snapshot
	err := s.withSharedLock(func() error {
		live, err := captureReadSource(s.org, false, false, s.checkOptions())
		if err != nil {
			return err
		}
		archive := emptyReadSource(false)
		if includeArchive {
			if archive, err = captureReadSource(s.archive, true, false, s.checkOptions()); err != nil {
				return err
			}
		}
		snapshot, err = buildReadSnapshot(live, archive, includeArchive)
		return err
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// buildError marks a failure that is NOT an I/O failure — today, a value the
// JSON generator refuses while a revision is being computed. The two are kept
// apart on purpose: an unreadable store is the reportable `unavailable`
// status, while a value no digest can be taken over is a genuine error the
// caller must surface rather than dress up as a store that was not there.
type buildError struct{ err error }

func (e *buildError) Error() string { return e.err.Error() }
func (e *buildError) Unwrap() error { return e.err }

// CheckedReadSnapshot captures and validates both files under ONE store lock,
// returning canonical resources and a content-derived global revision from
// those exact bytes. The archive is optional, matching first-run behaviour;
// the live file is required. Invalid records never reach the tree.
//
// An unreadable store is the `unavailable` STATUS, not an error return: that
// is a fact about the store, and a caller has to be able to report it.
func (s *Store) CheckedReadSnapshot() (CheckedRead, error) {
	var result CheckedRead
	err := s.withSharedLock(func() error {
		live, err := captureReadSource(s.org, false, true, s.checkOptions())
		if err != nil {
			return err
		}
		archive, err := captureReadSource(s.archive, true, true, s.checkOptions())
		if err != nil {
			return err
		}
		storeRevision := StoreRevisionForContents(live.raw, archive.raw)
		errorEntries := append(annotate(live.check.Errors, SourceLive), annotate(archive.check.Errors, SourceArchive)...)
		warnings := append(annotate(live.check.Warnings, SourceLive), annotate(archive.check.Warnings, SourceArchive)...)

		// A store written under a different schema version is refused before
		// its records are interpreted. There is no migration path — this
		// binary reads exactly one schema version.
		for _, capture := range []struct {
			source Source
			value  readSource
		}{{SourceLive, live}, {SourceArchive, archive}} {
			declared, unsupported := check.UnsupportedVersion(capture.value.records)
			if !unsupported {
				continue
			}
			result = CheckedRead{
				Status: StatusUnsupportedSchema, StoreRevision: storeRevision,
				Errors:   []Entry{{Source: capture.source, Line: 1, Message: check.UnsupportedVersionMessage(declared)}},
				Warnings: warnings,
			}
			return nil
		}

		if len(errorEntries) > 0 {
			result = CheckedRead{
				Status: StatusStoreInvalid, StoreRevision: storeRevision,
				Errors: errorEntries, Warnings: warnings,
			}
			return nil
		}

		snapshot, err := buildReadSnapshot(live, archive, true)
		if err != nil {
			return &buildError{err}
		}
		result = CheckedRead{
			Status: StatusOK, Snapshot: snapshot,
			StoreRevision: storeRevision, Warnings: warnings,
		}
		return nil
	})
	var failed *buildError
	if errors.As(err, &failed) {
		return CheckedRead{}, failed.err
	}
	if err != nil {
		return CheckedRead{
			Status: StatusUnavailable,
			Errors: []Entry{{Line: 0, Message: UnavailableMessage(err)}},
		}, nil
	}
	return result, nil
}

func annotate(entries []check.Entry, source Source) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, Entry{Source: source, Line: entry.Line, Message: entry.Message})
	}
	return out
}

// readSource is bytes, parsed records and the validation of that exact parse,
// all from one file descriptor.
type readSource struct {
	raw     []byte
	records []record.Record
	check   check.Result
}

// captureReadSource reads one file under the caller's lock. API-grade reads
// additionally validate the parse they just made; ordinary reads do not pay
// for a structural check they never consume.
//
// A missing archive is an empty optional history. A missing live file is a
// validation error rather than an I/O failure, because "the store is not there
// yet" is a first-run state, not a broken host.
func captureReadSource(path string, optional, validate bool, options check.Options) (readSource, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if optional {
				return emptyReadSource(validate), nil
			}
			source := emptyReadSource(validate)
			if validate {
				source.check = check.Result{Errors: []check.Entry{{Line: 0, Message: "file not found"}}, Warnings: []check.Entry{}}
			}
			return source, nil
		}
		return readSource{}, err
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		return readSource{}, err
	}
	if raw == nil {
		raw = []byte{}
	}
	if !utf8.Valid(raw) {
		source := readSource{raw: raw}
		if validate {
			source.check = check.Result{
				Errors:   []check.Entry{{Line: 0, Message: "file is not valid UTF-8"}},
				Warnings: []check.Entry{},
			}
		}
		return source, nil
	}
	parsed := record.Parse(raw)
	source := readSource{raw: raw, records: parsed.Records}
	if validate {
		source.check = check.CheckParsedWith(parsed, options)
	}
	return source, nil
}

func emptyReadSource(validate bool) readSource {
	source := readSource{}
	if validate {
		source.check = check.Result{Errors: []check.Entry{}, Warnings: []check.Entry{}}
	}
	return source
}

func buildReadSnapshot(live, archive readSource, includeArchive bool) (*Snapshot, error) {
	archiveRecords := []record.Record{}
	if includeArchive {
		archiveRecords = archive.records
	}
	liveRevisions, err := taskRevisions(live.records)
	if err != nil {
		return nil, err
	}
	archiveRevisions := map[string]string{}
	if includeArchive {
		if archiveRevisions, err = taskRevisions(archiveRecords); err != nil {
			return nil, err
		}
	}
	return &Snapshot{
		liveRecords:    live.records,
		archiveRecords: archiveRecords,
		items:          buildItems(live.records, SourceLive),
		archiveItems:   buildItems(archiveRecords, SourceArchive),
		archiveLoaded:  includeArchive,
		revisions: map[Source]map[string]string{
			SourceLive: liveRevisions, SourceArchive: archiveRevisions,
		},
	}, nil
}

// Resource is one revisioned object in a snapshot: the shape the HTTP surface
// publishes and the conformance probe reports.
type Resource struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Revision string `json:"revision"`
}

// Resources is every identified item with its revision, sorted by (id, kind)
// so two implementations that agree about content agree about order too.
func (s *Snapshot) Resources() []Resource {
	resources := []Resource{}
	for _, group := range []struct {
		items []Item
		kind  string
	}{{s.items, "task"}, {s.archiveItems, "archived_task"}} {
		for _, item := range group.items {
			if item.ID == "" {
				continue
			}
			resources = append(resources, Resource{ID: item.ID, Kind: group.kind, Revision: s.RevisionFor(item)})
		}
	}
	sort.SliceStable(resources, func(left, right int) bool {
		if resources[left].ID != resources[right].ID {
			return resources[left].ID < resources[right].ID
		}
		return resources[left].Kind < resources[right].Kind
	})
	return resources
}
