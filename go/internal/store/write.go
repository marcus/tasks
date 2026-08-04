package store

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"tasks-go/internal/check"
	"tasks-go/internal/journal"
	"tasks-go/internal/lead"
	"tasks-go/internal/query"
	"tasks-go/internal/record"
	"tasks-go/internal/recur"
	"tasks-go/internal/temporal"
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

// CreateCommand is the typed create every capture goes through.
type CreateCommand struct {
	Title    string
	Priority string
	Tags     []string
	State    string
	Project  string
	ParentID string
	Notes    []string
	Deferred bool
	// Scheduled and Deadline are the two dates a capture may name, each
	// optionally carrying a wall time and a zone. HasScheduled / HasDeadline
	// distinguish "not supplied" from the zero date, which a bare
	// temporal.Value cannot.
	Scheduled    temporal.Value
	HasScheduled bool
	Deadline     temporal.Value
	HasDeadline  bool
	// Recurrence is a canonical cookie and Lead a canonical span. Both are
	// intents ABOUT a date, and both are validated here against the dates this
	// very create is about to write — a create that would need an immediate
	// repair is a create that should have been refused.
	Recurrence string
	Lead       string
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

		attributes, errors := normalizeCreate(command, today)
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
	title        string
	priority     string
	tags         []string
	state        string
	project      string
	parentID     string
	notes        []string
	scheduled    temporal.Value
	hasScheduled bool
	deadline     temporal.Value
	hasDeadline  bool
	recurrence   string
	lead         string
}

// normalizeCreate is Store#normalize_create_task. Every refusal here is one the
// file never sees.
func normalizeCreate(command CreateCommand, today string) (createAttributes, []string) {
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
	recurrence := command.Recurrence
	if recurrence != "" && !recur.Cookie(recurrence) {
		errors = append(errors, "invalid recurrence cookie")
		recurrence = ""
	}
	span := command.Lead
	if span != "" && !lead.Span(span) {
		errors = append(errors, "invalid lead time (expected a span like 3w, 2d, 1m, 1y)")
		span = ""
	}

	scheduled, hasScheduled := command.Scheduled, command.HasScheduled
	deadline, hasDeadline := command.Deadline, command.HasDeadline
	day, dayOK := temporal.ParseDate(today)

	// Capturing with a recurrence has always meant "start repeating now" when no
	// date was given. With a LEAD that reading is wrong: today's anchor puts the
	// window in the past, so the task appears immediately and the schedule's own
	// first occurrence is never used. A lead therefore seeds the FIRST
	// occurrence instead, which is what makes `--recur y:06-01 --lead 17d` mean
	// "invisible until May 15" with no further arguments.
	if recurrence != "" && !hasDeadline && !hasScheduled && dayOK {
		seed := day
		if span != "" {
			if first, ok := firstOccurrence(recurrence, day); ok {
				seed = first
			}
		}
		scheduled, hasScheduled = temporal.Value{Date: seed}, true
	}
	if state == "" {
		state = "INBOX"
		if hasScheduled || hasDeadline {
			state = "TODO"
		}
	}
	if recurrence != "" &&
		(contains(check.ClosedStates, state) || contains(check.ProposedStates, state)) {
		errors = append(errors, "can't set recurrence on a "+state+" task")
	}
	if recurrence != "" && dayOK {
		// Completion rolls the DEADLINE when both dates exist, so that is the
		// stamp the schedule has to be reachable from.
		anchor := scheduled.Date
		if hasDeadline {
			anchor = deadline.Date
		}
		if hasScheduled || hasDeadline {
			if reason := unreachableRecurrence(recurrence, anchor, day); reason != "" {
				errors = append(errors, reason)
			}
		}
	}
	if span != "" {
		errors = append(errors, createLeadErrors(span, scheduled, hasScheduled, deadline, hasDeadline)...)
	}

	return createAttributes{
		title: title, priority: command.Priority, tags: tags, state: state,
		project: command.Project, parentID: command.ParentID, notes: command.Notes,
		scheduled: scheduled, hasScheduled: hasScheduled,
		deadline: deadline, hasDeadline: hasDeadline,
		recurrence: recurrence, lead: span,
	}, errors
}

// createLeadErrors is the same three lead rules patch_lead enforces, stated
// against the values this create is about to write.
func createLeadErrors(span string, scheduled temporal.Value, hasScheduled bool,
	deadline temporal.Value, hasDeadline bool) []string {

	anchor, hasAnchor := leadAnchor(deadline.Date, hasDeadline, scheduled.Date, hasScheduled)
	if !hasAnchor {
		return []string{"a lead time needs a date to hide before — " +
			"add a deadline or an available-from date first"}
	}
	if hasDeadline && hasScheduled {
		return []string{leadGateConflictMessage(span)}
	}
	gate, ok := lead.DateBound(anchor, span)
	if !ok || !validISODate(gate.ISO()) {
		return []string{"a lead of " + humanizeLead(span) + " would open before " +
			anchor.ISO() + ", outside the four-digit years dates are stored with"}
	}
	return nil
}

// firstOccurrence is the date a schedule would first fire on, or a miss when it
// cannot be projected. The caller then falls back to today and the ordinary
// satisfiability guard reports the real problem.
func firstOccurrence(cookie string, today temporal.Date) (temporal.Date, bool) {
	from := recur.NewCivilDate(int64(today.Year), int(today.Month), today.Day)
	next, err := recur.NextDate(cookie, from, from)
	if err != nil {
		return temporal.Date{}, false
	}
	return temporal.ParseDate(next.String())
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
	if attributes.hasScheduled {
		writeTemporal(&fresh, "scheduled", attributes.scheduled)
	}
	if attributes.hasDeadline {
		writeTemporal(&fresh, "deadline", attributes.deadline)
	}
	if attributes.recurrence != "" {
		fresh.SetString("recur", attributes.recurrence)
	}
	if attributes.lead != "" {
		fresh.SetString("lead", attributes.lead)
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
// rather than silently taking a default path through the transaction. The
// vocabulary and the transaction both live in patch.go.
type PatchField string

// The two fields whose value is a plain word. The rest are in patch.go, with
// the value shapes they accept.
const (
	FieldPriority PatchField = "priority"
	FieldState    PatchField = "state"
)

type patchOutcome struct {
	status     MutationStatus
	errors     []string
	touchedIDs []string
	summary    MutationSummary
	// fieldErrors is the per-field breakdown a placement refusal carries, so a
	// caller learns WHICH of the two ids it supplied did not resolve rather than
	// having to guess from one sentence.
	fieldErrors map[string][]string
}

func patchPriority(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindNone && (value.kind != kindText || !contains(Priorities, value.text)) {
		return patchInvalid("priority must be A, B, C, or nil")
	}
	if value.kind == kindNone {
		records[index].Delete("priority")
	} else {
		records[index].SetString("priority", value.text)
	}
	return patchOK(records[index])
}

// patchState is the transition, and the effects a transition owns: a closed
// task loses its defer tag, gains a `closed` date, settles an unclaimed
// delegation, and — for DONE — cascades to its open descendants, because
// finishing a project finishes its work.
//
// Completing a RECURRING task is not a close at all: the cookie rolls the
// anchor forward and the task stays open. See advanceRecurrence.
func patchState(records []record.Record, index int, value PatchValue, context patchContext) patchOutcome {
	if value.kind != kindText || !contains(check.States, value.text) {
		return patchInvalid("invalid task state")
	}
	state := value.text
	target := records[index]
	from := target.String("state")
	today := context.today.ISO()

	if contains(check.ProposedStates, state) {
		if recur.Cookie(target.String("recur")) {
			return patchInvalid("remove recurrence before setting PROPOSED")
		}
		// Approval and delegation are independent owner decisions, and an
		// undecided proposal carries neither a claim nor an assignee.
		if delegationMarker(target) != nil {
			return patchInvalid("undelegate before setting PROPOSED")
		}
		if !contains(check.ProposedStates, from) {
			end := subtreeEnd(records, index)
			for position := index + 1; position < end; position++ {
				if records[position].String("type") == "task" &&
					!contains(check.ProposedStates, records[position].String("state")) {
					return patchInvalid("cannot set PROPOSED while accepted descendants remain")
				}
			}
		}
	}
	if contains(check.ProposedStates, from) && state == "DONE" {
		return patchInvalid("approve the proposal before completing it")
	}
	if !contains(check.ClosedStates, state) && !contains(check.ProposedStates, state) &&
		!context.allowProposedAncestor && proposedTaskAncestor(records, target) {
		return patchInvalid("accepted work cannot remain under a proposed task")
	}
	if state == "DONE" && recur.Cookie(target.String("recur")) {
		outcome := advanceRecurrence(records, index, context)
		if outcome.status == MutationOK {
			outcome.summary = MutationSummary{Action: "recurrence_advanced", TaskID: target.String("id")}
		}
		return outcome
	}

	records[index].SetString("state", state)
	touched := []string{records[index].String("id")}
	if contains(check.ClosedStates, state) && !contains(check.ClosedStates, from) {
		records[index].SetOptional("tags", record.RawStrings(withoutTag(semanticTags(records[index]), DeferTag)))
		records[index].SetDefault("closed", record.RawString(today))
		settleDelegationOnClose(&records[index])
		if state == "DONE" {
			touched = append(touched, closeOpenDescendants(records, index, today)...)
		}
	} else if contains(check.ClosedStates, from) && !contains(check.ClosedStates, state) {
		records[index].Delete("closed")
	}
	return patchOutcome{status: MutationOK, touchedIDs: touched}
}

// rollDelegationForward re-arms a marker onto the next occurrence: a human
// delegation keeps its assignee and goes back to awaiting the person, an agent
// delegation returns to the unclaimed pool, and both drop the work reference,
// because the work that reference names belongs to the occurrence just closed.
func rollDelegationForward(target *record.Record, context patchContext) {
	existing := delegationMarker(*target)
	if existing == nil {
		return
	}
	kind := markerKind(existing)
	if kind != "agent" && kind != "human" {
		// A marker of no recognized kind cannot describe intent to carry over;
		// a fresh occurrence is the right place to drop it.
		target.Delete(DelegationField)
		return
	}
	rolled := map[string]string{}
	for key, value := range existing {
		rolled[key] = value
	}
	if kind == "human" {
		rolled["status"] = DelegationDelegated
	} else {
		rolled["status"] = DelegationReady
		rolled["assignee"] = ""
	}
	rolled["at"] = context.stamp
	rolled["work_ref"] = ""
	target.Set(DelegationField, orderedDelegation(rolled, markerOrder(*target)))
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
