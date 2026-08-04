package store

import (
	"strings"

	"tasks-go/internal/check"
	"tasks-go/internal/journal"
	"tasks-go/internal/record"
)

// The section and project lifecycle.
//
// These are the only mutations that report failure through a bare id, count or
// boolean rather than a MutationResult, and Ruby's shape is kept deliberately:
// `create_section!`, `rename_section!`, `complete_project!` and
// `archive_project!` all predate the typed result and their callers branch on
// the falsy value. What that costs is visible in the application layer — a
// rolled-back write and a clean no-op both return zero — which is exactly why
// LastRollback exists and why every one of them clears it first.
//
// `create_project!` is the exception. It bootstraps the "Projects" root and
// inserts the project beneath it in ONE checked write, so a failed child
// insertion cannot leave a root and a stray undo entry behind, and that is worth
// a typed result.

// withHistory is Store#with_history: the lock, the before-snapshot, the body,
// and — only if the body actually changed the files — post-write validation, a
// rollback on failure, and one journal step.
//
// The "only if changed" gate is load-bearing. A refusal may be inspecting a
// preexisting invalid file precisely to report an actionable conflict; it wrote
// nothing, so validating and rolling back would turn a correct diagnosis into a
// failure. `failed` is the value the caller reports when a rollback happened.
func (s *Store) withHistory(label string, body func() (changed bool)) bool {
	ok := true
	_ = s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if !body() {
			ok = false
			return nil
		}
		after := s.fileSnapshot()
		if sameSnapshot(before, after) {
			return nil
		}
		if reason := s.postWriteFailure(); reason != "" {
			s.recordRollback(reason, RollbackValidation)
			_ = s.restore(before)
			ok = false
			return nil
		}
		s.journal().Record(label, before, after, "", false)
		return nil
	})
	return ok
}

func sameSnapshot(left, right journal.Snapshot) bool {
	return equalOptionalText(left.Org, right.Org) && equalOptionalText(left.Archive, right.Archive)
}

// CreateSection creates a new empty section.
//
// With no parent it is appended at end of file as a top-level list; with a
// parent section id it is inserted as the LAST record of that section's subtree,
// which the DFS pre-order invariant keeps valid. The id is minted against live
// AND archived ids, so a fresh section can never collide with swept history.
//
// It returns the new section id, or "" when the title is blank, the parent names
// no section, or the write rolled back.
func (s *Store) CreateSection(title, parentID string) string {
	title = rubyStrip(title)
	if title == "" {
		return ""
	}
	created := ""
	ok := s.withHistory("create section: "+title, func() bool {
		records := freshRecords(s.org)
		if len(records) == 0 {
			records = []record.Record{metaRecord()}
		}
		insertAt := len(records)
		if parentID != "" {
			parentIndex := sectionIndex(records, parentID)
			if parentIndex < 0 {
				return false
			}
			insertAt = subtreeEnd(records, parentIndex)
		}
		id := s.genID(append(idsOf(records), s.archivedIDs()...))
		fresh := record.Record{Fields: []record.Field{
			{Key: "type", Value: record.RawString("section")},
			{Key: "id", Value: record.RawString(id)},
			{Key: "title", Value: record.RawString(title)},
		}}
		if parentID != "" {
			fresh.SetString("parent", parentID)
		}
		if s.writeRecords(s.org, spliceAt(records, insertAt, []record.Record{fresh})) != nil {
			return false
		}
		created = id
		return true
	})
	if !ok {
		return ""
	}
	return created
}

// CreateProject creates one project section under the top-level "Projects"
// root, bootstrapping that root in the SAME checked write when it is absent.
//
// The duplicate check spans the root's whole child list, projects and areas
// alike, because those titles are the project-ref candidate set: a second
// "Home" would make every later ref to it ambiguous.
func (s *Store) CreateProject(title string) MutationResult {
	title = rubyStrip(title)
	if title == "" {
		return MutationResult{
			Status: MutationInvalid, Errors: []string{"title cannot be blank"},
			FieldErrors: map[string][]string{"title": {"cannot be blank"}},
		}
	}

	var result MutationResult
	err := s.withLock(func() error {
		s.clearRollback()
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		if reason, ok := s.createPreflightFailure(); !ok {
			result = MutationResult{Status: MutationStoreInvalid, Errors: []string{reason}}
			return nil
		}

		records := record.CloneAll(freshRecords(s.org))
		if len(records) == 0 {
			records = []record.Record{metaRecord()}
		}
		rootIndex := -1
		for index, parsed := range records {
			if parsed.String("type") == "section" && !parsed.Truthy("parent") &&
				strings.EqualFold(rubyStrip(parsed.String("title")), "Projects") {
				rootIndex = index
				break
			}
		}
		createdRoot := rootIndex < 0
		taken := append(idsOf(records), s.archivedIDs()...)
		rootID := ""
		if createdRoot {
			rootID = s.genID(taken)
			records = append(records, record.Record{Fields: []record.Field{
				{Key: "type", Value: record.RawString("section")},
				{Key: "id", Value: record.RawString(rootID)},
				{Key: "title", Value: record.RawString("Projects")},
			}})
			rootIndex = len(records) - 1
			taken = append(taken, rootID)
		} else {
			rootID = records[rootIndex].String("id")
		}

		for _, parsed := range records {
			if parsed.String("type") == "section" && parsed.String("parent") == rootID &&
				strings.EqualFold(rubyStrip(parsed.String("title")), title) {
				message := "a project or area named " + rubyInspectText(title) + " already exists"
				result = MutationResult{
					Status: MutationInvalid, Errors: []string{message},
					FieldErrors: map[string][]string{"title": {message}},
				}
				return nil
			}
		}

		projectID := s.genID(taken)
		fresh := record.Record{Fields: []record.Field{
			{Key: "type", Value: record.RawString("section")},
			{Key: "id", Value: record.RawString(projectID)},
			{Key: "title", Value: record.RawString(title)},
			{Key: "parent", Value: record.RawString(rootID)},
		}}
		records = spliceAt(records, subtreeEnd(records, rootIndex), []record.Record{fresh})
		if _, err := record.Dump(records); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, records, "create project: "+title, "")
		if result.Status == MutationOK {
			result.TouchedIDs = []string{projectID}
			if createdRoot {
				result.TouchedIDs = []string{projectID, rootID}
			}
			result.Summary = MutationSummary{CreatedID: projectID, RootID: rootID, CreatedRoot: createdRoot}
		}
		return nil
	})
	if err != nil {
		return MutationResult{Status: MutationUnavailable, Errors: []string{"task store unavailable"}}
	}
	return result
}

// SectionNamed is the record of the section matching `name`, resolved with
// capture's widening tiers: exact top-level, exact any-level, substring
// top-level, substring any-level, all case-insensitive. It lets a move
// destination reach a nested project sub-section by name, not just a top-level
// heading.
func (s *Store) SectionNamed(name string) (record.Record, bool) {
	snapshot, err := s.ReadSnapshot(false)
	if err != nil {
		return record.Record{}, false
	}
	records := snapshot.LiveRecords()
	index := findSection(records, name)
	if index < 0 {
		return record.Record{}, false
	}
	return records[index], true
}

// RenameSection retitles a section in one checked transaction. It reports the
// section id, or false when the id names no section, the title is blank, or the
// write rolled back.
func (s *Store) RenameSection(id, title string) (string, bool) {
	title = rubyStrip(title)
	if title == "" {
		return "", false
	}
	renamed := ""
	ok := s.withHistory("rename section: "+title, func() bool {
		records := freshRecords(s.org)
		index := sectionIndex(records, id)
		if index < 0 {
			return false
		}
		records[index].SetString("title", title)
		if s.writeRecords(s.org, records) != nil {
			return false
		}
		renamed = id
		return true
	})
	if !ok {
		return "", false
	}
	return renamed, true
}

// CompleteProject closes every open descendant task of a section — DONE, today's
// closed date, the defer tag dropped, the recurrence cookie retired.
//
// Zero closed is a CLEAN result for a project that was already fully closed, and
// it is also what a rollback returns. Only the second records a rollback, which
// is the only way a caller can tell them apart.
func (s *Store) CompleteProject(id, today string) (int, bool) {
	closed := 0
	ok := s.withHistory("complete project: "+id, func() bool {
		records := freshRecords(s.org)
		index := sectionIndex(records, id)
		if index < 0 {
			return false
		}
		touched := closeOpenDescendants(records, index, today)
		if len(touched) == 0 {
			// A clean no-op: nothing written, so nothing to validate or journal.
			return true
		}
		if s.writeRecords(s.org, records) != nil {
			return false
		}
		closed = len(touched)
		return true
	})
	if !ok {
		return 0, false
	}
	return closed, true
}

// ArchiveProject moves a section's entire contiguous subtree to the archive,
// mirroring the sweep's serialization: the root section drops its parent and
// gains today's `archived` stamp.
//
// Open tasks do not block — that is caller policy — but an undecided proposal is
// never archival material, because a proposal archived without a decision is a
// decision nobody made.
//
// The archive is written FIRST. An interruption between the two writes can then
// only leave retry-safe duplicates across the files, never a lost subtree.
// `today` is the day the swept root is stamped with, exactly as the sweep takes
// it: the READER's day, resolved through their configured time zone. Ruby read
// `Date.today` here — the one date this store wrote that neither a configured
// zone nor a harness pin could reach — and that was fixed in lib/tasks/store.rb
// rather than reproduced.
func (s *Store) ArchiveProject(id, today string) ([]string, bool, bool) {
	var moved []string
	proposed := false
	ok := s.withHistory("archive project: "+id, func() bool {
		records := freshRecords(s.org)
		index := sectionIndex(records, id)
		if index < 0 {
			return false
		}
		end := subtreeEnd(records, index)
		span := record.CloneAll(records[index:end])
		for _, parsed := range span {
			if parsed.String("type") == "task" && contains(check.ProposedStates, parsed.String("state")) {
				proposed = true
				return false
			}
		}
		span[0].Delete("parent")
		span[0].SetString("archived", today)
		kept := append(append([]record.Record{}, records[:index]...), records[end:]...)

		archived := freshRecords(s.archive)
		if len(archived) == 0 {
			archived = []record.Record{metaRecord()}
		}
		state, _ := archiveRetryState(archived, span)
		if state == retryConflict {
			return false
		}
		if state == retryNew {
			if s.writeRecords(s.archive, append(archived, span...)) != nil {
				return false
			}
		}
		// The live records are removed only once the archive copy is on disk and
		// readable. Verifying that separately rather than trusting the write is
		// the difference between "probably persisted" and "these ids are there".
		persisted := map[string]bool{}
		for _, id := range idsOf(freshRecords(s.archive)) {
			persisted[id] = true
		}
		for _, id := range idsOf(span) {
			if !persisted[id] {
				return false
			}
		}
		if s.writeRecords(s.org, kept) != nil {
			return false
		}
		moved = idsOf(span)
		return true
	})
	if proposed {
		return nil, true, false
	}
	if !ok {
		return nil, false, false
	}
	return moved, false, true
}

// EnsureID gives a legacy record without a stable id one, and reports it. It is
// idempotent: a record that already has an id is returned untouched, with no
// write and no undo slot burned.
func (s *Store) EnsureID(line int, existing, title string) (string, bool) {
	if existing != "" {
		return existing, true
	}
	assigned := ""
	ok := s.withHistory("id: "+title, func() bool {
		records := freshRecords(s.org)
		index := -1
		for position, parsed := range records {
			if parsed.Line == line {
				index = position
				break
			}
		}
		if index < 0 {
			return false
		}
		if id := records[index].String("id"); id != "" {
			assigned = id
			return true
		}
		id := s.genID(append(idsOf(records), s.archivedIDs()...))
		records[index].SetString("id", id)
		if s.writeRecords(s.org, records) != nil {
			return false
		}
		assigned = id
		return true
	})
	if !ok {
		return "", false
	}
	return assigned, true
}

// sectionIndex locates a SECTION by stable id. It is deliberately not
// locateStableIndex: a task id passed where a section id belongs must miss, not
// silently retitle or archive a task.
func sectionIndex(records []record.Record, id string) int {
	if id == "" {
		return -1
	}
	for index, parsed := range records {
		if parsed.String("type") == "section" && parsed.String("id") == id {
			return index
		}
	}
	return -1
}
