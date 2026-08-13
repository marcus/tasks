package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// ArchiveBlock is one closed root the sweep refuses to move: archiving it would
// take live, open work into the archive with it.
type ArchiveBlock struct {
	RootID     string
	RootTitle  string
	OpenIDs    []string
	OpenTitles []string
}

// ArchivePreview is a read-only summary of what the next sweep would move.
// Roots are the DONE/CANCELLED tasks it selects; Descendants excludes those
// roots.
//
// CandidateIDs and Fingerprint exist for one purpose: a `--json` caller reads
// the preview to learn WHICH records will move, then pins the sweep to it. If
// anything changed in between — including the day stamp a moved record carries,
// so a sweep prepared either side of local midnight also refuses — the sweep
// refuses rather than reporting a stale list as though it were true.
type ArchivePreview struct {
	Roots       int
	Descendants int
	Blocks      []ArchiveBlock
	// CandidateIDs is every id in the moved set, roots and descendants, in
	// file order.
	CandidateIDs []string
	Fingerprint  string
	// Unavailable is set when the preview could not be taken. A zero Roots
	// with this empty is a real empty sweep; with it set, nothing was read.
	Unavailable string
}

// Total is every task the sweep would move.
func (p ArchivePreview) Total() int { return p.Roots + p.Descendants }

// Blocked reports whether any closed root still contains open work.
func (p ArchivePreview) Blocked() bool { return len(p.Blocks) > 0 }

// BlockedRoots is how many roots are blocked.
func (p ArchivePreview) BlockedRoots() int { return len(p.Blocks) }

// OpenDescendants is how many open tasks are blocking, across every root.
func (p ArchivePreview) OpenDescendants() int {
	total := 0
	for _, block := range p.Blocks {
		total += len(block.OpenIDs)
	}
	return total
}

// ArchiveRefusal names a safety gate that stopped the sweep. The spellings are
// the `reason` field of the CLI's error envelope and of the planned HTTP
// endpoint, so a caller branches on one vocabulary across every surface.
type ArchiveRefusal string

// The refusals.
const (
	// ArchiveNotRefused is the zero value: the sweep was allowed to proceed.
	ArchiveNotRefused ArchiveRefusal = ""
	// ArchiveUnsupportedSchema is the version gate.
	ArchiveUnsupportedSchema ArchiveRefusal = "unsupported_schema"
	// ArchivePreviewChanged means the live file moved while the sweep was being
	// prepared, so the pinned preview no longer describes it.
	ArchivePreviewChanged ArchiveRefusal = "preview_changed"
	// ArchiveOpenDescendants means a closed root still contains open work.
	ArchiveOpenDescendants ArchiveRefusal = "open_descendants"
	// ArchiveConflict means archive.jsonl already holds partial or differing
	// copies of the candidate ids — an interrupted sweep that cannot be
	// resumed safely.
	ArchiveConflict ArchiveRefusal = "archive_conflict"
	// ArchiveUnavailable means the store lock could not be taken in time.
	ArchiveUnavailable ArchiveRefusal = "unavailable"
)

// ArchiveResult is the outcome of one sweep.
type ArchiveResult struct {
	// Roots is how many closed roots moved. Zero with no refusal means there
	// was nothing to archive.
	Roots int
	// Refusal is the gate that stopped the sweep, or "" when none did.
	Refusal ArchiveRefusal
	// Preview is what the sweep saw, carried on every refusal that has one so
	// the caller can report the blocked subtrees without a second read.
	Preview ArchivePreview
	// Details are the conflicting ids behind ArchiveConflict.
	Details []string
	// Failed marks a sweep that WROTE and was rolled back. Live tasks were
	// preserved; the reason is in LastRollback.
	Failed bool
}

// OK reports a sweep that was allowed to run, whether or not it moved anything.
func (r ArchiveResult) OK() bool { return r.Refusal == ArchiveNotRefused && !r.Failed }

// ArchivePreviewFor is the read-only plan of the next sweep. `today` is the
// stamp a moved record would carry, and it is part of the fingerprint, so the
// caller must pass the same day to the sweep it pins with this preview.
func (s *Store) ArchivePreviewFor(today string) ArchivePreview {
	var preview ArchivePreview
	if err := s.withSharedLock(func() error {
		preview = archivePlanFor(freshRecords(s.org), today).preview
		return nil
	}); err != nil {
		return ArchivePreview{Unavailable: UnavailableMessage(err)}
	}
	return preview
}

// ArchiveSweep moves every fully closed DONE/CANCELLED task subtree to the
// archive file.
//
// The archive is written FIRST, then the live file. Interruption between the
// two can leave retry-safe duplicates across the pair, but can never silently
// lose a task — and the reverse order could. A retry converges only when every
// stable id has exactly one canonically equal archived copy; partial or
// mismatched overlap refuses and keeps the live data.
//
// `expected`, when supplied, pins the sweep to a preview the caller already
// reported: a store that changed in between refuses rather than moving a
// different set than it announced.
func (s *Store) ArchiveSweep(today string, expected *ArchivePreview) ArchiveResult {
	var result ArchiveResult
	err := s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if source, _ := s.unsupportedSchemaSource(); source != "" {
			result = ArchiveResult{Refusal: ArchiveUnsupportedSchema}
			return nil
		}

		plan := archivePlanFor(freshRecords(s.org), today)
		if expected != nil && !samePreview(*expected, plan.preview) {
			result = ArchiveResult{Refusal: ArchivePreviewChanged, Preview: plan.preview}
			return nil
		}
		if plan.preview.Blocked() {
			result = ArchiveResult{Refusal: ArchiveOpenDescendants, Preview: plan.preview}
			return nil
		}
		if len(plan.moved) == 0 {
			result = ArchiveResult{Roots: 0, Preview: plan.preview}
			return nil
		}

		archived := []record.Record{}
		if _, err := os.Stat(s.archive); err == nil {
			archived = freshRecords(s.archive)
		}
		if len(archived) == 0 {
			archived = []record.Record{metaRecord()}
		}
		state, conflicts := archiveRetryState(archived, plan.moved)
		if state == retryConflict {
			result = ArchiveResult{Refusal: ArchiveConflict, Preview: plan.preview, Details: conflicts}
			return nil
		}
		if state == retryNew {
			archived = append(archived, plan.moved...)
			if err := s.writeRecords(s.archive, archived); err != nil {
				// Nothing has been removed from the live file yet, so there is
				// nothing to put back. Deliberately no restore: see below.
				s.recordRollback(err.Error(), RollbackWrite)
				result = ArchiveResult{Failed: true, Preview: plan.preview}
				return nil
			}
		}

		// A successful atomic archive write is the commit point. Re-read it
		// before deleting the live records, so even an injected or custom writer
		// cannot make the destructive half proceed without a durable copy of
		// every moved id.
		persisted := map[string]bool{}
		for _, id := range idsOf(freshRecords(s.archive)) {
			persisted[id] = true
		}
		missing := []string{}
		for _, id := range idsOf(plan.moved) {
			if !persisted[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			s.recordRollback("archive write omitted moved ids: "+joinComma(missing), RollbackWrite)
			result = ArchiveResult{Failed: true, Preview: plan.preview}
			return nil
		}

		// A failed live write is the ONE failure this transaction does not roll
		// back, and the asymmetry is deliberate. The archive copy is already
		// durable; restoring `before` would delete it and leave the task in
		// exactly one place again — but only after having proved the machine
		// cannot write that place. Leaving both copies is retry-safe: the next
		// sweep sees a complete overlap and finishes the destructive half.
		// Duplicated data is recoverable. A task in neither file is not.
		if err := s.writeRecords(s.org, plan.kept); err != nil {
			s.recordRollback(err.Error(), RollbackWrite)
			result = ArchiveResult{Failed: true, Preview: plan.preview}
			return nil
		}
		// The post-write invariant: a mutation must never mangle either file,
		// and the sweep writes both. If it would, record why, restore BOTH, and
		// report failure rather than a count.
		if reason := s.postWriteFailure(); reason != "" {
			s.recordRollback(reason, RollbackValidation)
			_ = s.restore(before)
			result = ArchiveResult{Failed: true, Preview: plan.preview}
			return nil
		}
		s.journal().Record("archive sweep", before, s.fileSnapshot(), "", false)
		result = ArchiveResult{Roots: plan.preview.Roots, Preview: plan.preview}
		return nil
	})
	if err != nil {
		return ArchiveResult{Refusal: ArchiveUnavailable, Details: []string{UnavailableMessage(err)}}
	}
	return result
}

func samePreview(expected, actual ArchivePreview) bool {
	if expected.Fingerprint != actual.Fingerprint ||
		len(expected.CandidateIDs) != len(actual.CandidateIDs) {
		return false
	}
	for index := range expected.CandidateIDs {
		if expected.CandidateIDs[index] != actual.CandidateIDs[index] {
			return false
		}
	}
	return true
}

type archivePlan struct {
	kept    []record.Record
	moved   []record.Record
	preview ArchivePreview
}

// archivePlanFor selects the subtrees to move. A closed root takes its WHOLE
// subtree, which is why an open descendant blocks it: the archive is not a
// place live work may end up by accident.
func archivePlanFor(records []record.Record, today string) archivePlan {
	kept := []record.Record{}
	moved := []record.Record{}
	blocks := []ArchiveBlock{}
	roots, descendants := 0, 0

	for index := 0; index < len(records); {
		parsed := records[index]
		if parsed.String("type") != "task" || !contains(check.ClosedStates, parsed.String("state")) {
			kept = append(kept, parsed)
			index++
			continue
		}
		end := subtreeEnd(records, index)
		group := record.CloneAll(records[index:end])

		openIDs, openTitles := []string{}, []string{}
		for _, child := range group[1:] {
			if child.String("type") == "task" && !contains(check.ClosedStates, child.String("state")) {
				openIDs = append(openIDs, child.String("id"))
				openTitles = append(openTitles, child.String("title"))
			}
		}
		if len(openIDs) > 0 {
			blocks = append(blocks, ArchiveBlock{
				RootID: parsed.String("id"), RootTitle: parsed.String("title"),
				OpenIDs: openIDs, OpenTitles: openTitles,
			})
		}

		// The root leaves its parent behind — it no longer hangs under a live
		// section — and gains the day it was swept.
		group[0].Delete("parent")
		group[0].SetString("archived", today)

		tasks := 0
		for _, child := range group {
			if child.String("type") == "task" {
				tasks++
			}
		}
		moved = append(moved, group...)
		roots++
		descendants += tasks - 1
		index = end
	}

	fingerprint := ""
	if text, err := record.Dump(moved); err == nil {
		digest := sha256.Sum256([]byte(text))
		fingerprint = hex.EncodeToString(digest[:])
	}
	return archivePlan{
		kept: kept, moved: moved,
		preview: ArchivePreview{
			Roots: roots, Descendants: descendants, Blocks: blocks,
			CandidateIDs: idsOf(moved), Fingerprint: fingerprint,
		},
	}
}

type retryState int

const (
	retryNew retryState = iota
	retryComplete
	retryConflict
)

// archiveRetryState decides whether a sweep may proceed against the archive it
// finds. It is safe only when the archive contains NONE of the moved ids (a
// fresh sweep) or exactly one canonically equal copy of every one of them (an
// interrupted archive-first sweep, resumed). Partial overlap, a duplicate id,
// or differing content is a conflict: retain the live data and require a human
// to reconcile it.
func archiveRetryState(archived, moved []record.Record) (retryState, []string) {
	byID := map[string][]record.Record{}
	for _, parsed := range archived {
		if id := parsed.String("id"); id != "" {
			byID[id] = append(byID[id], parsed)
		}
	}
	movedIDs := idsOf(moved)
	overlap := []string{}
	for _, id := range movedIDs {
		if _, present := byID[id]; present {
			overlap = append(overlap, id)
		}
	}
	if len(overlap) == 0 {
		return retryNew, nil
	}

	conflicts := []string{}
	seen := map[string]bool{}
	for _, id := range movedIDs {
		copies := byID[id]
		var expected record.Record
		for _, candidate := range moved {
			if candidate.String("id") == id {
				expected = candidate
				break
			}
		}
		if len(copies) != 1 || !archiveRetryRecord(expected, copies[0]) {
			if !seen[id] {
				conflicts = append(conflicts, id)
				seen[id] = true
			}
		}
	}
	// A partial overlap needs no separate rule: an id the archive does not hold
	// has zero copies, which already fails the "exactly one" test above. Ruby
	// unions those ids in explicitly; the union is a no-op for the same reason.
	if len(conflicts) == 0 {
		return retryComplete, nil
	}
	return retryConflict, conflicts
}

// archiveRetryRecord compares a proposed archived record with the copy already
// there, ignoring the two stamps the FIRST write owns.
//
// A retry after midnight must not conflict merely because today's proposed
// `archived` date has advanced; and moving into the archive is a write for
// every task in the subtree, so the durable copy owns each task's `updated`
// too.
// Ruby compares two Hashes, which is insensitive to key order, so this
// compares key sets and values rather than serialized bytes: a forward-compatible
// key a newer binary wrote in a different position is the same record.
func archiveRetryRecord(expected, actual record.Record) bool {
	want := record.Clone(expected)
	have := record.Clone(actual)
	want.Delete(record.LineKey)
	have.Delete(record.LineKey)
	if want.Has("archived") && have.Has("archived") {
		value, _ := have.Get("archived")
		want.Set("archived", value)
	}
	if value, present := have.Get("updated"); present {
		want.Set("updated", value)
	}
	left, leftOK := comparableFields(want)
	right, rightOK := comparableFields(have)
	if !leftOK || !rightOK || len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if other, present := right[key]; !present || other != value {
			return false
		}
	}
	return true
}

func comparableFields(parsed record.Record) (map[string]string, bool) {
	fields := map[string]string{}
	for _, field := range parsed.Fields {
		if _, seen := fields[field.Key]; seen {
			continue
		}
		canonical, err := canonical(field.Value)
		if err != nil {
			return nil, false
		}
		fields[field.Key] = string(canonical)
	}
	return fields, true
}

func metaRecord() record.Record {
	return record.Record{Fields: []record.Field{
		{Key: "type", Value: record.RawString("meta")},
		{Key: "version", Value: record.RawInt(check.Version)},
	}}
}

func joinComma(values []string) string {
	out := ""
	for index, value := range values {
		if index > 0 {
			out += ", "
		}
		out += value
	}
	return out
}
