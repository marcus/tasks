package store

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/temporal"
)

// PatchValue is the typed value a field patch carries.
//
// Ruby's `apply_semantic_patch` receives an untyped Ruby object and each field
// asserts the shape it accepts: a String for `title`, `true`/`false` for
// `deferred`, an Array for the tag slices, a Date or TemporalValue for the two
// date fields, `nil` for "clear this". A single Go string cannot express that —
// most importantly it cannot tell "clear the priority" (nil) apart from "set
// the priority to the empty string" (which Ruby refuses) — so the value model
// is explicit instead. The zero value is the CLEARING value, which is what the
// absent-argument spelling of every clearing verb means.
type PatchValue struct {
	kind    valueKind
	text    string
	boolean bool
	list    []string
	add     []string
	remove  []string
	value   temporal.Value
	// placement is the structural destination of a `location` change. It has
	// its own kind rather than reusing text because "move under this parent"
	// and "put this exactly here" are different operations with different
	// refusals, and a single string cannot tell them apart.
	placement Placement
	links     []links.FormalLink
}

type valueKind int

const (
	kindNone valueKind = iota
	kindText
	kindBool
	kindList
	kindTagDelta
	kindTemporal
	kindPlacement
	kindUnnest
	kindLinks
)

// NoValue is Ruby's `nil`: clear this field.
func NoValue() PatchValue { return PatchValue{kind: kindNone} }

// TextValue is a String value.
func TextValue(text string) PatchValue { return PatchValue{kind: kindText, text: text} }

// BoolValue is a `true`/`false` value.
func BoolValue(value bool) PatchValue { return PatchValue{kind: kindBool, boolean: value} }

// ListValue is an Array-of-String value.
func ListValue(values []string) PatchValue {
	return PatchValue{kind: kindList, list: append([]string{}, values...)}
}

// LinksValue is an ordered full replacement of the stored formal-link list.
func LinksValue(values []links.FormalLink) PatchValue {
	return PatchValue{kind: kindLinks, links: append([]links.FormalLink(nil), values...)}
}

// TagDeltaValue is the `{add:, remove:}` Hash the CLI's `tag` verb sends.
func TagDeltaValue(add, remove []string) PatchValue {
	return PatchValue{
		kind: kindTagDelta,
		add:  append([]string{}, add...), remove: append([]string{}, remove...),
	}
}

// TemporalValue is a date, optionally qualified by a wall time and a zone.
func TemporalValue(value temporal.Value) PatchValue {
	return PatchValue{kind: kindTemporal, value: value}
}

// IsNone reports the clearing value.
func (v PatchValue) IsNone() bool { return v.kind == kindNone }

// Text is the string half of a text value, or "" for any other kind.
func (v PatchValue) Text() string {
	if v.kind == kindText {
		return v.text
	}
	return ""
}

// off is Ruby's `nil` / `:off`, which `lead` and `recurrence` both accept as
// "clear this".
//
// The literal STRING "off" is deliberately NOT off. Ruby's `:off` is a Symbol,
// and a String reaching `patch_lead` or `patch_recurrence` is validated as a
// span or a cookie — so `off` typed by a user is refused by the store and
// translated to nil by the adapter that read the word. Accepting the string
// here would make the store guess at a spelling that is the CLI's to own, and
// the differential harness caught it doing exactly that.
func (v PatchValue) off() bool { return v.kind == kindNone }

// -- the field vocabulary ------------------------------------------------------

// The implemented fields. The list is EditSnapshot::FIELDS plus the three
// composite commands TaskChangeset::SPECIAL_FIELDS names, which are not editor
// fields but do reach the same transaction.
const (
	// FieldTitle replaces the task's title.
	FieldTitle PatchField = "title"
	// FieldDeferred adds or removes the someday/maybe tag.
	FieldDeferred PatchField = "deferred"
	// FieldScheduled sets or clears the available-from date.
	FieldScheduled PatchField = "scheduled"
	// FieldDeadline sets or clears the deadline.
	FieldDeadline PatchField = "deadline"
	// FieldRecurrence sets or clears the recurrence cookie.
	FieldRecurrence PatchField = "recurrence"
	// FieldLead sets or clears the lead-time window.
	FieldLead PatchField = "lead"
	// FieldContexts replaces the @context slice of the tag list.
	FieldContexts PatchField = "contexts"
	// FieldTags replaces the ordinary slice of the tag list.
	FieldTags PatchField = "tags"
	// FieldBody replaces the note body.
	FieldBody PatchField = "body"
	// FieldLinks replaces the ordered formal-link list.
	FieldLinks PatchField = "links"
	// FieldLocation moves the task. Not implemented — see applyFieldPatch.
	FieldLocation PatchField = "location"

	// FieldTagDelta is the CLI `tag` verb's whole-sequence add/remove.
	FieldTagDelta PatchField = "tag_delta"
	// FieldActivate is the composite "available now" operation.
	FieldActivate PatchField = "activate"
	// FieldDateClear is `undate`: both date fields in one checked write.
	FieldDateClear PatchField = "date_clear"
)

// dateOwningFields is Store::DATE_OWNING_FIELDS: the writes after which a
// recurrence or lead window may have lost its last anchor.
var dateOwningFields = []PatchField{FieldScheduled, FieldDeadline, FieldDateClear}

// -- baselines -----------------------------------------------------------------

// fieldBaseline is EditSnapshot's `baselines` for one field, rendered as the
// opaque string the conflict check compares. Rendering rather than typing it is
// deliberate: the baseline crosses a process boundary (the CLI reads it, then
// writes with it) and its ONLY contract is that the same expression produces it
// on both sides.
func fieldBaseline(records []record.Record, index int, field PatchField) (string, error) {
	parsed := records[index]
	switch field {
	case FieldTitle:
		return parsed.String("title"), nil
	case FieldPriority:
		return parsed.String("priority"), nil
	case FieldDeferred:
		if contains(semanticTags(parsed), DeferTag) {
			return "true", nil
		}
		return "false", nil
	case FieldScheduled:
		return temporalExpectation(parsed, "scheduled"), nil
	case FieldDeadline:
		return temporalExpectation(parsed, "deadline"), nil
	case FieldRecurrence:
		return parsed.String("recur"), nil
	case FieldLead:
		return parsed.String("lead"), nil
	case FieldContexts:
		return string(stringArray(contextTags(semanticTags(parsed)))), nil
	case FieldTags:
		return string(stringArray(ordinaryTags(semanticTags(parsed)))), nil
	case FieldBody:
		return parsed.String("body"), nil
	case FieldLinks:
		raw := fieldRaw(parsed, "links")
		if len(raw) == 0 {
			return "[]", nil
		}
		canonical, err := canonical(raw)
		return string(canonical), err
	case FieldLocation:
		return locationFingerprint(records, index, siblingIDsByParent(records))
	case FieldState:
		return lifecycleFingerprint(records, index)
	case FieldActivate:
		// Ruby has NO baseline for activate: `patch_expected_for` falls through
		// to `EditSnapshot#expected_for`, whose `baselines.fetch(:activate)`
		// raises KeyError. That path is unreachable in the Ruby product —
		// `tasks activate` sends a changeset guarded by a whole-task revision
		// instead — but this build's single-field entry point needs a baseline,
		// so it uses the pair activation actually reads: the defer marker and
		// the available-from date.
		deferred := "false"
		if contains(semanticTags(parsed), DeferTag) {
			deferred = "true"
		}
		return string(jsonArray(jsonString(deferred),
			jsonString(temporalExpectation(parsed, "scheduled")))), nil
	case FieldTagDelta:
		// EditSnapshot#metadata[:tag_sequence]: the WHOLE ordered list, because
		// the delta rewrites the whole list and a slice baseline would let an
		// unrelated context edit through.
		return string(stringArray(semanticTags(parsed))), nil
	case FieldDateClear:
		// metadata[:date_state]: both dates and the cookie they anchor, because
		// `undate` retires the cookie as well.
		return string(jsonArray(
			jsonString(temporalExpectation(parsed, "scheduled")),
			jsonString(temporalExpectation(parsed, "deadline")),
			jsonString(parsed.String("recur")),
		)), nil
	}
	return "", errUnknownField
}

var errUnknownField = &patchError{"unknown editable field"}

type patchError struct{ text string }

func (e *patchError) Error() string { return e.text }

// temporalExpectation is EditSnapshot#temporal_expectation: an all-day value is
// compared as its date, a timed one as the whole stamp — so a zone-only edit
// still invalidates a date expectation.
func temporalExpectation(parsed record.Record, field string) string {
	value, ok := temporalFromRecord(parsed, field)
	if !ok {
		return ""
	}
	if value.AllDay() {
		return value.Date.ISO()
	}
	return value.Date.ISO() + "T" + value.LocalTime + "|" + value.Timezone + "|" + itoa(value.Fold)
}

func temporalFromRecord(parsed record.Record, field string) (temporal.Value, bool) {
	stored := parsed.String(field)
	if stored == "" {
		return temporal.Value{}, false
	}
	local, zone, fold := decodeTimeObject(fieldRaw(parsed, field+"_time"))
	return temporal.FromRecord(stored, local, zone, fold, false)
}

func decodeTimeObject(raw json.RawMessage) (string, string, int) {
	if len(raw) == 0 {
		return "", "", 0
	}
	fields, err := record.Fields(raw)
	if err != nil {
		return "", "", 0
	}
	local, zone, fold := "", "", 0
	for _, field := range fields {
		switch field.Key {
		case "local":
			local, _ = decodeString(field.Value)
		case "timezone":
			zone, _ = decodeString(field.Value)
		case "fold":
			var number int
			if json.Unmarshal(field.Value, &number) == nil {
				fold = number
			}
		}
	}
	return local, zone, fold
}

func contextTags(tags []string) []string {
	out := []string{}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "@") {
			out = append(out, tag)
		}
	}
	return out
}

func ordinaryTags(tags []string) []string {
	out := []string{}
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "@") && tag != DeferTag {
			out = append(out, tag)
		}
	}
	return out
}

// -- the patches ---------------------------------------------------------------

// patchContext is everything a field needs beyond the record: the day a close
// stamps, the clock a temporal roll resolves candidates against, and the
// delegation stamp a rolled marker records.
type patchContext struct {
	today    temporal.Date
	temporal temporal.Context
	stamp    string
	// explicit says the CALLER supplied the clock. `activate` branches on it:
	// with a reader's real clock a timed available-from date is compared as an
	// instant, and without one it falls back to comparing calendar days, which
	// is the whole of the difference Ruby's `temporal_context:` default makes.
	explicit bool
	// allowProposedAncestor waives the "accepted work cannot remain under a
	// proposed task" rule. ONLY an approval sets it, and only because a
	// proposal tree is decided leaves-first: the child is approved while its
	// parent is still PROPOSED, and the parent's own approval is next. Every
	// other write keeps the rule, or a plain `state` patch would become a way
	// around the proposal gate entirely.
	allowProposedAncestor bool
	// maxDepth is the nesting cap a move may not push a subtree past. It rides
	// on the context because applyFieldPatch is a free function shared by both
	// transactions, and a package-level global would be a data race the moment
	// two stores wrote at once.
	maxDepth int
	// force is `patch_location`'s `force:`. Without it a move to the parent the
	// task already has is a satisfied no-op; with it the subtree is re-appended
	// at the end of that parent's children, which is what `move --under` and
	// `move "Section"` mean when the task is already there.
	force bool
	// placementTargets are the destination indexes a changeset resolved BEFORE
	// the field loop, so a bad id refuses as not_found with field errors rather
	// than as a generic invalid halfway through.
	placementTargets *placementTargets
}

func patchInvalid(message string) patchOutcome {
	return patchOutcome{status: MutationInvalid, errors: []string{message}}
}

func patchOK(parsed record.Record) patchOutcome {
	return patchOutcome{status: MutationOK, touchedIDs: []string{parsed.String("id")}}
}

func patchTitle(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindText {
		return patchInvalid("title must be text")
	}
	title := rubyStrip(value.text)
	if title == "" {
		return patchInvalid("title cannot be blank")
	}
	records[index].SetString("title", title)
	return patchOK(records[index])
}

func patchBody(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindText {
		return patchInvalid("body must be text")
	}
	records[index].SetOptional("body", record.RawString(value.text))
	return patchOK(records[index])
}

func patchLinks(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindLinks {
		return patchInvalid("links must be a list of formal links")
	}
	if len(value.links) > links.MaxFormalLinks {
		return patchInvalid("links may contain at most 50 entries")
	}
	seen := map[string]bool{}
	for _, link := range value.links {
		if !links.ValidFormalURL(link.URL) {
			return patchInvalid("link URL must be an http or https URL with a host")
		}
		if link.Label != "" && !links.ValidFormalLabel(link.Label) {
			return patchInvalid("link label must be non-empty, trimmed single-line text")
		}
		if seen[link.URL] {
			return patchInvalid("duplicate formal link URL: " + link.URL)
		}
		seen[link.URL] = true
	}
	raw, err := json.Marshal(value.links)
	if err != nil {
		return patchInvalid("links could not be encoded")
	}
	records[index].SetOptional("links", raw)
	return patchOK(records[index])
}

func patchDeferred(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindBool {
		return patchInvalid("deferred must be true or false")
	}
	tags := semanticTags(records[index])
	if contains(tags, DeferTag) == value.boolean {
		return patchOK(records[index])
	}
	if value.boolean {
		tags = append(tags, DeferTag)
	} else {
		tags = withoutTag(tags, DeferTag)
	}
	records[index].SetOptional("tags", record.RawStrings(tags))
	return patchOK(records[index])
}

// patchActivate is the composite "available now" operation. It deliberately
// preserves recurrence when a future available-from date was its only anchor:
// activation owns availability, not the recurrence contract.
func patchActivate(records []record.Record, index int, value PatchValue, context patchContext) patchOutcome {
	if value.kind != kindBool || !value.boolean {
		return patchInvalid("activate must be true")
	}
	target := &records[index]
	target.SetOptional("tags", record.RawStrings(withoutTag(semanticTags(*target), DeferTag)))

	scheduled, hasScheduled := temporalFromRecord(*target, "scheduled")
	future := false
	if hasScheduled {
		if scheduled.LocalTime != "" && context.explicit {
			if instant, err := scheduled.ReleaseInstant(context.temporal); err == nil {
				future = instant.After(context.temporal.Now)
			}
		} else {
			future = scheduled.Date.After(context.today)
		}
	}

	// A lead task releases the CURRENT OCCURRENCE, stamped by its anchor date,
	// and keeps every date it has: the anchor is what the next window is
	// measured from, and the roll re-arms it.
	deadlineDate, hasDeadline := storedDate(*target, "deadline")
	scheduledDate, hasScheduledDate := storedDate(*target, "scheduled")
	anchor, hasAnchor := leadAnchor(deadlineDate, hasDeadline, scheduledDate, hasScheduledDate)
	if hasAnchor && lead.Span(target.String("lead")) {
		target.SetString("lead_skip", anchor.ISO())
		return patchOK(*target)
	}
	if future {
		target.Delete("scheduled")
		target.Delete("scheduled_time")
	}
	return patchOK(*target)
}

// patchDate writes or clears one of the two date fields.
func patchDate(records []record.Record, index int, value PatchValue, kind string) patchOutcome {
	target := &records[index]
	stamp, present, ok := patchDateValue(value)
	if !ok {
		return patchInvalid(kind + " must be a date/time or nil")
	}
	if present {
		// Rule 3: the lead owns the task's own timed gate. A lead task may not
		// end up carrying BOTH dates, from either direction.
		other := "deadline"
		if kind == "deadline" {
			other = "scheduled"
		}
		if lead.Span(target.String("lead")) && target.Truthy(other) {
			return patchInvalid(leadGateConflictMessage(target.String("lead")))
		}
		writeTemporal(target, kind, stamp)
		if target.String("state") == "INBOX" {
			target.SetString("state", "TODO")
		}
	} else {
		target.Delete(kind)
		target.Delete(kind + "_time")
	}
	target.Delete("lead_skip")
	return patchOK(*target)
}

// patchDateValue is `normalize_patch_date`. A blank string is nil, an ISO date
// is a date, and anything else is the typed refusal — never a silent no-op.
func patchDateValue(value PatchValue) (temporal.Value, bool, bool) {
	switch value.kind {
	case kindNone:
		return temporal.Value{}, false, true
	case kindTemporal:
		return value.value, true, true
	case kindText:
		if value.text == "" {
			return temporal.Value{}, false, true
		}
		date, ok := temporal.ParseDate(value.text)
		if !ok {
			return temporal.Value{}, false, false
		}
		return temporal.Value{Date: date}, true, true
	}
	return temporal.Value{}, false, false
}

// patchDateClear is `undate`: one checked write and one undo entry rather than
// an observable intermediate state between two single-date patches.
func patchDateClear(records []record.Record, index int, value PatchValue) patchOutcome {
	kind := value.Text()
	if value.kind != kindNone && value.kind != kindText {
		return patchInvalid("date clear kind must be deadline, scheduled, or nil")
	}
	if kind != "" && kind != "deadline" && kind != "scheduled" {
		return patchInvalid("date clear kind must be deadline, scheduled, or nil")
	}
	fields := []string{"scheduled", "deadline"}
	if kind != "" {
		fields = []string{kind}
	}
	target := &records[index]
	present := false
	for _, field := range fields {
		if target.Truthy(field) {
			present = true
		}
	}
	if !present {
		return patchInvalid("no matching date stamp")
	}
	for _, field := range fields {
		target.Delete(field)
		target.Delete(field + "_time")
	}
	target.Delete("lead_skip")
	return patchOK(*target)
}

// clearDatelessIntent retires a recurrence and a lead window whose last anchor
// a write removed. Judged after the WHOLE patch, never mid-flight, because a
// change that MOVES the anchor passes through a momentary dateless state.
func clearDatelessIntent(target *record.Record) {
	if target.Truthy("scheduled") || target.Truthy("deadline") {
		return
	}
	target.Delete("recur")
	target.Delete("lead")
}

// patchLead attaches, replaces, or clears the lead-time window. Clearing is
// always allowed — a refusal a user cannot undo is a trap.
func patchLead(records []record.Record, index int, value PatchValue) patchOutcome {
	target := &records[index]
	if value.off() {
		target.Delete("lead")
		target.Delete("lead_skip")
		return patchOK(*target)
	}
	if value.kind != kindText {
		return patchInvalid("invalid lead time " + rubyInspectText(value.Text()) +
			" (expected a span like 3w, 2d, 1m, 1y)")
	}
	// Rule 4: grammar. The canonical span is what reaches the store; friendly
	// phrasings are an adapter's job, so a non-canonical value here is a caller
	// bug, not a user typo.
	if !lead.Span(value.text) {
		return patchInvalid("invalid lead time " + rubyInspectText(value.text) +
			" (expected a span like 3w, 2d, 1m, 1y)")
	}
	// Rule 1: a lead needs an anchor to measure back from.
	deadline, hasDeadline := storedDate(*target, "deadline")
	scheduled, hasScheduled := storedDate(*target, "scheduled")
	anchor, hasAnchor := leadAnchor(deadline, hasDeadline, scheduled, hasScheduled)
	if !hasAnchor {
		return patchInvalid("a lead time needs a date to hide before — " +
			"add a deadline or an available-from date first")
	}
	// Rule 3: one own timed gate.
	if target.Truthy("deadline") && target.Truthy("scheduled") {
		return patchInvalid(leadGateConflictMessage(value.text))
	}
	// Rule 5: the derived gate must stay a storable date.
	gate, ok := lead.DateBound(anchor, value.text)
	if !ok || !validISODate(gate.ISO()) {
		return patchInvalid("a lead of " + humanizeLead(value.text) + " would open before " +
			anchor.ISO() + ", outside the four-digit years dates are stored with")
	}
	target.SetString("lead", value.text)
	// A new window supersedes any occurrence a previous one released early.
	target.Delete("lead_skip")
	return patchOK(*target)
}

// leadGateConflictMessage is Rule 3's one message, shared by the two writes
// that can create the conflict, so the user reads the same fix either way.
func leadGateConflictMessage(span string) string {
	return "a lead time of " + humanizeLead(span) + " hides this task before its date — " +
		"carrying a deadline AND an available-from date beside it would leave a " +
		"second, ignored gate. Clear one of them " +
		"(`tasks undate <ref> --kind scheduled`, or `tasks lead <ref> off`)."
}

func humanizeLead(span string) string {
	if text, ok := lead.Humanize(span); ok {
		return text
	}
	return span
}

func leadAnchor(deadline temporal.Date, hasDeadline bool, scheduled temporal.Date, hasScheduled bool) (temporal.Date, bool) {
	if !hasDeadline {
		deadline = temporal.Date{}
	}
	if !hasScheduled {
		scheduled = temporal.Date{}
	}
	anchor, ok := lead.AnchorDate(deadline, scheduled)
	return anchor, ok
}

func storedDate(parsed record.Record, field string) (temporal.Date, bool) {
	value := parsed.String(field)
	if !validISODate(value) {
		return temporal.Date{}, false
	}
	return temporal.ParseDate(value)
}

func patchRecurrence(records []record.Record, index int, value PatchValue, context patchContext) patchOutcome {
	target := &records[index]
	if !value.off() && contains(check.ProposedStates, target.String("state")) {
		return patchInvalid("can't set recurrence on a PROPOSED task")
	}
	// The anchor requirement holds for CLEARING too: Ruby refuses before it
	// looks at the value, and a dateless task with a stored cookie is exactly
	// the shape `repair` exists for.
	if !target.Truthy("scheduled") && !target.Truthy("deadline") {
		return patchInvalid("recurrence requires a scheduled date or deadline")
	}
	if value.off() {
		target.Delete("recur")
		return patchOK(*target)
	}
	if value.kind != kindText || !recur.Cookie(value.text) {
		return patchInvalid("invalid recurrence cookie")
	}
	anchor, hasAnchor := storedDate(*target, "deadline")
	if !hasAnchor {
		anchor, hasAnchor = storedDate(*target, "scheduled")
	}
	if hasAnchor {
		if reason := unreachableRecurrence(value.text, anchor, context.today); reason != "" {
			return patchInvalid(reason)
		}
	}
	target.SetString("recur", value.text)
	return patchOK(*target)
}

// unreachableRecurrence refuses a cookie that parses cleanly and still leaves a
// task nothing can ever complete: a calendar schedule with no target, or a roll
// past the four-digit years a stored date is written with.
func unreachableRecurrence(cookie string, anchor temporal.Date, today temporal.Date) string {
	date, err := recur.NextDate(cookie, recur.NewCivilDate(int64(anchor.Year), int(anchor.Month), anchor.Day),
		recur.NewCivilDate(int64(today.Year), int(today.Month), today.Day))
	if err != nil {
		return err.Error()
	}
	if validISODate(date.String()) {
		return ""
	}
	return "recurrence would roll to " + date.String() +
		", outside the four-digit years dates are stored with"
}

func patchTagSlice(records []record.Record, index int, value PatchValue, slice PatchField) patchOutcome {
	name := string(slice)
	if value.kind != kindList {
		return patchInvalid(name + " must be a list of tags")
	}
	proposed := value.list
	valid := true
	for _, tag := range proposed {
		if slice == FieldContexts {
			valid = valid && strings.HasPrefix(tag, "@") && len(tag) > 1
			continue
		}
		valid = valid && !strings.HasPrefix(tag, "@") && tag != DeferTag && tag != ""
	}
	if !valid {
		return patchInvalid("invalid " + name + " tag")
	}
	seen := map[string]bool{}
	for _, tag := range proposed {
		if seen[tag] {
			return patchInvalid("duplicate " + name + " tag")
		}
		seen[tag] = true
	}

	owns := func(tag string) bool { return strings.HasPrefix(tag, "@") }
	if slice == FieldTags {
		owns = func(tag string) bool { return !strings.HasPrefix(tag, "@") && tag != DeferTag }
	}
	target := &records[index]
	existing := semanticTags(*target)
	owned := []string{}
	for _, tag := range existing {
		if owns(tag) {
			owned = append(owned, tag)
		}
	}
	if equalStrings(owned, proposed) {
		return patchOK(*target)
	}
	target.SetOptional("tags", record.RawStrings(mergeOwnedSlice(existing, proposed, owns)))
	return patchOK(*target)
}

// mergeOwnedSlice replaces the slice a field owns IN PLACE inside the stored
// tag order, so an unowned tag interleaved between two owned ones keeps its
// position. Rewriting the list as "owned first, then the rest" would reorder
// bytes on every unrelated edit.
func mergeOwnedSlice(existing, proposed []string, owns func(string) bool) []string {
	merged := []string{}
	ownedCount := 0
	for _, tag := range existing {
		if owns(tag) {
			ownedCount++
		}
	}
	ownedIndex := 0
	for _, tag := range existing {
		if !owns(tag) {
			merged = append(merged, tag)
			continue
		}
		if ownedIndex < len(proposed) {
			merged = append(merged, proposed[ownedIndex])
		}
		ownedIndex++
		if ownedIndex == ownedCount && ownedIndex < len(proposed) {
			merged = append(merged, proposed[ownedIndex:]...)
		}
	}
	if ownedCount == 0 {
		merged = append(merged, proposed...)
	}
	return merged
}

// kindTagDelta is the CLI `tag` verb's whole-sequence edit: it may add and
// remove contexts, plain tags, and the defer marker in one undoable write.
func patchTagDelta(records []record.Record, index int, value PatchValue) patchOutcome {
	if value.kind != kindTagDelta {
		return patchInvalid("tag changes must contain add and remove lists")
	}
	target := &records[index]
	tags := []string{}
	for _, tag := range semanticTags(*target) {
		if !contains(value.remove, tag) {
			tags = append(tags, tag)
		}
	}
	for _, tag := range value.add {
		if !contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	target.SetOptional("tags", record.RawStrings(tags))
	return patchOK(*target)
}

// -- recurrence advance --------------------------------------------------------

// advanceRecurrence is what `done` does to a recurring task: it rolls the
// anchor forward instead of closing the task, keeping the paired date's offset,
// noting the completion in the body, and re-arming the delegation and the lead
// window against the new occurrence.
func advanceRecurrence(records []record.Record, index int, context patchContext) patchOutcome {
	target := &records[index]
	cookie := target.String("recur")
	if !recur.Cookie(cookie) {
		return patchInvalid("invalid recurrence cookie")
	}
	deadline, hasDeadline := storedDate(*target, "deadline")
	scheduled, hasScheduled := storedDate(*target, "scheduled")
	if !hasDeadline && !hasScheduled {
		return patchInvalid("recurrence requires a valid date")
	}

	if hasDeadline {
		stamp, _ := temporalFromRecord(*target, "deadline")
		offset := 0
		if hasScheduled {
			offset = scheduled.Sub(deadline)
		}
		veto := func(candidate temporal.Date) bool {
			if !hasScheduled {
				return true
			}
			return temporalCandidateValid(*target, "scheduled", candidate.AddDays(offset), context.temporal)
		}
		next, err := recur.NextTemporalDate(cookie, stamp, recur.Deadline, context.temporal, veto)
		if err != nil {
			return patchInvalid(err.Error())
		}
		if target.Truthy("scheduled") {
			if !hasScheduled {
				return patchInvalid("recurrence requires a valid date")
			}
			target.SetString("scheduled", scheduled.AddDays(next.Sub(deadline)).ISO())
		}
		target.SetString("deadline", next.ISO())
	} else {
		stamp, _ := temporalFromRecord(*target, "scheduled")
		next, err := recur.NextTemporalDate(cookie, stamp, recur.Scheduled, context.temporal, nil)
		if err != nil {
			return patchInvalid(err.Error())
		}
		target.SetString("scheduled", next.ISO())
	}

	target.SetOptional("tags", record.RawStrings(withoutTag(semanticTags(*target), DeferTag)))
	// The roll moved the anchor, so any occurrence released early is history
	// and the lead window re-arms against the new one.
	target.Delete("lead_skip")
	target.SetOptional("body", record.RawString(
		appendBodyLine(target.String("body"), "- Did ["+context.today.ISO()+"].")))
	rollDelegationForward(target, context)
	return patchOK(*target)
}

func temporalCandidateValid(parsed record.Record, field string, date temporal.Date, context temporal.Context) bool {
	raw := fieldRaw(parsed, field+"_time")
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	local, zone, fold := decodeTimeObject(raw)
	value, err := temporal.NewValue(date, local, zone, fold, false)
	if err != nil {
		return false
	}
	if _, err := value.Instant(context); err != nil {
		return false
	}
	return true
}

func appendBodyLine(body, line string) string {
	if body == "" {
		return line
	}
	return body + "\n" + line
}

// -- transaction ---------------------------------------------------------------

// PatchRequest is one field-owned semantic change plus everything the field
// needs to decide it. Field, Value and Expected are the command; Today and
// Context are the ambient facts a temporal field reads.
type PatchRequest struct {
	// ID is the target task's stable id.
	ID string
	// Field is the single field this patch owns.
	Field PatchField
	// Value is the new value, in the field's own shape.
	Value PatchValue
	// Expected is the baseline ExpectedFor produced before the caller decided.
	Expected string
	// Label is the history entry's text; "" takes Ruby's default spelling.
	Label string
	// Today is the ISO day a close stamps and a roll measures from.
	Today string
	// Context is the clock a temporal roll resolves candidates against. The
	// zero value means noon UTC on Today, which is `advance_recurrence_records`'
	// own default.
	Context temporal.Context
	// CoalesceKey groups byte-contiguous edits into one undo step.
	CoalesceKey string
	// Force is `patch_location`'s `force:` — re-append a subtree to the parent
	// it already has instead of treating the move as satisfied. No other field
	// reads it, which is why it is a request flag rather than part of the value.
	Force bool
}

// PatchTask applies one field-owned semantic change in the same transaction
// shape a changeset uses.
//
// `expected` is the patch's narrow conflict check: the value the caller read
// before deciding. It is compared against the value under the write lock, so an
// edit that landed in between refuses instead of silently overwriting.
//
// The string spelling of the value is retained for the fields whose value IS a
// string, with "" meaning nil for the two clearable ones. Everything else goes
// through Patch, which carries the value's real shape.
func (s *Store) PatchTask(id string, field PatchField, value string, expected string, label string, today string) MutationResult {
	return s.Patch(PatchRequest{
		ID: id, Field: field, Value: stringPatchValue(field, value),
		Expected: expected, Label: label, Today: today,
	})
}

// stringPatchValue is the legacy string spelling, kept so a caller that only
// ever sends text does not have to know about PatchValue. A blank string is
// nil for the fields that CAN be cleared and a blank string for the rest,
// which is where each one's own refusal reads it.
func stringPatchValue(field PatchField, value string) PatchValue {
	if value == "" {
		switch field {
		case FieldPriority, FieldRecurrence, FieldLead, FieldScheduled, FieldDeadline, FieldDateClear:
			return NoValue()
		}
	}
	return TextValue(value)
}

// PatchTaskCoalesced is PatchTask with a journal coalesce key.
//
// It exists because a composed operation is TWO writes that have to cost the
// user ONE undo: setting WAITING behind a human delegation, or appending a
// blocker note behind a release. Ruby spells that `coalesce_key:`; without it
// the second write opens its own history step and the user has to undo twice
// to get back to where they started.
func (s *Store) PatchTaskCoalesced(id string, field PatchField, value, expected, label, today,
	coalesceKey string) MutationResult {

	return s.Patch(PatchRequest{
		ID: id, Field: field, Value: stringPatchValue(field, value),
		Expected: expected, Label: label, Today: today, CoalesceKey: coalesceKey,
	})
}

// PatchesField publishes the closed vocabulary, so a caller can ask the side
// that OWNS the set rather than maintaining a mirror of it that goes stale the
// moment a field lands here.
func (s *Store) PatchesField(field PatchField) bool { return patchableFields[field] }

// patchableFields is every field applyFieldPatch can actually write.
var patchableFields = map[PatchField]bool{
	FieldTitle: true, FieldPriority: true, FieldDeferred: true, FieldScheduled: true,
	FieldDeadline: true, FieldRecurrence: true, FieldLead: true, FieldContexts: true,
	FieldTags: true, FieldBody: true, FieldState: true, FieldLocation: true,
	FieldLinks:    true,
	FieldTagDelta: true, FieldActivate: true, FieldDateClear: true,
}

// Patch is the typed entry point. It shares one transaction with every other
// mutation: lock, schema gate, preflight, conflict check, detached apply,
// write, post-write validation, rollback, one journal step, re-read.
func (s *Store) Patch(request PatchRequest) MutationResult {
	var result MutationResult
	err := s.withLock(func() error {
		before := s.fileSnapshot()
		if refusal := s.unsupportedSchemaRefusal(); refusal != nil {
			result = *refusal
			return nil
		}
		repair := false
		preflight := check.Check(s.org)
		if !preflight.OK() {
			// Targeted repair: a field-owned patch may fix its OWN invalid
			// record, but only when every preflight error is attributable to
			// that one record. A baseline built over malformed data is not
			// trustworthy, so the conflict check is skipped in this mode and
			// the post-write validation is the whole of the safety net — it
			// must pass COMPLETELY or the write rolls back.
			repair = repairScope(s.org, preflight, request.ID)
			if !repair {
				messages := []string{}
				for _, entry := range preflight.Errors {
					messages = append(messages, entry.Message)
				}
				result = MutationResult{Status: MutationStoreInvalid, Errors: messages}
				return nil
			}
		}

		records := freshRecords(s.org)
		index := locateStableIndex(records, request.ID)
		if index < 0 {
			result = MutationResult{Status: MutationNotFound}
			return nil
		}
		actual, err := fieldBaseline(records, index, request.Field)
		if err != nil && !repair {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		if !repair && actual != request.Expected {
			result = MutationResult{Status: MutationConflict}
			return nil
		}

		original, err := record.Dump(records)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		working := record.CloneAll(records)
		context, err := s.patchContext(request)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		applied := applyFieldPatch(working, index, request.Field, request.Value, context)
		if applied.status != MutationOK {
			result = MutationResult{
				Status: applied.status, Errors: applied.errors,
				FieldErrors: applied.fieldErrors, Summary: applied.summary,
			}
			return nil
		}
		if containsField(dateOwningFields, request.Field) {
			clearDatelessIntent(&working[index])
		}
		proposed, err := record.Dump(working)
		if err != nil {
			result = MutationResult{Status: MutationInvalid, Errors: []string{err.Error()}}
			return nil
		}
		if proposed == original {
			snapshot, revision := s.readAfterWrite()
			result = MutationResult{
				Status: MutationNoChange, ReadSnapshot: snapshot, StoreRevision: revision,
				Summary: applied.summary,
			}
			return nil
		}

		label := request.Label
		if label == "" {
			label = "edit " + string(request.Field) + ": " + records[index].String("title")
		}
		result = s.commitRepair(before, working, label, request.CoalesceKey, repair)
		if result.Status == MutationOK {
			result.TouchedIDs = applied.touchedIDs
			result.Summary = applied.summary
		}
		return nil
	})
	if err != nil {
		return mutationUnavailable(err)
	}
	return result
}

// patchContext resolves the ambient facts once per transaction. An absent
// Context is noon UTC on Today, which is what `advance_recurrence_records`
// builds when a caller supplies none.
func (s *Store) patchContext(request PatchRequest) (patchContext, error) {
	today, ok := temporal.ParseDate(request.Today)
	if !ok {
		return patchContext{}, &patchError{"today must be an ISO date"}
	}
	context := request.Context
	explicit := context.Timezone != nil
	if context.Timezone == nil {
		built, err := temporal.NewContext(
			time.Date(today.Year, today.Month, today.Day, 12, 0, 0, 0, time.UTC), "Etc/UTC", 24)
		if err != nil {
			return patchContext{}, err
		}
		context = built
	}
	return patchContext{today: today, temporal: context, stamp: DelegationStamp(s.now()),
		explicit: explicit, maxDepth: s.options.MaxDepth, force: request.Force}, nil
}

// ExpectedFor is the baseline a caller reads before proposing a patch. It is
// exported because the conflict check is only meaningful when the SAME
// expression produces the value on both sides of the decision.
func (s *Store) ExpectedFor(id string, field PatchField) (string, bool) {
	var value string
	found := false
	_ = s.withSharedLock(func() error {
		if !check.Check(s.org).OK() {
			return nil
		}
		records := freshRecords(s.org)
		index := locateStableIndex(records, id)
		if index < 0 {
			return nil
		}
		expected, err := fieldBaseline(records, index, field)
		if err != nil {
			return nil
		}
		value, found = expected, true
		return nil
	})
	return value, found
}

// applyFieldPatch dispatches to the one field that owns the change.
//
// The default arm REFUSES. A field the port has not reached must never fall
// through to a write, so it names itself rather than silently doing nothing.
func applyFieldPatch(records []record.Record, index int, field PatchField, value PatchValue,
	context patchContext) patchOutcome {

	switch field {
	case FieldTitle:
		return patchTitle(records, index, value)
	case FieldPriority:
		return patchPriority(records, index, value)
	case FieldDeferred:
		return patchDeferred(records, index, value)
	case FieldActivate:
		return patchActivate(records, index, value, context)
	case FieldScheduled:
		return patchDate(records, index, value, "scheduled")
	case FieldDeadline:
		return patchDate(records, index, value, "deadline")
	case FieldDateClear:
		return patchDateClear(records, index, value)
	case FieldRecurrence:
		return patchRecurrence(records, index, value, context)
	case FieldLead:
		return patchLead(records, index, value)
	case FieldContexts, FieldTags:
		return patchTagSlice(records, index, value, field)
	case FieldTagDelta:
		return patchTagDelta(records, index, value)
	case FieldBody:
		return patchBody(records, index, value)
	case FieldLinks:
		return patchLinks(records, index, value)
	case FieldState:
		return patchState(records, index, value, context)
	case FieldLocation:
		return patchLocation(records, index, value, context, context.placementTargets)
	}
	return patchInvalid("unknown editable field")
}

func containsField(fields []PatchField, field PatchField) bool {
	for _, candidate := range fields {
		if candidate == field {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// rubyStrip is String#strip: ASCII whitespace and NUL only. Go's TrimSpace
// would also remove U+00A0 and the other Unicode spaces, which Ruby keeps — and
// a title is user text, so the difference is observable in stored bytes.
func rubyStrip(value string) string {
	return strings.Trim(value, " \t\n\v\f\r\x00")
}

func writeTemporal(target *record.Record, key string, value temporal.Value) {
	target.SetString(key, value.Date.ISO())
	local, zone, fold, ok := value.TimeMetadata()
	if !ok {
		target.Delete(key + "_time")
		return
	}
	var out bytes.Buffer
	out.WriteString(`{"local":`)
	out.Write(record.RawString(local))
	if zone != "" {
		out.WriteString(`,"timezone":`)
		out.Write(record.RawString(zone))
	}
	if fold == 1 {
		out.WriteString(`,"fold":1`)
	}
	out.WriteByte('}')
	target.Set(key+"_time", json.RawMessage(out.Bytes()))
}

// repairScope is Store#repair_scope?: true when every preflight error belongs
// to the one record this patch targets, which is the only case where a patch is
// allowed to write against a file that does not validate.
//
// The line-1 exclusion is load-bearing. A record on line 1 is the meta record's
// slot, and an error reported there may be about the FILE (a missing or wrong
// meta) rather than about a task, so a task that landed there can never claim
// the whole error set as its own.
func repairScope(path string, preflight check.Result, id string) bool {
	if id == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(raw) {
		return false
	}
	parsed := record.Parse(raw)
	if len(parsed.Errors) > 0 {
		// An unparseable line is not attributable, so nothing is in scope.
		return false
	}
	line := 0
	for _, candidate := range parsed.Records {
		if candidate.String("type") == "task" && candidate.String("id") == id {
			line = candidate.Line
			break
		}
	}
	if line == 0 || line == 1 {
		return false
	}
	for _, entry := range preflight.Errors {
		if entry.Line != line {
			return false
		}
	}
	return true
}
