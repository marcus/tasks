// Package merge is the deterministic, record-aware three-way merge for tasks
// JSONL files — the Go port of lib/tasks/jsonl_merge.rb.
//
// The merge is field-level; ordering stays ours-first while the final DFS walk
// guarantees parent-before-child structural validity. Nothing here touches a
// file: Merge builds and VALIDATES entirely in memory, and only a result that
// passed carries text. That ordering is the whole safety property — a
// write-then-validate driver would leave a rejected merge's output in the
// working file looking clean, and returning nonzero afterwards could not take
// it back.
package merge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/updatestamp"
)

// Version is the schema version this binary merges. Both SIDES must declare it.
const Version = check.Version

const delegationField = record.DelegationField

// temporalPairs are the date fields whose nested time metadata is one logical
// value with them. Resolving the two independently could attach one side's
// clock or zone to the other side's date.
var temporalPairs = []struct{ date, time string }{
	{"scheduled", "scheduled_time"},
	{"deadline", "deadline_time"},
}

// specialFields are the fields merge_record does NOT resolve with the ordinary
// scalar rule, because each has a rule of its own.
var specialFields = map[string]bool{
	record.LineKey: true, "updated": true,
	"state": true, "closed": true, "rejected": true,
	"tags": true, "body": true, delegationField: true,
	"scheduled": true, "scheduled_time": true,
	"deadline": true, "deadline_time": true,
}

var terminalStates = map[string]bool{}
var proposedStates = map[string]bool{}

func init() {
	for _, state := range check.ClosedStates {
		terminalStates[state] = true
	}
	for _, state := range check.ProposedStates {
		proposedStates[state] = true
	}
}

// Result is one merge attempt. A failed merge carries no text at all: there is
// no partial result a caller could be tempted to write.
type Result struct {
	Text   string
	Events []Event
	Error  string
}

// OK reports whether the merge produced a validated result.
func (r Result) OK() bool { return r.Error == "" }

// Merge resolves three sides into one validated JSONL text.
func Merge(baseText, oursText, theirsText string) Result {
	ours, err := parseSide("ours", oursText, false, false)
	if err != nil {
		return Result{Error: err.Error()}
	}
	theirs, err := parseSide("theirs", theirsText, false, false)
	if err != nil {
		return Result{Error: err.Error()}
	}
	base, err := parseSide("base", baseText, true, true)
	if err != nil {
		return Result{Error: err.Error()}
	}

	events := &eventLog{}
	baseByID := indexByID(base)
	oursByID := indexByID(ours)
	theirsByID := indexByID(theirs)
	ids := orderedUnion(oursByID.order, theirsByID.order, baseByID.order)
	merged := newIndex()

	for _, id := range ids {
		if resolved := resolveRecord(id, baseByID.get(id), oursByID.get(id), theirsByID.get(id), events); resolved != nil {
			merged.put(id, resolved)
		}
	}

	logOrderConflicts(merged, base, ours, theirs, events)
	restoreRequiredAncestors(merged, baseByID, oursByID, theirsByID, events)

	records, err := orderRecords(merged, ours, theirs, base)
	if err != nil {
		return Result{Error: err.Error()}
	}
	dumpable := make([]record.Record, 0, len(records))
	for _, entry := range records {
		dumpable = append(dumpable, entry.toRecord())
	}
	text, dumpErr := record.Dump(dumpable)
	if dumpErr != nil {
		return Result{Error: dumpErr.Error()}
	}
	if validation := check.CheckText([]byte(text)); !validation.OK() {
		return Result{Error: "merged output is invalid: " + describe(validation.Errors)}
	}
	return Result{Text: text, Events: events.entries}
}

// parseSide reads and validates one side.
//
// Both SIDES must declare the schema version this binary implements. There is
// no cross-version merge of sides: reconciling records whose meaning differs by
// version is exactly the silent corruption a version header exists to stop.
//
// The BASE is the deliberate exception (allowOlder), and it is not symmetry
// being given up — a base is not merged, it is only consulted to tell "changed"
// from "unchanged". An older base under two current sides is the ordinary shape
// of a merge whose common ancestor predates a schema upgrade: merge, rebase,
// cherry-pick and revert all reach for ancestors arbitrarily far back. Refusing
// it produced a hard CONFLICT for a case that is safe.
//
// A NEWER base is still refused: a base ahead of both sides means this binary is
// the stale one and cannot know what the ancestor's records meant. The base is
// validated against its OWN declared version, so a v1 ancestor does not fail the
// lint for being a faithful v1 file.
func parseSide(label, text string, allowEmpty, allowOlder bool) ([]*entry, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("%s is not valid UTF-8", label)
	}
	if allowEmpty && text == "" {
		return []*entry{}, nil
	}
	parsed := record.Parse([]byte(text))
	if !parsed.OK() {
		return nil, fmt.Errorf("%s cannot be parsed: %s", label, describeParse(parsed.Errors))
	}
	version, declared := declaredVersion(parsed.Records)
	if declared && !acceptableVersion(version, allowOlder) {
		return nil, fmt.Errorf("%s is schema v%d; this binary reads schema v%d only", label, version, Version)
	}
	against := Version
	if declared {
		against = version
	}
	if validation := check.CheckParsedVersion(parsed, against); !validation.OK() {
		return nil, fmt.Errorf("%s is invalid: %s", label, describe(validation.Errors))
	}
	entries := make([]*entry, 0, len(parsed.Records))
	for _, parsedRecord := range parsed.Records {
		entries = append(entries, fromRecord(parsedRecord))
	}
	return entries, nil
}

// acceptableVersion is current always; older only where the caller says an older
// file is merely an ancestor. Never newer, in either position.
func acceptableVersion(version int, allowOlder bool) bool {
	if version == Version {
		return true
	}
	return allowOlder && version < Version
}

// declaredVersion is the meta record's version when it is an Integer. A
// non-Integer version is not skew, it is malformed, and Check reports it as
// such.
func declaredVersion(records []record.Record) (int, bool) {
	var meta *record.Record
	for index := range records {
		if records[index].Line == 1 {
			meta = &records[index]
			break
		}
	}
	if meta == nil {
		if len(records) == 0 {
			return 0, false
		}
		meta = &records[0]
	}
	fields := fromRecord(*meta)
	if fields.str("type") != "meta" {
		return 0, false
	}
	return strictInteger(fields.get("version"))
}

func strictInteger(raw json.RawMessage) (int, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || strings.ContainsAny(text, ".eE") {
		return 0, false
	}
	var value int
	if json.Unmarshal([]byte(text), &value) != nil {
		return 0, false
	}
	return value, true
}

func describe(entries []check.Entry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf("line %d: %s", entry.Line, entry.Message))
	}
	return strings.Join(parts, "; ")
}

func describeParse(entries []record.ParseError) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf("line %d: %s", entry.Line, entry.Message))
	}
	return strings.Join(parts, "; ")
}

// index is a Ruby Hash keyed by record id: insertion-ordered, because the order
// the merged records were decided in is the order the failure diagnostic and
// the sibling walk both read.
type index struct {
	order   []string
	entries map[string]*entry
}

func newIndex() *index { return &index{entries: map[string]*entry{}} }

func (i *index) get(id string) *entry {
	if i == nil {
		return nil
	}
	return i.entries[id]
}

func (i *index) has(id string) bool {
	_, ok := i.entries[id]
	return ok
}

func (i *index) put(id string, value *entry) {
	if _, exists := i.entries[id]; !exists {
		i.order = append(i.order, id)
	}
	i.entries[id] = value
}

func indexByID(records []*entry) *index {
	built := newIndex()
	for _, candidate := range records {
		if id := candidate.id(); id != "" {
			built.put(id, candidate)
		}
	}
	return built
}

// resolveRecord decides one id from its three sides.
func resolveRecord(id string, base, ours, theirs *entry, events *eventLog) *entry {
	if base == nil {
		return addedRecord(id, ours, theirs, events)
	}
	if ours == nil && theirs == nil {
		events.add(Event{ID: id, Decision: DecisionDeleted})
		return nil
	}
	if ours == nil {
		if sameEntry(theirs, base) {
			events.add(Event{ID: id, Decision: DecisionDeletedByOurs})
			return nil
		}
		events.add(Event{ID: id, Decision: DecisionKeptTheirsEditOverOursDelete})
		return theirs.clone()
	}
	if theirs == nil {
		if sameEntry(ours, base) {
			events.add(Event{ID: id, Decision: DecisionDeletedByTheirs})
			return nil
		}
		events.add(Event{ID: id, Decision: DecisionKeptOursEditOverTheirsDelete})
		return ours.clone()
	}
	if sameEntry(ours, theirs) {
		return ours.clone()
	}
	return mergeRecord(id, base, ours, theirs, events, DecisionMergedFields)
}

func addedRecord(id string, ours, theirs *entry, events *eventLog) *entry {
	switch {
	case ours != nil && theirs != nil:
		if sameEntry(ours, theirs) {
			return ours.clone()
		}
		return mergeRecord(id, nil, ours, theirs, events, DecisionMergedConcurrentAdd)
	case ours != nil:
		events.add(Event{ID: id, Decision: DecisionAddedOurs})
		return ours.clone()
	case theirs != nil:
		events.add(Event{ID: id, Decision: DecisionAddedTheirs})
		return theirs.clone()
	}
	return nil
}

// mergeRecord resolves one record field by field, then reconciles the parts
// whose legality depends on each other.
func mergeRecord(id string, base, ours, theirs *entry, events *eventLog, decision string) *entry {
	event := &Event{ID: id, Decision: decision}
	merged := newEntry()

	baseKeys := []string{}
	if base != nil {
		baseKeys = base.keys
	}
	keys := orderedUnion(ours.keys, theirs.keys, baseKeys)
	present := map[string]bool{}
	for _, key := range keys {
		present[key] = true
	}

	for _, field := range keys {
		if specialFields[field] {
			continue
		}
		value := mergeScalar(field, valueOf(base, field), valueOf(ours, field),
			valueOf(theirs, field), ours, theirs, event)
		merged.assign(field, value)
	}

	for _, pair := range temporalPairs {
		if !present[pair.date] && !present[pair.time] {
			continue
		}
		mergeTemporalPair(merged, pair.date, pair.time, base, ours, theirs, event)
	}

	if present["tags"] {
		merged.assign("tags", mergeTags(valueOf(base, "tags"), valueOf(ours, "tags"), valueOf(theirs, "tags")))
	}
	if present["body"] {
		merged.assign("body", mergeBody(valueOf(base, "body"), valueOf(ours, "body"),
			valueOf(theirs, "body"), ours, theirs, event))
	}
	if present[delegationField] {
		mergeDelegation(merged, base, ours, theirs, event)
	}
	if present["state"] {
		mergeState(merged, base, ours, theirs, event)
	}
	// Runs after both, because the rule reads the resolved state and rewrites
	// the resolved delegation.
	settleDelegationState(merged, event)
	merged.assign("updated", stampValue(updatestamp.Max(stampOf(ours), stampOf(theirs))))

	events.add(*event)
	return merged
}

func stampOf(side *entry) string { return side.str("updated") }

func stampValue(stamp string) json.RawMessage {
	if stamp == "" {
		return nil
	}
	quoted, err := json.Marshal(stamp)
	if err != nil {
		return nil
	}
	return json.RawMessage(quoted)
}

// mergeScalar is the classic three-way rule, falling back to last-write-wins.
func mergeScalar(field string, baseValue, oursValue, theirsValue json.RawMessage,
	ours, theirs *entry, event *Event) json.RawMessage {
	if sameValue(oursValue, theirsValue) {
		return oursValue
	}
	if sameValue(oursValue, baseValue) {
		return theirsValue
	}
	if sameValue(theirsValue, baseValue) {
		return oursValue
	}
	event.Conflicts = append(event.Conflicts, field)
	if lwwSide(ours, theirs, event, field) == sideOurs {
		return oursValue
	}
	return theirsValue
}

// mergeTemporalPair resolves a date and its time metadata as ONE unit: the
// three-way rules apply to the [date, time] tuple, and a genuine conflict is
// decided once, with the winning side supplying both halves.
func mergeTemporalPair(merged *entry, dateField, timeField string, base, ours, theirs *entry, event *Event) {
	basePair := [2]json.RawMessage{valueOf(base, dateField), valueOf(base, timeField)}
	oursPair := [2]json.RawMessage{valueOf(ours, dateField), valueOf(ours, timeField)}
	theirsPair := [2]json.RawMessage{valueOf(theirs, dateField), valueOf(theirs, timeField)}

	var winner [2]json.RawMessage
	switch {
	case samePair(oursPair, theirsPair):
		winner = oursPair
	case samePair(oursPair, basePair):
		winner = theirsPair
	case samePair(theirsPair, basePair):
		winner = oursPair
	default:
		event.Conflicts = append(event.Conflicts, dateField)
		if lwwSide(ours, theirs, event, dateField) == sideOurs {
			winner = oursPair
		} else {
			winner = theirsPair
		}
	}
	merged.assign(dateField, winner[0])
	merged.assign(timeField, winner[1])
}

func samePair(left, right [2]json.RawMessage) bool {
	return sameValue(left[0], right[0]) && sameValue(left[1], right[1])
}

// mergeTags is a union that keeps the base's order for the tags that survived
// and sorts the concurrent additions, so two devices agree on the bytes.
func mergeTags(baseTags, oursTags, theirsTags json.RawMessage) json.RawMessage {
	base := tagList(baseTags)
	union := make([]string, 0)
	seen := map[string]bool{}
	for _, tag := range append(append([]string{}, tagList(oursTags)...), tagList(theirsTags)...) {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		union = append(union, tag)
	}
	inUnion := map[string]bool{}
	for _, tag := range union {
		inUnion[tag] = true
	}
	retained := make([]string, 0, len(base))
	retainedSet := map[string]bool{}
	for _, tag := range base {
		if inUnion[tag] && !retainedSet[tag] {
			retained = append(retained, tag)
			retainedSet[tag] = true
		}
	}
	additions := make([]string, 0, len(union))
	for _, tag := range union {
		if !retainedSet[tag] {
			additions = append(additions, tag)
		}
	}
	sort.Strings(additions)
	merged := append(retained, additions...)
	if len(merged) == 0 {
		return nil
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

// tagList is Ruby's Array(): an array yields its members, nil yields nothing,
// and any other value yields itself as a single member.
func tagList(raw json.RawMessage) []string {
	if nilValue(raw) {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}
	return nil
}

// mergeBody prefers the longer of two appends before calling a conflict: an
// append on top of an append is the ordinary way two devices touch one note,
// and taking the superset loses nothing.
func mergeBody(baseValue, oursValue, theirsValue json.RawMessage, ours, theirs *entry, event *Event) json.RawMessage {
	if sameValue(oursValue, theirsValue) {
		return oursValue
	}
	if sameValue(oursValue, baseValue) {
		return theirsValue
	}
	if sameValue(theirsValue, baseValue) {
		return oursValue
	}
	oursText, oursIsText := decodeString(oursValue)
	theirsText, theirsIsText := decodeString(theirsValue)
	if oursIsText && theirsIsText {
		if strings.HasPrefix(theirsText, oursText) {
			return theirsValue
		}
		if strings.HasPrefix(oursText, theirsText) {
			return oursValue
		}
	}
	event.Conflicts = append(event.Conflicts, "body")
	if lwwSide(ours, theirs, event, "body") == sideOurs {
		return oursValue
	}
	return theirsValue
}

// mergeState resolves the state and carries the closed date with it, because a
// closed date belonging to the losing state would name a day nothing happened.
func mergeState(merged *entry, base, ours, theirs *entry, event *Event) {
	state, winner := resolveState(valueOf(base, "state"), valueOf(ours, "state"),
		valueOf(theirs, "state"), ours, theirs, event)
	merged.assign("state", state)

	var closed json.RawMessage
	switch winner {
	case sideOurs:
		closed = valueOf(ours, "closed")
	case sideTheirs:
		closed = valueOf(theirs, "closed")
	default:
		closed = mergeScalar("closed", valueOf(base, "closed"), valueOf(ours, "closed"),
			valueOf(theirs, "closed"), ours, theirs, event)
	}
	name, _ := decodeString(state)
	if !terminalStates[name] {
		closed = nil
	}
	merged.assign("closed", closed)

	// The decline marker rides with the state for the same reason the closed date
	// does, and one step further: it is only valid on CANCELLED, so a side whose
	// state lost must not leave it behind on the winner. A restore on one device
	// and a re-decision on another therefore converge on a file `check` accepts.
	var rejected json.RawMessage
	switch winner {
	case sideOurs:
		rejected = valueOf(ours, "rejected")
	case sideTheirs:
		rejected = valueOf(theirs, "rejected")
	default:
		rejected = mergeScalar("rejected", valueOf(base, "rejected"), valueOf(ours, "rejected"),
			valueOf(theirs, "rejected"), ours, theirs, event)
	}
	if name != "CANCELLED" {
		rejected = nil
	}
	merged.assign("rejected", rejected)
}

type side int

const (
	sideBoth side = iota
	sideOurs
	sideTheirs
)

// resolveState prefers a progressed state over an open one: a device that
// finished the work knows something the device that did not finish it does not.
func resolveState(baseState, oursState, theirsState json.RawMessage, ours, theirs *entry, event *Event) (json.RawMessage, side) {
	if sameValue(oursState, theirsState) {
		return oursState, sideBoth
	}
	if sameValue(oursState, baseState) {
		return theirsState, sideTheirs
	}
	if sameValue(theirsState, baseState) {
		return oursState, sideOurs
	}
	oursName, _ := decodeString(oursState)
	theirsName, _ := decodeString(theirsState)
	oursTerminal := terminalStates[oursName]
	theirsTerminal := terminalStates[theirsName]
	if oursTerminal != theirsTerminal {
		event.Conflicts = append(event.Conflicts, "state")
		if oursTerminal {
			return oursState, sideOurs
		}
		return theirsState, sideTheirs
	}
	event.Conflicts = append(event.Conflicts, "state")
	if lwwSide(ours, theirs, event, "state") == sideOurs {
		return oursState, sideOurs
	}
	return theirsState, sideTheirs
}

// lwwSide decides a genuine conflict by the record's update stamp. Two records
// with no stamp at all cannot be ordered, so ours wins and the decision is
// logged low-confidence rather than presented as a resolution.
func lwwSide(ours, theirs *entry, event *Event, field string) side {
	oursStamp := stampOf(ours)
	theirsStamp := stampOf(theirs)
	switch comparison := updatestamp.Compare(oursStamp, theirsStamp); {
	case comparison > 0:
		return sideOurs
	case comparison < 0:
		return sideTheirs
	}
	// Ruby's test is `nil?` on the raw field, not "invalid": two records that
	// both carry an unusable stamp are still stamped records, and fall to the
	// byte tiebreak rather than being reported as undecidable.
	if nilValue(valueOf(ours, "updated")) && nilValue(valueOf(theirs, "updated")) {
		event.LowConfidence = append(event.LowConfidence, field)
		return sideOurs
	}
	// Equal valid stamps can occur after a common prior merge. Break that tie by
	// the complete record bytes so swapping ours/theirs stays commutative.
	if dumpFor(ours) >= dumpFor(theirs) {
		return sideOurs
	}
	return sideTheirs
}

func dumpFor(side *entry) string {
	line, err := record.DumpRecord(side.toRecord())
	if err != nil {
		return ""
	}
	return line
}
