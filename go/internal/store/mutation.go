package store

import (
	"os"
	"sort"

	"tasks-go/internal/atomic"
	"tasks-go/internal/check"
	"tasks-go/internal/journal"
	"tasks-go/internal/record"
)

// MutationStatus is MutationResult::STATUSES. Refusals are values rather than
// errors so an adapter can map the same outcome consistently for a CLI, a TUI,
// or a transport that does not exist yet.
type MutationStatus string

// The statuses, spelled exactly as lib/tasks/patch_result.rb spells them.
const (
	MutationOK                MutationStatus = "ok"
	MutationNoChange          MutationStatus = "no_change"
	MutationNotFound          MutationStatus = "not_found"
	MutationStale             MutationStatus = "stale"
	MutationInvalid           MutationStatus = "invalid"
	MutationConflict          MutationStatus = "conflict"
	MutationCycle             MutationStatus = "cycle"
	MutationTooDeep           MutationStatus = "too_deep"
	MutationUnsupportedSchema MutationStatus = "unsupported_schema"
	MutationStoreInvalid      MutationStatus = "store_invalid"
	MutationUnavailable       MutationStatus = "unavailable"
)

// RollbackStage names which half of a mutation failed and forced the rollback.
// `write` means the atomic replace itself raised and validation never ran;
// `validation` means the bytes landed and the post-write check refused them.
// They are indistinguishable in `rolled_back` alone, and naming the wrong one
// misattributes the failure in the only diagnostic the user gets.
type RollbackStage string

// The two stages.
const (
	RollbackWrite      RollbackStage = "write"
	RollbackValidation RollbackStage = "validation"
)

// cliExitCodes is MutationResult::CLI_EXIT_CODES. `not_found` is 2 for the same
// reason a bad ref is: the caller should refine and retry, not abort.
var cliExitCodes = map[MutationStatus]int{
	MutationOK: 0, MutationNoChange: 0, MutationNotFound: 2,
	MutationStale: 1, MutationInvalid: 1, MutationConflict: 1, MutationCycle: 1,
	MutationTooDeep: 1, MutationUnsupportedSchema: 1, MutationStoreInvalid: 1,
	MutationUnavailable: 1,
}

// MutationResult is the immutable outcome every write path returns.
type MutationResult struct {
	Status MutationStatus
	Errors []string
	// FieldErrors is the per-field breakdown of a refusal, when the refusal has
	// one. A placement that names a missing anchor says so under `before_id`,
	// which is what lets a caller correct the right argument.
	FieldErrors   map[string][]string
	TouchedIDs    []string
	ReadSnapshot  *Snapshot
	StoreRevision string
	// RolledBack distinguishes a failed mutation that WROTE and restored the
	// previous bytes from a preflight refusal that never wrote. The two leave
	// byte-identical files behind and exit the same way, so the filesystem
	// cannot tell them apart and the boolean is the only thing that can.
	RolledBack    bool
	RollbackStage RollbackStage
	// Summary carries the per-operation facts an adapter renders: the
	// delegation holder for a conflict, the action for the error envelope.
	Summary MutationSummary
}

// MutationSummary is the union of the summary fields the CLI reads. Ruby
// returns an untyped Hash per operation; the fields any surface here consumes
// are few and naming them is what keeps a typo from silently reading nil.
type MutationSummary struct {
	Action string
	Holder string
	At     string
	TaskID string
	// From and To are a state transition's endpoints, which a proposal decision
	// reports and the CLI prints. A move reuses them for the old and the new
	// parent id, which is the same question asked of a different axis.
	From string
	To   string
	// Before is the anchor an anchored move placed the subtree in front of, and
	// CurrentParentID is the anchor's ACTUAL parent when that anchor turned out
	// not to be a child of the destination. The second is the whole content of a
	// placement conflict: the caller decided against a sibling list that has
	// since changed, and needs to know what it changed to.
	Before          string
	CurrentParentID string
	// CreatedID, RootID and CreatedRoot are a project create's report. The
	// bootstrapped root is called out separately because it is a SECOND section
	// the caller never asked for, and a caller that renders "created X" without
	// knowing a "Projects" heading also appeared has described half the write.
	CreatedID   string
	RootID      string
	CreatedRoot bool
	// MovedIDs is every task id the move relocated, root first. An empty (but
	// non-nil) slice is a real answer: the task was already where it was asked
	// to be, so nothing moved and no undo slot was burned.
	MovedIDs []string
	// Removed, Descendants and OpenDescendants are the delete's counts. They
	// are what the cascade refusal quotes back — "3 descendants (2 open)" — so
	// the caller learns what --cascade would actually remove.
	Removed         int
	Descendants     int
	OpenDescendants int
	// ProposedDescendantIDs is the leaves-first refusal: the undecided
	// proposals under this one that must be decided before it can be.
	ProposedDescendantIDs []string
	// Previous is the delegation marker this write REPLACED, as stored JSON,
	// captured inside the transaction. A caller that read it before the write
	// instead would be racing: whether an inherited WAITING is cleared depends
	// on what the marker was at the moment the marker changed, not a moment
	// earlier.
	Previous string
}

// OK is MutationResult#ok?: a no-op that succeeded is a success.
func (r MutationResult) OK() bool { return r.Status == MutationOK || r.Status == MutationNoChange }

// Changed reports whether the mutation actually wrote.
func (r MutationResult) Changed() bool { return r.Status == MutationOK }

// ExitCode is the CLI status this outcome exits with.
func (r MutationResult) ExitCode() int {
	if code, ok := cliExitCodes[r.Status]; ok {
		return code
	}
	return 1
}

// FirstError is the message a refusal quotes, or "" when it carries none.
func (r MutationResult) FirstError() string {
	if len(r.Errors) == 0 {
		return ""
	}
	return r.Errors[0]
}

// -- write plumbing ----------------------------------------------------------

// fileSnapshot is the bytes of both files at one moment, the shape the journal
// records and a rollback restores. A nil half means the file is absent, which
// is a real state: an archive that does not exist yet is not an empty one.
func (s *Store) fileSnapshot() journal.Snapshot { return s.FileSnapshot() }

// restore puts both files back to a recorded snapshot.
//
// Undo and redo can replay an archive sweep, so restore has the same ordering
// obligation as the forward operation: install the destination copy before
// removing the source copy — archive first for live→archive, live first for
// archive→live. Any other history entry keeps live-first.
func (s *Store) restore(target journal.Snapshot) error {
	current := s.fileSnapshot()
	order := []struct {
		path    string
		content *string
		now     *string
	}{
		{s.org, target.Org, current.Org},
		{s.archive, target.Archive, current.Archive},
	}
	if restoreArchiveFirst(current, target) {
		order[0], order[1] = order[1], order[0]
	}
	for _, step := range order {
		if equalOptionalText(step.now, step.content) {
			continue
		}
		if err := restoreFile(step.path, step.content); err != nil {
			return err
		}
	}
	return nil
}

// removeFile is os.Remove behind one name, so a fault-injection test can deny
// the DELETE half of a restore the way test/test_journal.rb stubs File.delete.
// Deleting the archive is what an undo of a sweep does, and a delete that fails
// while the live write succeeded is exactly the split state the rollback exists
// to prevent — it cannot be tested without a seam.
var removeFile = os.Remove

func restoreFile(path string, content *string) error {
	if content == nil {
		if _, err := os.Lstat(path); err == nil {
			return removeFile(path)
		}
		return nil
	}
	return atomic.Write(path, *content)
}

func restoreArchiveFirst(current, target journal.Snapshot) bool {
	currentLive := snapshotIDs(current.Org)
	targetLive := snapshotIDs(target.Org)
	targetArchive := snapshotIDs(target.Archive)
	for id := range currentLive {
		if !targetLive[id] && targetArchive[id] {
			return true
		}
	}
	return false
}

func snapshotIDs(content *string) map[string]bool {
	ids := map[string]bool{}
	if content == nil {
		return ids
	}
	for _, parsed := range record.Parse([]byte(*content)).Records {
		if id := parsed.String("id"); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func equalOptionalText(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// writeRecords stamps the tasks whose SEMANTICS changed and replaces the file
// atomically. Stamping is what makes the last-write-wins merge work, so it has
// to happen here rather than at each call site: a mutation that forgot would
// hand an older device the win.
func (s *Store) writeRecords(path string, records []record.Record) error {
	s.stampChangedTasks(freshRecords(path), records)
	return s.writeRecordsUnstamped(path, records)
}

// writeRecordsUnstamped is Ruby's `write_records(..., stamp: false)`: the same
// atomic replacement with no update stamp at all.
//
// Repair is its only caller, and the reason is the whole of the decision. A
// repair asserts nothing about a task's CONTENT — it converges bytes the store
// refuses to write over — so stamping would falsify "when this task last
// changed" and, in the last-write-wins merge, hand the repairing device a win it
// did not earn. For a just-minted id it is worse: stampChangedTasks indexes the
// originals by id, a fresh id is in no index, and the repair would be stamped as
// a brand-new task.
func (s *Store) writeRecordsUnstamped(path string, records []record.Record) error {
	text, err := record.Dump(records)
	if err != nil {
		return err
	}
	return atomic.Write(path, text)
}

// stampChangedTasks gives every task whose semantics differ from the file's
// copy ONE shared `updated` stamp, and gives every task whose semantics did not
// change back exactly the stamp it already had.
//
// The second half matters as much as the first. Re-stamping an untouched record
// would claim a write that did not happen; dropping the stamp from a record
// that had one would lose the merge tiebreaker.
func (s *Store) stampChangedTasks(original, proposed []record.Record) {
	originalByID := map[string]record.Record{}
	for _, parsed := range original {
		if id := parsed.String("id"); id != "" {
			if _, seen := originalByID[id]; !seen {
				originalByID[id] = parsed
			}
		}
	}

	changed := map[string]bool{}
	for _, parsed := range proposed {
		if parsed.String("type") != "task" {
			continue
		}
		id := parsed.String("id")
		previous, existed := originalByID[id]
		if !existed || stampSemantics(previous) != stampSemantics(parsed) {
			changed[id] = true
		}
	}
	if len(changed) == 0 {
		return
	}
	stamp := FormatStamp(s.now(), s.options.Device)
	for index := range proposed {
		if proposed[index].String("type") != "task" {
			continue
		}
		id := proposed[index].String("id")
		if changed[id] {
			proposed[index].SetString("updated", stamp)
			continue
		}
		previous, existed := originalByID[id]
		if !existed {
			continue
		}
		if value, present := previous.Get("updated"); present {
			proposed[index].Set("updated", value)
		} else {
			proposed[index].Delete("updated")
		}
	}
}

// stampSemantics is the record minus the two fields a stamp must not depend on:
// `line` is physical bookkeeping and `updated` is the stamp itself.
func stampSemantics(parsed record.Record) string {
	stripped := record.Clone(parsed)
	stripped.Delete("updated")
	stripped.Delete(record.LineKey)
	text, err := record.DumpRecord(stripped)
	if err != nil {
		return "\x00unrepresentable"
	}
	return text
}

// freshRecords parses a file from disk, ignoring parse errors: the caller has
// already gated on validity where validity matters.
func freshRecords(path string) []record.Record {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []record.Record{}
	}
	return record.Parse(raw).Records
}

// postWriteFailure is the first check error if the live file — or the archive,
// when it exists, since a sweep writes that too — fails validation after a
// write, and "" when both are clean. It drives the rollback and the CLI's
// "run `tasks check`" hint.
func (s *Store) postWriteFailure() string {
	paths := []string{s.org}
	if _, err := os.Stat(s.archive); err == nil {
		paths = append(paths, s.archive)
	}
	for _, path := range paths {
		result := check.Check(path)
		if result.OK() {
			continue
		}
		if len(result.Errors) > 0 {
			return result.Errors[0].Message
		}
		return "validation failed"
	}
	return ""
}

// unsupportedSchemaRefusal is the shared refusal every mutation returns for a
// store this binary cannot read. It writes nothing and offers no conversion: a
// store at another schema version needs the matching binary, not a rewrite by
// this one.
func (s *Store) unsupportedSchemaRefusal() *MutationResult {
	message := s.UnsupportedSchemaError()
	if message == "" {
		return nil
	}
	return &MutationResult{Status: MutationUnsupportedSchema, Errors: []string{message}}
}

// commit is the tail every mutation shares once it has a proposed record set:
// write, validate, roll back on failure, journal, and re-read.
//
// It is one function because the ORDER is the contract. The post-write check
// runs before the journal entry, so a rolled-back write leaves no history step
// pointing at bytes that never survived; the journal runs before the re-read,
// so the snapshot a caller renders is the one the history recorded.
func (s *Store) commit(before journal.Snapshot, records []record.Record, label, coalesceKey string) MutationResult {
	return s.commitRepair(before, records, label, coalesceKey, false)
}

// commitRepair is commit with the journal's repair flag, which a targeted
// repair sets so history can tell a converge-the-file write apart from an
// ordinary edit.
func (s *Store) commitRepair(before journal.Snapshot, records []record.Record, label, coalesceKey string,
	repair bool) MutationResult {

	s.clearRollback()
	if err := s.writeRecords(s.org, records); err != nil {
		// The write itself raised. Validation never ran, so the rollback is
		// staged `write` and the diagnostic must not send the user to `check`.
		s.recordRollback(err.Error(), RollbackWrite)
		_ = s.restore(before)
		return MutationResult{
			Status: MutationUnavailable, Errors: []string{err.Error()},
			RolledBack: true, RollbackStage: RollbackWrite,
		}
	}
	if reason := s.postWriteFailure(); reason != "" {
		s.recordRollback(reason, RollbackValidation)
		_ = s.restore(before)
		return MutationResult{
			Status: MutationStoreInvalid, Errors: []string{reason},
			RolledBack: true, RollbackStage: RollbackValidation,
		}
	}
	after := s.fileSnapshot()
	s.journal().Record(label, before, after, coalesceKey, repair)
	snapshot, revision := s.readAfterWrite()
	return MutationResult{
		Status: MutationOK, ReadSnapshot: snapshot, StoreRevision: revision,
	}
}

// readAfterWrite is Store#reload!: the snapshot a caller renders the mutation
// from, taken under the SAME lock as the write so no other process can slip a
// change between the two.
func (s *Store) readAfterWrite() (*Snapshot, string) {
	live, err := captureReadSource(s.org, false, false)
	if err != nil {
		return nil, ""
	}
	// reload! defaults to include_archive: false, and a mutation report only
	// ever names live tasks.
	snapshot, err := buildReadSnapshot(live, emptyReadSource(false), false)
	if err != nil {
		return nil, ""
	}
	archive, err := captureReadSource(s.archive, true, false)
	if err != nil {
		return snapshot, ""
	}
	return snapshot, StoreRevisionForContents(live.raw, archive.raw)
}

// -- record helpers shared by the plans ---------------------------------------

// locateStableIndex finds a record by its stable id.
func locateStableIndex(records []record.Record, id string) int {
	if id == "" {
		return -1
	}
	for index, parsed := range records {
		if parsed.String("id") == id {
			return index
		}
	}
	return -1
}

// idsOf is every id present in a record set.
func idsOf(records []record.Record) []string {
	ids := []string{}
	for _, parsed := range records {
		if id := parsed.String("id"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// findSection is Store#find_section: an exact top-level title, then an exact
// title anywhere, then a substring at top level, then a substring anywhere. The
// order is a precedence, not a search heuristic — "Inbox" must never match
// "Inbox archive" while a real "Inbox" exists.
func findSection(records []record.Record, name string) int {
	want := downcase(trimSpace(name))
	sections := []int{}
	top := []int{}
	for index, parsed := range records {
		if parsed.String("type") != "section" {
			continue
		}
		sections = append(sections, index)
		if !parsed.Has("parent") || parsed.String("parent") == "" {
			top = append(top, index)
		}
	}
	exact := func(pool []int) int {
		for _, index := range pool {
			if downcase(records[index].String("title")) == want {
				return index
			}
		}
		return -1
	}
	substring := func(pool []int) int {
		for _, index := range pool {
			if containsText(downcase(records[index].String("title")), want) {
				return index
			}
		}
		return -1
	}
	for _, candidate := range []int{exact(top), exact(sections), substring(top), substring(sections)} {
		if candidate >= 0 {
			return candidate
		}
	}
	return -1
}

// taskDepth counts the TASK records on a record's parent chain, itself
// included. A task filed directly under a section is depth 1; sections do not
// count. It drives the nesting cap.
func taskDepth(byID map[string]record.Record, parsed record.Record) int {
	depth := 0
	current, ok := parsed, true
	for ok {
		if current.String("type") == "task" {
			depth++
		}
		parent := current.String("parent")
		if parent == "" {
			return depth
		}
		current, ok = byID[parent]
	}
	return depth
}

func recordsByID(records []record.Record) map[string]record.Record {
	byID := map[string]record.Record{}
	for _, parsed := range records {
		if id := parsed.String("id"); id != "" {
			byID[id] = parsed
		}
	}
	return byID
}

// sortedIDs keeps the collision set deterministic, which matters only for the
// diagnostics a test reads — the mint itself is order-independent.
func sortedIDs(ids []string) []string {
	out := append([]string{}, ids...)
	sort.Strings(out)
	return out
}

// -- the rollback record -------------------------------------------------------

// LastRollback is the reason and the stage of the most recent mutation that
// WROTE a file and then restored it, or ("", "") when the last mutation was
// clean.
//
// It exists for the operations that report failure through a bare boolean or
// count — the project lifecycle calls — where the recorded rollback is the only
// evidence that anything was written at all. Set and cleared together through
// the single writer below, so the pair can never drift apart.
func (s *Store) LastRollback() (string, RollbackStage) {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	return s.lastRollback, s.lastRollbackStage
}

func (s *Store) clearRollback() {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	s.lastRollback, s.lastRollbackStage = "", ""
}

func (s *Store) recordRollback(reason string, stage RollbackStage) {
	s.rollbackMu.Lock()
	defer s.rollbackMu.Unlock()
	s.lastRollback, s.lastRollbackStage = reason, stage
}
