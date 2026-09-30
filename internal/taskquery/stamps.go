package taskquery

import (
	"regexp"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/updatestamp"
)

// The lifecycle dates a task resource reports beside its own fields. None of
// them is a writable field: each is read from what the store already keeps,
// and each answers false rather than guessing when that record is missing.

// capturedLine is the note every capture writes as the body's first line,
// `Captured [YYYY-MM-DD].`. The bracket may carry an org-style weekday or time
// after the date and the closing period is optional, so an older or hand-typed
// spelling of the same note still dates the task.
var capturedLine = regexp.MustCompile(`^Captured \[(\d{4}-\d{2}-\d{2})(?: [^\]]*)?\]\.?$`)

// Created is the day the task was captured. There is no stored creation stamp,
// so the source is the `Captured [date]` note a capture writes into the body:
// the first body line of that shape whose date is a real calendar day. A body
// edit that removed the note leaves the task undated, and that is reported
// as false rather than approximated from `updated` or the id.
func (q *Queries) Created(item store.Item) (string, bool) {
	for _, line := range q.Body(item) {
		match := capturedLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if _, ok := temporal.ParseDate(match[1]); ok {
			return match[1], true
		}
	}
	return "", false
}

// UpdatedAt is the instant of the task's stored last-write stamp. The device
// half of the stamp is sync bookkeeping and is dropped; a missing or malformed
// stamp answers false.
func (q *Queries) UpdatedAt(item store.Item) (time.Time, bool) {
	stamp, _, ok := updatestamp.Key(item.Updated)
	if !ok {
		return time.Time{}, false
	}
	instant, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return instant.UTC(), true
}

// ArchivedOn is the day an archived task was swept to the archive. A sweep
// stamps `archived` on the subtree root only, so a descendant answers with its
// nearest stamped ancestor's date. A live task was never archived and answers
// false.
func (q *Queries) ArchivedOn(item store.Item) (string, bool) {
	if item.Source != store.SourceArchive {
		return "", false
	}
	if q.archiveByID == nil {
		q.archiveByID = map[string]store.Item{}
		for _, archived := range q.snapshot.ArchiveItems() {
			if archived.HasID {
				q.archiveByID[archived.ID] = archived
			}
		}
	}
	current, seen := item, map[string]bool{}
	for {
		if current.Archived != "" {
			return current.Archived, true
		}
		if !current.HasParent || seen[current.Parent] {
			return "", false
		}
		seen[current.Parent] = true
		parent, found := q.archiveByID[current.Parent]
		if !found {
			return "", false
		}
		current = parent
	}
}
