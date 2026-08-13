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

// DelegationKinds and DelegationModes are the closed vocabularies a refusal
// quotes back to the caller.
var (
	DelegationKinds = []string{"human", "agent"}
	DelegationModes = []string{"refine", "research", "implement"}
)

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
func (s *Store) delegationMutation(id, coalesceKey string, plan func(*record.Record) delegationPlan) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		preflight := check.Check(s.org)
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
			if shape := record.DelegationErrors(decodeAny(marker)); len(shape) > 0 {
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

// Delegate hands a task to the agent pool or to a named person.
func (s *Store) Delegate(id, kind, mode, assignee, coalesceKey string) MutationResult {
	result := s.delegationMutation(id, coalesceKey, func(target *record.Record) delegationPlan {
		return s.planDelegate(target, kind, mode, assignee)
	})
	result.Summary.Action = "delegate"
	result.Summary.TaskID = id
	return result
}

// Claim takes an agent-pool task for one worker.
func (s *Store) Claim(id, worker, coalesceKey string) MutationResult {
	result := s.delegationMutation(id, coalesceKey, func(target *record.Record) delegationPlan {
		return s.planClaim(target, worker)
	})
	result.Summary.Action = "claim"
	result.Summary.TaskID = id
	return result
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
	result := s.delegationMutation(id, "", func(target *record.Record) delegationPlan {
		return planUndelegate(target)
	})
	result.Summary.Action = "undelegate"
	result.Summary.TaskID = id
	return result
}

// Release hands a claim back to the ready queue: claimed → ready, dropping the
// assignee. A worker must supply the id that matches the live claim; the owner
// passes force (with no worker id) to clear a stale claim without undelegating.
func (s *Store) Release(id, worker string, force bool, coalesceKey string) MutationResult {
	result := s.delegationMutation(id, coalesceKey, func(target *record.Record) delegationPlan {
		return s.planRelease(target, worker, force)
	})
	result.Summary.Action = "release"
	result.Summary.TaskID = id
	return result
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
	result := s.delegationMutation(id, "", func(target *record.Record) delegationPlan {
		return planWorkRef(target, workRef, worker)
	})
	result.Summary.Action = "work_ref"
	result.Summary.TaskID = id
	return result
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

func (s *Store) planDelegate(target *record.Record, kind, mode, assignee string) delegationPlan {
	mode = strings.TrimSpace(mode)
	assignee = strings.TrimSpace(assignee)
	if message := delegateInputError(kind, mode, assignee); message != "" {
		return delegationPlan{status: MutationInvalid, errors: []string{message}}
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
	retained := ""
	if existing != nil && existing["kind"] == kind {
		retained = existing["work_ref"]
	}
	status := DelegationReady
	if kind == "human" {
		status = DelegationDelegated
	}
	candidate := map[string]string{
		"kind": kind, "mode": mode, "assignee": assignee,
		"at": DelegationStamp(s.now()), "status": status, "work_ref": retained,
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

func delegateInputError(kind, mode, assignee string) string {
	if !contains(DelegationKinds, kind) {
		return "delegation kind " + rubyInspectText(kind) + " must be " + strings.Join(DelegationKinds, " or ")
	}
	if kind == "human" {
		if mode != "" {
			return "a human delegation has no mode"
		}
		if !validEmail(assignee) {
			return "assignee " + rubyInspectText(assignee) + " must be an email address " +
				"(local@domain.tld, no whitespace or control characters, at most " +
				itoa(AssigneeLimit) + " chars)"
		}
		return ""
	}
	if !contains(DelegationModes, mode) {
		return "mode " + rubyInspectText(mode) + " must be one of " + strings.Join(DelegationModes, "/")
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
	for _, key := range []string{"kind", "mode", "assignee", "status", "work_ref"} {
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
