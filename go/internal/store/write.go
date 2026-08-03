package store

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"tasks-go/internal/check"
	"tasks-go/internal/journal"
	"tasks-go/internal/query"
	"tasks-go/internal/record"
)

// DeferTag is the semantic tag that means someday/maybe. Closing a task drops
// it, because a finished task is not deferred.
const DeferTag = "defer"

// Priorities is Check::PRIORITIES.
var Priorities = []string{"A", "B", "C"}

// Options carries everything a WRITING store needs and a reading one does not:
// where history goes, what time it is, who is writing, and how ids are minted.
//
// They are injected rather than read from the environment here for the reason
// lib/tasks/determinism.rb states: the store stays env-free, and the adapter
// boundary is the single place a harness pin turns into a value.
type Options struct {
	// JournalDir is the history directory for this store.
	JournalDir string
	// Now is the clock every stamp reads.
	Now func() time.Time
	// Device is the update stamp's tiebreaker half.
	Device string
	// IDSource mints task ids; nil means the collision-checked random mint.
	IDSource func() string
	// CoalesceScope is the journal's per-process coalescing scope.
	CoalesceScope string
	// MaxDepth is the nesting cap a create may not push a subtree past.
	MaxDepth int
	// UndoLimit is how many undo steps the journal retains.
	UndoLimit int
}

// NewWriter builds a store that can mutate. New stays the read-only
// constructor, so a surface that has no business writing cannot accidentally
// acquire the ability by construction.
func NewWriter(org, archive string, options Options) *Store {
	store := New(org, archive)
	store.options = options
	if store.options.UndoLimit <= 0 {
		store.options.UndoLimit = journal.Limit
	}
	if store.options.Now == nil {
		store.options.Now = time.Now
	}
	return store
}

func (s *Store) now() time.Time { return s.options.Now() }

func (s *Store) journal() *journal.Journal {
	return journal.Open(s.options.JournalDir, s.org).
		Writable(s.options.UndoLimit, s.options.CoalesceScope)
}

// FormatStamp is UpdateStamp.format: one sortable token so the timestamp and
// the device tiebreaker cannot drift apart.
func FormatStamp(instant time.Time, device string) string {
	if device == "" {
		device = "device"
	}
	return instant.UTC().Format("2006-01-02T15:04:05Z") + "#" + device
}

// genID is a short, unique, CLI-typeable id. Collisions are astronomically
// unlikely but cheap to exclude across BOTH files, so a fresh id can never
// clash with one already swept into the archive.
func (s *Store) genID(taken []string) string {
	excluded := map[string]bool{}
	for _, id := range taken {
		excluded[id] = true
	}
	for {
		id := s.mintID()
		if !excluded[id] {
			return id
		}
	}
}

// mintID draws from the injected sequence when there is one — that is the
// harness pin's only seam — and otherwise from four random bytes, which is
// SecureRandom.hex(4).
func (s *Store) mintID() string {
	if s.options.IDSource != nil {
		return s.options.IDSource()
	}
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		// A store that cannot mint an id must not mint a predictable one.
		panic("tasks: secure random unavailable for id minting: " + err.Error())
	}
	return hex.EncodeToString(buffer)
}

// -- capture -----------------------------------------------------------------

// CreateCommand is the typed create every capture goes through. Recurrence,
// lead time and temporal modifiers are deliberately absent: this build's CLI
// refuses those flags outright rather than accepting them and writing a record
// whose semantics it has not yet ported.
type CreateCommand struct {
	Title    string
	Priority string
	Tags     []string
	State    string
	Project  string
	ParentID string
	Notes    []string
	Deferred bool
}

// CreateTask creates one task from a complete typed command in one checked
// transaction: preflight, normalize, plan, serialize, write, validate, journal.
//
// Serialization happens BEFORE the file is replaced, so a value the emitter
// refuses is an invalid-command result rather than a partially installed record.
func (s *Store) CreateTask(command CreateCommand, today string) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		if reason, ok := s.createPreflightFailure(); !ok {
			result = MutationResult{Status: MutationStoreInvalid, Errors: []string{reason}}
			return nil
		}

		attributes, errors := normalizeCreate(command)
		if len(errors) > 0 {
			result = MutationResult{Status: MutationInvalid, Errors: errors}
			return nil
		}

		records := record.CloneAll(freshRecords(s.org))
		planned, refusal := s.planCreateTask(records, attributes, today)
		if refusal != nil {
			result = *refusal
			return nil
		}
		if _, err := record.Dump(planned.records); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, planned.records, "capture: "+attributes.title, "")
		if result.Status == MutationOK {
			result.TouchedIDs = []string{planned.id}
		}
		return nil
	})
	if err != nil {
		return MutationResult{Status: MutationUnavailable, Errors: []string{"task store unavailable"}}
	}
	return result
}

type createAttributes struct {
	title    string
	priority string
	tags     []string
	state    string
	project  string
	parentID string
	notes    []string
}

// normalizeCreate is Store#normalize_create_task for the fields this build
// writes. Every refusal here is one the file never sees.
func normalizeCreate(command CreateCommand) (createAttributes, []string) {
	errors := []string{}
	title := strings.TrimSpace(command.Title)
	if title == "" {
		errors = append(errors, "title cannot be blank")
	}
	if command.Priority != "" && !contains(Priorities, command.Priority) {
		errors = append(errors, "priority must be A, B, C, or nil")
	}
	tags := append([]string{}, command.Tags...)
	if command.Deferred && !contains(tags, DeferTag) {
		tags = append(tags, DeferTag)
	}
	state := command.State
	if state != "" && !contains(check.States, state) {
		errors = append(errors, "invalid task state")
	}
	if command.Project != "" && command.ParentID != "" {
		errors = append(errors, "project and parent_id cannot both be supplied")
	}
	if state == "" {
		// No date can reach this build's create, so the dated branch is
		// unreachable and INBOX is the whole of the default.
		state = "INBOX"
	}
	return createAttributes{
		title: title, priority: command.Priority, tags: tags, state: state,
		project: command.Project, parentID: command.ParentID, notes: command.Notes,
	}, errors
}

type createPlan struct {
	records  []record.Record
	id       string
	parentID string
}

// planCreateTask decides WHERE the new task lands and builds its record. The
// three destinations are the whole of the rule: an explicit parent, a
// bootstrapped first-run store, or the named (or default Inbox) section.
func (s *Store) planCreateTask(records []record.Record, attributes createAttributes, today string) (createPlan, *MutationResult) {
	var parentID string
	var insertAt int

	switch {
	case attributes.parentID != "":
		parentIndex := locateStableIndex(records, attributes.parentID)
		if parentIndex < 0 {
			return createPlan{}, &MutationResult{Status: MutationNotFound}
		}
		parentType := records[parentIndex].String("type")
		if parentType != "task" && parentType != "section" {
			return createPlan{}, &MutationResult{
				Status: MutationInvalid,
				Errors: []string{"parent_id must identify a task or section"},
			}
		}
		if parentType == "task" &&
			contains(check.ProposedStates, records[parentIndex].String("state")) &&
			!contains(check.ProposedStates, attributes.state) {
			return createPlan{}, &MutationResult{
				Status: MutationInvalid,
				Errors: []string{"accepted work cannot be created under a proposed task"},
			}
		}
		// A section parent files the task directly beneath the heading (depth
		// 1), so only a TASK parent can push past the nesting cap.
		if parentType == "task" {
			byID := recordsByID(records)
			if taskDepth(byID, records[parentIndex])+1 > s.options.MaxDepth {
				return createPlan{}, &MutationResult{
					Status: MutationTooDeep,
					Errors: []string{"would exceed max depth " + itoa(s.options.MaxDepth) +
						" (max_depth config / TASKS_MAX_DEPTH)"},
				}
			}
		}
		parentID = records[parentIndex].String("id")
		// Append at the end of the parent's subtree — after any existing task
		// and section children — which the DFS pre-order invariant keeps valid.
		insertAt = subtreeEnd(records, parentIndex)

	case len(records) == 0:
		section := attributes.project
		if section == "" {
			section = "Inbox"
		}
		meta := record.Record{Fields: []record.Field{
			{Key: "type", Value: record.RawString("meta")},
			{Key: "version", Value: record.RawInt(check.Version)},
		}}
		heading := record.Record{Fields: []record.Field{
			{Key: "type", Value: record.RawString("section")},
			{Key: "id", Value: record.RawString(s.genID(s.archivedIDs()))},
			{Key: "title", Value: record.RawString(section)},
		}}
		records = []record.Record{meta, heading}
		parentID = heading.String("id")
		insertAt = subtreeEnd(records, len(records)-1)

	default:
		wanted := attributes.project
		if wanted == "" {
			wanted = "Inbox"
		}
		sectionIndex := findSection(records, wanted)
		if sectionIndex < 0 {
			return createPlan{}, &MutationResult{
				Status: MutationInvalid, Errors: []string{"capture project does not exist"},
			}
		}
		parentID = records[sectionIndex].String("id")
		insertAt = subtreeEnd(records, sectionIndex)
	}

	id := s.genID(append(idsOf(records), s.archivedIDs()...))
	fresh := record.Record{Fields: []record.Field{
		{Key: "type", Value: record.RawString("task")},
		{Key: "id", Value: record.RawString(id)},
		{Key: "parent", Value: record.RawString(parentID)},
		{Key: "state", Value: record.RawString(attributes.state)},
		{Key: "title", Value: record.RawString(attributes.title)},
	}}
	if attributes.priority != "" {
		fresh.SetString("priority", attributes.priority)
	}
	if len(attributes.tags) > 0 {
		fresh.Set("tags", record.RawStrings(attributes.tags))
	}
	body := append([]string{"Captured [" + today + "]."}, attributes.notes...)
	fresh.SetString("body", strings.Join(body, "\n"))

	out := make([]record.Record, 0, len(records)+1)
	out = append(out, records[:insertAt]...)
	out = append(out, fresh)
	out = append(out, records[insertAt:]...)
	return createPlan{records: out, id: id, parentID: parentID}, nil
}

func (s *Store) archivedIDs() []string { return idsOf(freshRecords(s.archive)) }

// -- field patches ------------------------------------------------------------

// PatchField names the single-field patches this build implements. The set is
// deliberately closed: a field the port has not reached refuses at the CLI
// rather than silently taking a default path through the transaction.
type PatchField string

// The implemented fields.
const (
	FieldPriority PatchField = "priority"
	FieldState    PatchField = "state"
)

// PatchTask applies one field-owned semantic change in the same transaction
// shape a changeset uses.
//
// `expected` is the patch's narrow conflict check: the value the caller read
// before deciding. It is compared against the value under the write lock, so an
// edit that landed in between refuses instead of silently overwriting.
func (s *Store) PatchTask(id string, field PatchField, value string, expected string, label string, today string) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		preflight := check.Check(s.org)
		if !preflight.OK() {
			// A targeted repair is Ruby's route out of an invalid file for a
			// field-owned patch, and this build does not implement it: refusing
			// is the conservative half of that rule, never the permissive half.
			messages := []string{}
			for _, entry := range preflight.Errors {
				messages = append(messages, entry.Message)
			}
			result = MutationResult{Status: MutationStoreInvalid, Errors: messages}
			return nil
		}

		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}
		actual, err := expectedFor(records, index, field)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		if actual != expected {
			result = MutationResult{Status: MutationConflict}
			return nil
		}

		original, err := record.Dump(records)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		working := record.CloneAll(records)
		applied := applyFieldPatch(working, index, field, value, today)
		if applied.status != MutationOK {
			result = MutationResult{Status: applied.status, Errors: applied.errors}
			return nil
		}
		proposed, err := record.Dump(working)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		if proposed == original {
			snapshot, revision := s.readAfterWrite()
			result = MutationResult{Status: MutationNoChange, ReadSnapshot: snapshot, StoreRevision: revision}
			return nil
		}

		result = s.commit(before, working, label, "")
		if result.Status == MutationOK {
			result.TouchedIDs = applied.touchedIDs
		}
		return nil
	})
	if err != nil {
		return MutationResult{Status: MutationUnavailable, Errors: []string{"task store unavailable"}}
	}
	return result
}

// ExpectedFor is the baseline a caller reads before proposing a patch. It is
// exported because the conflict check is only meaningful when the SAME
// expression produces the value on both sides of the decision.
func (s *Store) ExpectedFor(id string, field PatchField) (string, bool) {
	var value string
	found := false
	_ = s.withLock(func() error {
		if !check.Check(s.org).OK() {
			return nil
		}
		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			return nil
		}
		expected, err := expectedFor(records, index, field)
		if err != nil {
			return nil
		}
		value, found = expected, true
		return nil
	})
	return value, found
}

// expectedFor is EditSnapshot#expected_for: a plain baseline for a field that
// owns only itself, and a FINGERPRINT for one whose change has wider effects.
// State is the fingerprint case — completing a task can cascade to descendants,
// so the baseline has to cover the lifecycle of the whole subtree, not one word.
func expectedFor(records []record.Record, index int, field PatchField) (string, error) {
	switch field {
	case FieldPriority:
		return records[index].String("priority"), nil
	case FieldState:
		return lifecycleFingerprint(records, index)
	}
	return "", nil
}

type patchOutcome struct {
	status     MutationStatus
	errors     []string
	touchedIDs []string
}

func applyFieldPatch(records []record.Record, index int, field PatchField, value, today string) patchOutcome {
	switch field {
	case FieldPriority:
		return patchPriority(records, index, value)
	case FieldState:
		return patchState(records, index, value, today)
	}
	return patchOutcome{status: MutationInvalid, errors: []string{"unknown field"}}
}

func patchPriority(records []record.Record, index int, value string) patchOutcome {
	if value != "" && !contains(Priorities, value) {
		return patchOutcome{status: MutationInvalid, errors: []string{"priority must be A, B, C, or nil"}}
	}
	if value == "" {
		records[index].Delete("priority")
	} else {
		records[index].SetString("priority", value)
	}
	return patchOutcome{status: MutationOK, touchedIDs: []string{records[index].String("id")}}
}

// patchState is the transition, and the effects a transition owns: a closed
// task loses its defer tag, gains a `closed` date, settles an unclaimed
// delegation, and — for DONE — cascades to its open descendants, because
// finishing a project finishes its work.
//
// Recurrence advance is NOT implemented here, and a recurring task is refused
// rather than closed: rolling the date forward is a different product outcome,
// and writing the wrong one is worse than declining.
func patchState(records []record.Record, index int, value, today string) patchOutcome {
	if !contains(check.States, value) {
		return patchOutcome{status: MutationInvalid, errors: []string{"invalid task state"}}
	}
	target := records[index]
	from := target.String("state")

	if contains(check.ProposedStates, value) && target.String("recur") != "" {
		return patchOutcome{status: MutationInvalid, errors: []string{"remove recurrence before setting PROPOSED"}}
	}
	if contains(check.ProposedStates, from) && value == "DONE" {
		return patchOutcome{status: MutationInvalid, errors: []string{"approve the proposal before completing it"}}
	}
	if !contains(check.ClosedStates, value) && !contains(check.ProposedStates, value) &&
		proposedTaskAncestor(records, target) {
		return patchOutcome{status: MutationInvalid, errors: []string{"accepted work cannot remain under a proposed task"}}
	}
	if value == "DONE" && target.String("recur") != "" {
		return patchOutcome{
			status: MutationInvalid,
			errors: []string{"completing a recurring task is not implemented in the Go port — " +
				"the date roll is a separate behavior and this build will not guess it"},
		}
	}

	records[index].SetString("state", value)
	touched := []string{records[index].String("id")}
	if contains(check.ClosedStates, value) && !contains(check.ClosedStates, from) {
		records[index].SetOptional("tags", record.RawStrings(withoutTag(semanticTags(records[index]), DeferTag)))
		records[index].SetDefault("closed", record.RawString(today))
		settleDelegationOnClose(&records[index])
		if value == "DONE" {
			touched = append(touched, closeOpenDescendants(records, index, today)...)
		}
	} else if contains(check.ClosedStates, from) && !contains(check.ClosedStates, value) {
		records[index].Delete("closed")
	}
	return patchOutcome{status: MutationOK, touchedIDs: touched}
}

func proposedTaskAncestor(records []record.Record, target record.Record) bool {
	byID := recordsByID(records)
	current := target
	for {
		parent, ok := byID[current.String("parent")]
		if !ok {
			return false
		}
		if parent.String("type") == "task" && contains(check.ProposedStates, parent.String("state")) {
			return true
		}
		current = parent
	}
}

// closeOpenDescendants closes every OPEN task inside the subtree, excluding the
// root. A cascaded recurring descendant is NOT advanced — its cookie is retired
// outright, because completing the parent completes it. Closed descendants are
// left untouched: their prior `closed` date stands.
func closeOpenDescendants(records []record.Record, index int, today string) []string {
	end := subtreeEnd(records, index)
	touched := []string{}
	for position := index + 1; position < end; position++ {
		if records[position].String("type") != "task" ||
			!contains(check.OpenStates, records[position].String("state")) {
			continue
		}
		records[position].SetString("state", "DONE")
		records[position].SetString("closed", today)
		records[position].SetOptional("tags", record.RawStrings(withoutTag(semanticTags(records[position]), DeferTag)))
		records[position].Delete("recur")
		settleDelegationOnClose(&records[position])
		touched = append(touched, records[position].String("id"))
	}
	return touched
}

func withoutTag(tags []string, unwanted string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag != unwanted {
			out = append(out, tag)
		}
	}
	return out
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsText(haystack, needle string) bool { return strings.Contains(haystack, needle) }
func downcase(value string) string              { return query.Downcase(value) }
func trimSpace(value string) string             { return strings.TrimSpace(value) }

func itoa(value int) string { return strconv.Itoa(value) }
