package store

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// The delegation vocabulary, spelled as lib/tasks/delegation.rb spells it.
const (
	DelegationField     = record.DelegationField
	DelegationDelegated = "delegated" // human, awaiting the person
	DelegationReady     = "ready"     // agent pool, unclaimed
	DelegationClaimed   = "claimed"   // agent pool, held by one worker
	// AssigneeLimit bounds both an assignee address and a worker id.
	AssigneeLimit = 200
)

// DelegationKinds is the closed kind vocabulary a refusal quotes back.
var DelegationKinds = []string{"human", "agent"}

// modes is the delegation mode vocabulary THIS store validates against: the
// value it was constructed with, or the built-in set. The store holds no list
// of its own and reads no configuration; a configured vocabulary arrives as
// Options.Modes, which is one field rather than process-wide state.
func (s *Store) Modes() record.ModeVocabulary { return record.Modes(s.options.Modes) }

// checkOptions passes the store's own decisions down to the checker, so a
// post-write validation cannot refuse a marker the write path just accepted.
func (s *Store) checkOptions() check.Options { return check.Options{Modes: s.options.Modes} }

// delegationPlan is what a plan function decides: whether to write, what the
// history entry is called, and the facts a refusal reports.
type delegationPlan struct {
	status   MutationStatus
	errors   []string
	label    string
	noChange bool
	// holder and at describe the delegation that BLOCKED this one. They are the
	// whole content of a conflict: a caller retrying blindly needs to know who
	// holds the work and since when.
	holder string
	at     string
	// previous is the marker this plan replaced, as stored JSON. Captured by
	// the plan, which is the only place that sees the record before and after.
	previous string
}

// delegationMutation is the shared transaction every delegation verb runs in.
// It mirrors the patch transaction with one addition that matters: the marker
// is shape-checked BEFORE the write.
//
// That ordering is deliberate. The post-write check would catch a malformed
// marker too, but only by rolling the whole file back and reporting a
// validation failure — a far worse diagnostic than the shape error itself.
// DelegationRequest is one delegation write of any verb, with everything the
// verb can carry.
//
// It exists because the delegation surface grew two things the six positional
// signatures below could not express without another parameter each: the
// three-part marker's note, and the optimistic-concurrency token an HTTP
// If-Match carries. One request object keeps the store's delegation entry point
// singular, so a surface that grows a field does not fan out into six new
// method spellings.
type DelegationRequest struct {
	ID   string
	Verb DelegationVerb

	// Kind, Mode and Assignee are the delegate verb's inputs.
	Kind     string
	Mode     string
	Assignee string

	// Note is the receiver-facing briefing on delegate and delegation_note.
	// SetNote distinguishes "leave the existing note alone" (false) from "write
	// this note, clearing it when empty" (true) — a delegate that did not say
	// anything about the note must not silently erase one.
	Note    string
	SetNote bool

	// Worker is the claiming or releasing worker id; on work_ref it is the
	// worker proving its claim, and empty means the owner.
	Worker string
	// WorkRef is the reference to record; empty clears it.
	WorkRef string
	// Force is the owner override on release.
	Force bool

	// ExpectedRevision is the task revision the caller read before deciding. An
	// empty value skips the check; a value that no longer matches refuses stale
	// rather than overwriting a decision made against different facts.
	ExpectedRevision string

	// CoalesceKey merges a composed follow-up write into this write's undo step.
	// Only delegate and release compose one; the other verbs ignore it, and the
	// dispatcher below is where that is enforced rather than at each call site.
	CoalesceKey string
}

// DelegationVerb is the closed set of delegation writes the store performs.
type DelegationVerb string

// The six delegation writes.
const (
	VerbDelegate       DelegationVerb = "delegate"
	VerbUndelegate     DelegationVerb = "undelegate"
	VerbClaim          DelegationVerb = "claim"
	VerbRelease        DelegationVerb = "release"
	VerbWorkRef        DelegationVerb = "work_ref"
	VerbDelegationNote DelegationVerb = "delegation_note"
)

// WriteDelegation performs one delegation verb. Every method below is a thin
// spelling of this call, and every new input reaches the store through it.
func (s *Store) WriteDelegation(request DelegationRequest) MutationResult {
	// Only delegate and release compose a second write that has to share an undo
	// step. Honouring a key on the others would merge, say, a revocation into
	// whatever delegation write happened to precede it, and one undo would then
	// revert two decisions.
	coalesceKey := ""
	if request.Verb == VerbDelegate || request.Verb == VerbRelease {
		coalesceKey = request.CoalesceKey
	}
	var plan func(*record.Record) delegationPlan
	switch request.Verb {
	case VerbDelegate:
		plan = func(target *record.Record) delegationPlan { return s.planDelegate(target, request) }
	case VerbUndelegate:
		plan = planUndelegate
	case VerbClaim:
		plan = func(target *record.Record) delegationPlan { return s.planClaim(target, request.Worker) }
	case VerbRelease:
		plan = func(target *record.Record) delegationPlan {
			return s.planRelease(target, request.Worker, request.Force)
		}
	case VerbWorkRef:
		plan = func(target *record.Record) delegationPlan {
			return planWorkRef(target, request.WorkRef, request.Worker)
		}
	case VerbDelegationNote:
		plan = func(target *record.Record) delegationPlan {
			return s.planDelegationNote(target, request.Note)
		}
	default:
		return MutationResult{Status: MutationInvalid,
			Errors: []string{"unknown delegation verb " + rubyInspectText(string(request.Verb))}}
	}
	result := s.delegationMutation(request.ID, coalesceKey, request.ExpectedRevision, plan)
	result.Summary.Action = string(request.Verb)
	result.Summary.TaskID = request.ID
	return result
}

func (s *Store) delegationMutation(id, coalesceKey, expectedRevision string,
	plan func(*record.Record) delegationPlan) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		preflight := check.CheckWith(s.org, s.checkOptions())
		if !preflight.OK() {
			messages := []string{}
			for _, entry := range preflight.Errors {
				messages = append(messages, entry.Message)
			}
			result = MutationResult{Status: MutationStoreInvalid, Errors: messages}
			return nil
		}
		if id == "" {
			result = MutationResult{Status: MutationInvalid, Errors: []string{"task id is required"}}
			return nil
		}

		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}

		// The precondition is checked HERE, inside the same lock the write runs
		// in, and before the plan decides anything. That ordering is the whole
		// value of the guard: a caller that read the task, decided, and only then
		// sent the write must be refused if the task moved underneath, and it must
		// be refused before an eligibility or conflict verdict computed against
		// facts it never saw. A delegation marker is part of a task's OWN
		// revision component, so that component alone is what has to match —
		// an unrelated sibling capture must not refuse a delegation.
		if expectedRevision != "" {
			if status, message := delegationRevisionError(records, index, expectedRevision); status != "" {
				errors := []string(nil)
				if message != "" {
					errors = []string{message}
				}
				result = MutationResult{Status: status, Errors: errors}
				return nil
			}
		}

		working := record.CloneAll(records)
		previousMarker := ""
		if marker, present := working[index].Get(DelegationField); present {
			previousMarker = string(marker)
		}
		planned := plan(&working[index])
		if planned.previous == "" {
			planned.previous = previousMarker
		}
		if planned.status != MutationOK {
			result = MutationResult{
				Status: planned.status, Errors: planned.errors,
				Summary: MutationSummary{
					Holder: planned.holder, At: planned.at, Previous: planned.previous},
			}
			return nil
		}
		if planned.noChange {
			result = MutationResult{Status: MutationNoChange,
				Summary: MutationSummary{Previous: planned.previous}}
			return nil
		}
		if marker, present := working[index].Get(DelegationField); present {
			if shape := record.DelegationErrorsWith(decodeAny(marker), s.Modes()); len(shape) > 0 {
				result = MutationResult{Status: MutationInvalid, Errors: shape}
				return nil
			}
		}
		if _, err := record.Dump(working); err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}

		result = s.commit(before, working, planned.label, coalesceKey)
		if result.Status == MutationOK {
			result.TouchedIDs = []string{id}
		}
		result.Summary.Previous = planned.previous
		return nil
	})
	if err != nil {
		return mutationUnavailable(err)
	}
	return result
}

// delegationRevisionError compares the caller's expected task revision against
// the live one. It returns "" when the write may proceed.
func delegationRevisionError(records []record.Record, index int, expected string) (MutationStatus, string) {
	want, ok := revisionParts(expected)
	if !ok {
		return MutationInvalid, "malformed expected_revision"
	}
	current, err := taskRevision(records, index, siblingIDsByParent(records))
	if err != nil {
		return MutationInvalid, err.Error()
	}
	have, ok := revisionParts(current)
	if !ok {
		return MutationInvalid, "malformed expected_revision"
	}
	// Component 0 is the task's own semantic value, which is where the
	// delegation marker is digested.
	if have[0] != want[0] {
		return MutationStale, ""
	}
	return "", ""
}

// Delegate hands a task to the agent pool or to a named person, leaving any
// existing note alone. WriteDelegation is the spelling that can also write one.
func (s *Store) Delegate(id, kind, mode, assignee, coalesceKey string) MutationResult {
	return s.WriteDelegation(DelegationRequest{
		ID: id, Verb: VerbDelegate, Kind: kind, Mode: mode, Assignee: assignee,
		CoalesceKey: coalesceKey,
	})
}

// Claim takes an agent-pool task for one worker.
func (s *Store) Claim(id, worker, coalesceKey string) MutationResult {
	return s.WriteDelegation(DelegationRequest{ID: id, Verb: VerbClaim, Worker: worker})
}

// Undelegate clears the marker: an ordinary undelegate, or the revocation of a
// live claim. Revocation wins — afterwards the stale worker's release and
// work-ref calls fail their worker match. It is allowed whatever the marker's
// status and whatever the task's state, because clearing provenance from a
// closed task is an owner's prerogative; an undelegated task is no_change.
//
// The coalesce key is accepted and DELIBERATELY IGNORED. Ruby's
// `undelegate_task!` takes none — only `delegate` and `release` compose a second
// write that has to share an undo step — so honouring one here would merge a
// revocation into whatever unrelated delegation write happened to precede it,
// and one undo would then revert two decisions. The parameter stays in the
// signature because the application layer's optional interface declares it.
func (s *Store) Undelegate(id, _ string) MutationResult {
	return s.WriteDelegation(DelegationRequest{ID: id, Verb: VerbUndelegate})
}

// Release hands a claim back to the ready queue: claimed → ready, dropping the
// assignee. A worker must supply the id that matches the live claim; the owner
// passes force (with no worker id) to clear a stale claim without undelegating.
func (s *Store) Release(id, worker string, force bool, coalesceKey string) MutationResult {
	return s.WriteDelegation(DelegationRequest{
		ID: id, Verb: VerbRelease, Worker: worker, Force: force, CoalesceKey: coalesceKey,
	})
}

// SetWorkRef records where the work lives, or clears it with an empty ref. One
// reference: setting overwrites. The owner (an empty worker) may always set it;
// a worker only while its own claim stands. It is not a status transition, so
// `at` is deliberately left alone.
//
// The coalesce key is accepted and DELIBERATELY IGNORED, for the same reason it
// is on Undelegate: Ruby's `set_work_ref!` takes none, and merging a reference
// update into a neighbouring delegation write would make one undo revert both.
func (s *Store) SetWorkRef(id, workRef, worker, _ string) MutationResult {
	return s.WriteDelegation(DelegationRequest{
		ID: id, Verb: VerbWorkRef, WorkRef: workRef, Worker: worker,
	})
}

// SetDelegationNote records the briefing the receiver reads — how to work on
// the task, where the work should land — or clears it with an empty note. It is
// an OWNER decision, unlike work_ref, which the holding worker may also write:
// the note is the instruction, and a worker rewriting its own instructions is
// not a thing the model should allow. Like SetWorkRef the coalesce key is
// deliberately ignored.
//
// Unlike SetWorkRef it DOES restamp `at`. A note is owner intent about the
// delegation, and the multi-device order resolves competing intent by later
// `at`. Leaving the original delegation's stamp would make every note edit tie
// there and fall through to the canonical-byte tiebreak, so a device that
// cleared a stale briefing could silently lose to an older edit on another
// device — and an agent would then read a retracted instruction as live.
func (s *Store) SetDelegationNote(id, note, _ string) MutationResult {
	return s.WriteDelegation(DelegationRequest{ID: id, Verb: VerbDelegationNote, Note: note})
}

func (s *Store) planDelegationNote(target *record.Record, note string) delegationPlan {
	existing := delegationMarker(*target)
	if existing == nil {
		return delegationPlan{status: MutationInvalid, errors: []string{"task is not delegated"}}
	}
	text := strings.TrimSpace(note)
	if text != "" {
		if problems := record.DelegationNoteErrors(text); len(problems) > 0 {
			return delegationPlan{status: MutationInvalid, errors: problems[:1]}
		}
	}
	candidate := map[string]string{}
	for key, value := range existing {
		candidate[key] = value
	}
	if candidate["note"] == text {
		// Settled: comparing BEFORE the restamp, so re-stating the same briefing
		// is not a write and does not move the delegation's instant.
		return delegationPlan{status: MutationOK, noChange: true}
	}
	candidate["note"] = text
	// Restamp — EXCEPT on a claim. `at` means two different things depending on
	// status: on a delegated or ready marker it is when the owner last stated
	// their intent, which a note IS, so it moves. On a claimed marker it is when
	// the claim was TAKEN, and the merge ranks two claims by the earlier one, so
	// moving it would make briefing a worker that already holds the task lose to
	// an untouched copy of the same claim on another device — the note would
	// silently evaporate on sync, and a one-sided note write would be reported
	// as a conflict. For a claim the byte tiebreak already resolves in the
	// note-bearing marker's favour, which is the outcome we want.
	if markerStatus(candidate) != DelegationClaimed {
		candidate["at"] = DelegationStamp(s.now())
	}
	encoded := orderedDelegation(candidate, markerOrder(*target))
	target.Set(DelegationField, encoded)
	label := "clear delegation note: " + target.String("title")
	if text != "" {
		label = "delegation note: " + target.String("title")
	}
	return delegationPlan{status: MutationOK, label: label}
}

func planUndelegate(target *record.Record) delegationPlan {
	existing, present := target.Get(DelegationField)
	if !present {
		return delegationPlan{status: MutationOK, noChange: true}
	}
	target.Delete(DelegationField)
	return delegationPlan{
		status: MutationOK, label: "undelegate: " + target.String("title"),
		previous: string(existing),
	}
}

func (s *Store) planRelease(target *record.Record, worker string, force bool) delegationPlan {
	worker = strings.TrimSpace(worker)
	existing := delegationMarker(*target)
	if existing == nil || markerKind(existing) != "agent" || markerStatus(existing) != DelegationClaimed {
		return delegationPlan{status: MutationInvalid, errors: []string{"task is not claimed"}}
	}
	if plan := delegationIneligible(target, "released"); plan != nil {
		return *plan
	}
	if !force && worker != existing["assignee"] {
		return delegationPlan{
			status: MutationConflict,
			errors: []string{"claim is held by " + existing["assignee"] + ", not " +
				rubyInspectText(worker)},
			holder: existing["assignee"], at: existing["at"],
		}
	}
	released := map[string]string{}
	for key, value := range existing {
		released[key] = value
	}
	released["status"] = DelegationReady
	released["assignee"] = ""
	released["at"] = DelegationStamp(s.now())
	target.Set(DelegationField, orderedDelegation(released, markerOrder(*target)))
	return delegationPlan{status: MutationOK, label: "release: " + target.String("title")}
}

func planWorkRef(target *record.Record, workRef, worker string) delegationPlan {
	existing := delegationMarker(*target)
	if existing == nil {
		return delegationPlan{status: MutationInvalid, errors: []string{"task is not delegated"}}
	}
	if worker != "" {
		held := strings.TrimSpace(worker)
		if markerKind(existing) != "agent" || markerStatus(existing) != DelegationClaimed ||
			existing["assignee"] != held {
			return delegationPlan{
				status: MutationConflict,
				errors: []string{"a work reference from a worker requires a matching claim"},
				holder: existing["assignee"], at: existing["at"],
			}
		}
	}
	reference := strings.TrimSpace(workRef)
	if reference != "" {
		if problems := record.DelegationWorkRefErrors(reference); len(problems) > 0 {
			return delegationPlan{status: MutationInvalid, errors: problems[:1]}
		}
	}
	candidate := map[string]string{}
	for key, value := range existing {
		candidate[key] = value
	}
	candidate["work_ref"] = reference
	encoded := orderedDelegation(candidate, markerOrder(*target))
	if current, present := target.Get(DelegationField); present && string(current) == string(encoded) {
		return delegationPlan{status: MutationOK, noChange: true}
	}
	target.Set(DelegationField, encoded)
	label := "clear work ref: " + target.String("title")
	if reference != "" {
		label = "work ref → " + reference + ": " + target.String("title")
	}
	return delegationPlan{status: MutationOK, label: label}
}

func (s *Store) planDelegate(target *record.Record, request DelegationRequest) delegationPlan {
	kind := request.Kind
	mode := strings.TrimSpace(request.Mode)
	assignee := strings.TrimSpace(request.Assignee)
	if message := delegateInputErrorWith(kind, mode, assignee, s.Modes()); message != "" {
		return delegationPlan{status: MutationInvalid, errors: []string{message}}
	}
	// The note is validated BEFORE eligibility and before the marker is built, so
	// an over-long briefing reads as its own problem rather than as a
	// whole-marker shape report.
	note := strings.TrimSpace(request.Note)
	if request.SetNote && note != "" {
		if problems := record.DelegationNoteErrors(note); len(problems) > 0 {
			return delegationPlan{status: MutationInvalid, errors: problems[:1]}
		}
	}
	if plan := delegationIneligible(target, "delegated"); plan != nil {
		return *plan
	}

	existing := delegationMarker(*target)
	if markerStatus(existing) == DelegationClaimed && markerKind(existing) == "agent" {
		return delegationPlan{
			status: MutationConflict,
			errors: []string{"already claimed by " + existing["assignee"] + " at " + existing["at"] +
				"; undelegate to revoke the claim first"},
			holder: existing["assignee"], at: existing["at"],
		}
	}

	// A mode update or a new assignee of the SAME kind still points at the same
	// work, so the work reference survives; a human↔agent replacement is a
	// different delegation and drops it.
	// The note briefs whoever holds the work, so it survives the same way and
	// for the same reason: same kind keeps it, a human↔agent replacement drops
	// it along with the reference.
	//
	// A delegation that STATES a note overrides that retention in both
	// directions: it writes what it was given, and an empty one clears. A
	// delegation that says nothing about the note leaves it exactly as it was —
	// silently erasing a briefing because the owner re-stated the mode would be
	// the worst possible reading of an omitted field.
	retained := ""
	retainedNote := ""
	if existing != nil && existing["kind"] == kind {
		retained = existing["work_ref"]
		retainedNote = existing["note"]
	}
	if request.SetNote {
		retainedNote = note
	}
	status := DelegationReady
	if kind == "human" {
		status = DelegationDelegated
	}
	candidate := map[string]string{
		"kind": kind, "mode": mode, "assignee": assignee,
		"at": DelegationStamp(s.now()), "status": status, "work_ref": retained,
		"note": retainedNote,
	}
	// Two delegations describe the same state when only the transition stamp
	// differs: re-delegating at the current mode must not burn an undo slot.
	if existing != nil && settledDelegation(existing, candidate) {
		return delegationPlan{status: MutationOK, noChange: true}
	}

	target.Set(DelegationField, orderedDelegation(candidate, nil))
	label := "delegate " + mode + ": " + target.String("title")
	if kind == "human" {
		label = "delegate → " + assignee + ": " + target.String("title")
	}
	return delegationPlan{status: MutationOK, label: label}
}

func (s *Store) planClaim(target *record.Record, worker string) delegationPlan {
	worker = strings.TrimSpace(worker)
	if !validIdentifier(worker) {
		return delegationPlan{status: MutationInvalid, errors: []string{
			"worker id " + rubyInspectText(worker) + " must be non-empty, whitespace-free, " +
				"free of control characters, and at most " + itoa(AssigneeLimit) + " chars"}}
	}
	if plan := delegationIneligible(target, "claimed"); plan != nil {
		return *plan
	}

	existing := delegationMarker(*target)
	if existing == nil || markerKind(existing) != "agent" {
		return delegationPlan{status: MutationInvalid, errors: []string{"task is not delegated to the agent pool"}}
	}
	if markerStatus(existing) != DelegationReady {
		return delegationPlan{
			status: MutationConflict,
			errors: []string{"already claimed by " + existing["assignee"] + " at " + existing["at"]},
			holder: existing["assignee"], at: existing["at"],
		}
	}

	claimed := map[string]string{}
	for key, value := range existing {
		claimed[key] = value
	}
	claimed["status"] = DelegationClaimed
	claimed["assignee"] = worker
	claimed["at"] = DelegationStamp(s.now())
	target.Set(DelegationField, orderedDelegation(claimed, markerOrder(*target)))
	return delegationPlan{status: MutationOK, label: "claim: " + target.String("title")}
}

// delegationIneligible refuses everything that is not accepted live work, and
// names the state it is refusing: a proposal is an undecided suggestion, a
// closed task's marker is inert provenance, and an archived record is history.
func delegationIneligible(target *record.Record, verb string) *delegationPlan {
	if !target.Truthy("archived") && contains(check.OpenStates, target.String("state")) {
		return nil
	}
	state := target.String("state")
	if target.Truthy("archived") {
		state = "archived"
	}
	return &delegationPlan{
		status: MutationInvalid,
		errors: []string{"task is " + state + "; only accepted live tasks can be " + verb},
	}
}

// delegateInputErrorWith validates the caller's input against the vocabulary
// the store was given. There is no vocabulary-free spelling on purpose: a
// refusal has to quote the set that was actually enforced.
func delegateInputErrorWith(kind, mode, assignee string, modes record.ModeVocabulary) string {
	modes = record.Modes(modes)
	if !contains(DelegationKinds, kind) {
		return "delegation kind " + rubyInspectText(kind) + " must be " + strings.Join(DelegationKinds, " or ")
	}
	if kind == "human" {
		// A mode is optional for a person — it says what KIND of delegation this
		// is, not who holds it — but it is still a member of the vocabulary.
		if mode != "" && !modes.Valid(mode) {
			return "mode " + rubyInspectText(mode) + " must be one of " + modes.Quoted()
		}
		if !validEmail(assignee) {
			return "assignee " + rubyInspectText(assignee) + " must be an email address " +
				"(local@domain.tld, no whitespace or control characters, at most " +
				itoa(AssigneeLimit) + " chars)"
		}
		return ""
	}
	if !modes.Valid(mode) {
		return "mode " + rubyInspectText(mode) + " must be one of " + modes.Quoted()
	}
	if assignee != "" {
		return "an agent delegation is claimed by a worker, not assigned"
	}
	return ""
}

// settleDelegationOnClose drops an UNCLAIMED agent marker when a task closes.
// A claimed marker stays: it records who did the work.
func settleDelegationOnClose(target *record.Record) {
	existing := delegationMarker(*target)
	if existing != nil && markerKind(existing) == "agent" && markerStatus(existing) == DelegationReady {
		target.Delete(DelegationField)
	}
}

func settledDelegation(existing, candidate map[string]string) bool {
	for _, key := range []string{"kind", "mode", "assignee", "status", "work_ref", "note"} {
		if existing[key] != candidate[key] {
			return false
		}
	}
	// A key the existing marker carries that the candidate does not is a real
	// difference, so an unknown forward-compatible member cannot be silently
	// dropped by a "settled" verdict.
	for key := range existing {
		if key == "at" {
			continue
		}
		if _, known := candidate[key]; !known {
			return false
		}
	}
	return true
}

// orderedDelegation is Delegation.ordered: the declared key order first with
// absent members dropped, then any forward-compatible member in source order.
func orderedDelegation(values map[string]string, sourceOrder []string) json.RawMessage {
	ordered := []record.Field{}
	emitted := map[string]bool{}
	emit := func(key string) {
		if emitted[key] || values[key] == "" {
			return
		}
		emitted[key] = true
		ordered = append(ordered, record.Field{Key: key, Value: record.RawString(values[key])})
	}
	for _, key := range record.DelegationKeyOrder {
		emit(key)
	}
	for _, key := range sourceOrder {
		emit(key)
	}
	var out strings.Builder
	out.WriteByte('{')
	for index, field := range ordered {
		if index > 0 {
			out.WriteByte(',')
		}
		out.Write(record.RawString(field.Key))
		out.WriteByte(':')
		out.Write(field.Value)
	}
	out.WriteByte('}')
	return json.RawMessage(out.String())
}

// delegationMarker flattens a stored marker to its string members, which is all
// the plans compare. A value that is not an object is not a marker.
func delegationMarker(parsed record.Record) map[string]string {
	raw, present := parsed.Get(DelegationField)
	if !present {
		return nil
	}
	fields, err := record.Fields(raw)
	if err != nil {
		return nil
	}
	flat := map[string]string{}
	for _, field := range fields {
		var text string
		if json.Unmarshal(field.Value, &text) == nil {
			flat[field.Key] = text
			continue
		}
		flat[field.Key] = string(field.Value)
	}
	return flat
}

func markerOrder(parsed record.Record) []string {
	raw, present := parsed.Get(DelegationField)
	if !present {
		return nil
	}
	fields, err := record.Fields(raw)
	if err != nil {
		return nil
	}
	order := make([]string, 0, len(fields))
	for _, field := range fields {
		order = append(order, field.Key)
	}
	return order
}

func markerKind(marker map[string]string) string   { return marker["kind"] }
func markerStatus(marker map[string]string) string { return marker["status"] }

// DelegationStamp is the second-resolution UTC instant every marker records.
func DelegationStamp(instant time.Time) string { return record.DelegationStamp(instant) }

func decodeAny(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func validIdentifier(value string) bool { return record.DelegationIdentifier(value) }
func validEmail(value string) bool      { return record.DelegationEmail(value) }
func rubyInspectText(value string) string {
	return record.RubyInspect(value)
}
