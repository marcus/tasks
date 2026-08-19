package check

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/updatestamp"
)

// The vocabularies Check enforces, spelled where lib/tasks/check.rb spells them.
var (
	ProposedStates = []string{"PROPOSED"}
	OpenStates     = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	ClosedStates   = []string{"DONE", "CANCELLED"}
	States         = append(append(append([]string{}, ProposedStates...), OpenStates...), ClosedStates...)
	Priorities     = []string{"A", "B", "C"}
	Types          = []string{"meta", "section", "task"}
)

// sectionForbidden is the task-only semantics a section record must not carry.
// `archived` is deliberately absent: a swept subtree root can be a section.
var sectionForbidden = []string{
	"state", "priority", "scheduled", "scheduled_time", "deadline", "deadline_time",
	"recur", "lead", "lead_skip", "delegation", "closed", "rejected", "tags",
	"links",
}

// knownKeys is every key the schema knows, plus the two out-of-band ones: the
// parser's line stamp and meta's version.
var knownKeys = func() map[string]bool {
	keys := map[string]bool{"line": true, "version": true}
	for _, key := range record.KeyOrder {
		keys[key] = true
	}
	return keys
}()

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// validate is the whole of Check.check_parsed beyond the metadata and id rules
// checkMeta/checkID already own: keys, tree shape, task fields, section fields,
// and the two hazards that are warnings rather than errors.
func validate(records []record.Record, errors *[]Entry, warnings *[]Entry, duplicates *duplicateIndex, options Options) {
	seen := map[string]bool{}
	openTitles := map[string][]int{}
	titleOrder := []string{}
	stack := []identity{}

	for _, parsed := range records {
		line := parsed.Line
		typeName, typeIsString := decodeString(rawField(parsed, "type"))

		// meta is only valid on line 1 (checkMeta owns line 1); a later one is
		// an error, and it is never validated as a section or a task.
		if typeIsString && typeName == "meta" {
			if line != 1 {
				*errors = append(*errors, Entry{Line: line, Message: "unexpected meta record (only valid on line 1)"})
			}
			continue
		}
		if !typeIsString || !contains(Types, typeName) {
			*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("unknown record type %s", rubyInspect(rawField(parsed, "type")))})
			continue
		}

		checkID(parsed, errors, duplicates)
		checkKeys(parsed, warnings)
		stack = checkParent(parsed, seen, stack, errors)

		if typeName == "task" {
			checkTask(parsed, errors, options)
			if state := stringField(parsed, "state"); contains(OpenStates, state) {
				title := query.Downcase(stringField(parsed, "title"))
				if _, exists := openTitles[title]; !exists {
					titleOrder = append(titleOrder, title)
				}
				openTitles[title] = append(openTitles[title], line)
			}
		} else {
			checkSection(parsed, errors)
		}

		if id, ok := identityOf(rawField(parsed, "id")); ok {
			seen[id.key] = true
		}
	}

	for _, title := range titleOrder {
		lines := openTitles[title]
		if len(lines) < 2 {
			continue
		}
		*warnings = append(*warnings, Entry{
			Line: lines[len(lines)-1],
			Message: fmt.Sprintf("duplicate open title %s (lines %s) — fuzzy refs will be ambiguous",
				rubyInspectString(title), joinLines(lines)),
		})
	}
}

// identity is a record's id or parent as a comparable value. Ruby compares the
// parsed JSON values directly; the canonical spelling of the value is the same
// equivalence for every shape a store can hold.
type identity struct {
	key     string
	present bool
}

func identityOf(raw json.RawMessage) (identity, bool) {
	// Ruby's `if r["id"]` is truthiness: nil and false are absent.
	if raw == nil {
		return identity{}, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" || trimmed == "false" {
		return identity{}, false
	}
	if value, ok := decodeString(raw); ok {
		return identity{key: "s:" + value, present: true}, true
	}
	return identity{key: "j:" + trimmed, present: true}, true
}

// checkParent enforces both tree invariants at once: a parent must name an
// EARLIER record, and the record must sit in DFS pre-order beneath it. The
// second is what keeps the file's line order and the tree's shape the same
// fact, so a reader never has to reconstruct nesting by scanning.
func checkParent(parsed record.Record, seen map[string]bool, stack []identity, errors *[]Entry) []identity {
	parentRaw := rawField(parsed, "parent")
	id, _ := identityOf(rawField(parsed, "id"))
	if parentRaw == nil || strings.TrimSpace(string(parentRaw)) == "null" {
		return []identity{id}
	}
	parent, _ := identityOf(parentRaw)
	if !seen[parent.key] {
		*errors = append(*errors, Entry{Line: parsed.Line,
			Message: fmt.Sprintf("parent %s does not resolve to an earlier record", rubyInspect(parentRaw))})
		return stack
	}
	next := append([]identity{}, stack...)
	for len(next) > 0 && next[len(next)-1].key != parent.key {
		next = next[:len(next)-1]
	}
	if len(next) > 0 && next[len(next)-1].key == parent.key {
		return append(next, id)
	}
	*errors = append(*errors, Entry{Line: parsed.Line,
		Message: fmt.Sprintf("record %s breaks DFS pre-order (parent %s is not an open ancestor)",
			rubyInspect(rawField(parsed, "id")), rubyInspect(parentRaw))})
	return next
}

// checkKeys is the forward-compatibility seam: a key this binary does not know
// is a WARNING, not an error, because the write path preserves it. The same
// rule reaches inside the delegation object.
func checkKeys(parsed record.Record, warnings *[]Entry) {
	for _, field := range parsed.Fields {
		if !knownKeys[field.Key] {
			*warnings = append(*warnings, Entry{Line: parsed.Line,
				Message: fmt.Sprintf("unknown key %s", rubyInspectString(field.Key))})
		}
	}
	for _, key := range delegationUnknownKeys(rawField(parsed, record.DelegationField)) {
		*warnings = append(*warnings, Entry{Line: parsed.Line,
			Message: fmt.Sprintf("unknown delegation key %s", rubyInspectString(key))})
	}
}

// delegationUnknownKeys reports unknown delegation members in SOURCE order.
// record.DelegationUnknownKeys sorts them, which is right for a set and wrong
// for a diagnostic stream compared byte for byte against Ruby's Hash order.
func delegationUnknownKeys(raw json.RawMessage) []string {
	if raw == nil {
		return nil
	}
	fields, err := record.Fields(raw)
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, key := range record.DelegationKeyOrder {
		known[key] = true
	}
	unknown := []string{}
	for _, field := range fields {
		if !known[field.Key] {
			unknown = append(unknown, field.Key)
		}
	}
	return unknown
}

func checkTask(parsed record.Record, errors *[]Entry, options Options) {
	line := parsed.Line
	add := func(message string) { *errors = append(*errors, Entry{Line: line, Message: message}) }

	state, stateIsString := decodeString(rawField(parsed, "state"))
	if !stateIsString || !contains(States, state) {
		add(fmt.Sprintf("invalid state %s (expected %s)", rubyInspect(rawField(parsed, "state")), strings.Join(States, "/")))
	}
	if priorityRaw := rawField(parsed, "priority"); truthy(priorityRaw) {
		if priority, ok := decodeString(priorityRaw); !ok || !contains(Priorities, priority) {
			add(fmt.Sprintf("invalid priority %s (expected A, B, or C)", rubyInspect(priorityRaw)))
		}
	}
	titleRaw := rawField(parsed, "title")
	title, titleIsString := decodeString(titleRaw)
	switch {
	case titleRaw == nil || strings.TrimSpace(string(titleRaw)) == "null":
		add("task has no title")
	case titleIsString && strings.TrimSpace(title) == "":
		add("task has no title")
	case !titleIsString:
		add("title must be a string")
	}
	for _, key := range []string{"scheduled", "deadline", "closed", "rejected"} {
		checkDate(parsed, key, errors)
	}
	checkTemporalTime(parsed, "scheduled", errors)
	checkTemporalTime(parsed, "deadline", errors)
	checkDate(parsed, "archived", errors)

	if recurRaw := rawField(parsed, "recur"); truthy(recurRaw) {
		cookie, ok := decodeString(recurRaw)
		if !ok || cookie != strings.TrimSpace(cookie) || !recur.Cookie(cookie) {
			add(fmt.Sprintf("invalid recur cookie %s (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)",
				rubyInspect(recurRaw)))
		}
		// Recurrence is a schedule for ACCEPTED work: every write path already
		// refuses to put a cookie on an undecided proposal, or to propose a
		// recurring task. Stating it here too closes the hole a hand-edited,
		// repaired, or foreign-device file leaves — a shape no write could
		// produce and no operation can act on coherently (completing it rolls
		// the anchor forward instead of finishing anything).
		if contains(ProposedStates, state) {
			add(fmt.Sprintf("recurrence on a proposed task (%s)", state))
		}
	}
	checkLead(parsed, errors)

	closedRaw := rawField(parsed, "closed")
	if truthy(closedRaw) {
		if contains(OpenStates, state) {
			add(fmt.Sprintf("closed date on an open task (%s)", state))
		} else if contains(ProposedStates, state) {
			add(fmt.Sprintf("closed date on a proposed task (%s)", state))
		}
	}
	// `rejected` is the declined-proposal marker: the day a PROPOSED task was
	// declined, written in the same transaction as the CANCELLED state. It is
	// what tells a decline apart from an ordinary cancellation, so it is only
	// meaningful on a cancelled task — a marker left behind on any other state
	// would make `list --rejected` claim a decline that did not happen.
	if truthy(rawField(parsed, "rejected")) && state != "CANCELLED" {
		add(fmt.Sprintf("rejected date on a task that is not CANCELLED (%s)", state))
	}
	if tagsRaw := rawField(parsed, "tags"); truthy(tagsRaw) {
		var tags []json.RawMessage
		if json.Unmarshal(tagsRaw, &tags) != nil {
			add("tags must be an array")
		} else {
			for _, tag := range tags {
				if _, ok := decodeString(tag); !ok {
					add("tags must all be strings")
					break
				}
			}
		}
	}
	if updated := rawField(parsed, "updated"); updated != nil {
		if value, ok := decodeString(updated); !ok || !updatestamp.Valid(value) {
			add(fmt.Sprintf("updated %s is not an RFC3339 UTC timestamp with device slug", rubyInspect(updated)))
		}
	}
	checkDelegation(parsed, errors, options)
	checkLinks(parsed, errors)
}

func checkLinks(parsed record.Record, errors *[]Entry) {
	raw := rawField(parsed, "links")
	if raw == nil || strings.TrimSpace(string(raw)) == "null" {
		return
	}
	add := func(message string) { *errors = append(*errors, Entry{Line: parsed.Line, Message: message}) }
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		add("links must be an array")
		return
	}
	if len(entries) > links.MaxFormalLinks {
		add(fmt.Sprintf("links has %d entries (maximum %d)", len(entries), links.MaxFormalLinks))
	}
	seen := map[string]bool{}
	for index, entry := range entries {
		trimmed := strings.TrimSpace(string(entry))
		fields, fieldsErr := record.Fields(entry)
		if fieldsErr != nil || len(trimmed) == 0 || trimmed[0] != '{' {
			add(fmt.Sprintf("links[%d] must be an object", index))
			continue
		}
		object := make(map[string]json.RawMessage, len(fields))
		for _, field := range fields {
			if _, present := object[field.Key]; !present {
				object[field.Key] = field.Value
			}
			if field.Key != "url" && field.Key != "label" {
				add(fmt.Sprintf("links[%d] has unknown key %s", index, rubyInspectString(field.Key)))
			}
		}
		rawURL, ok := decodeString(object["url"])
		switch {
		case !ok:
			add(fmt.Sprintf("links[%d].url must be a string", index))
		case rawURL == "" || rawURL != strings.TrimSpace(rawURL):
			add(fmt.Sprintf("links[%d].url must be a non-empty canonical URL", index))
		case len(rawURL) > links.MaxURLLength:
			add(fmt.Sprintf("links[%d].url exceeds %d bytes", index, links.MaxURLLength))
		case links.UnsafeFormalText(rawURL):
			add(fmt.Sprintf("links[%d].url must be a single line without control characters", index))
		default:
			if !links.ValidFormalURL(rawURL) {
				add(fmt.Sprintf("links[%d].url must be an http or https URL with a host", index))
			} else if seen[rawURL] {
				add(fmt.Sprintf("links[%d].url duplicates an earlier formal link", index))
			} else {
				seen[rawURL] = true
			}
		}
		if labelRaw, present := object["label"]; present {
			label, labelOK := decodeString(labelRaw)
			switch {
			case !labelOK:
				add(fmt.Sprintf("links[%d].label must be a string", index))
			case label == "" || label != strings.TrimSpace(label):
				add(fmt.Sprintf("links[%d].label must be non-empty and trimmed", index))
			case len(label) > links.MaxLabelLength:
				add(fmt.Sprintf("links[%d].label exceeds %d bytes", index, links.MaxLabelLength))
			case links.UnsafeFormalText(label):
				add(fmt.Sprintf("links[%d].label must be a single line without control characters", index))
			}
		}
	}
}

// checkLead validates the lead-time pair. `lead` is a canonical span;
// `lead_skip` is the anchor date one `activate` already released, so it needs
// an anchor to name. The two shapes a lead cannot mean anything in — no date to
// measure back from, and two dates that already express their own window — are
// refused at write time, but a hand edit can still produce them, and a linter
// that stayed silent would leave a task hidden by a rule no surface explains.
func checkLead(parsed record.Record, errors *[]Entry) {
	line := parsed.Line
	add := func(message string) { *errors = append(*errors, Entry{Line: line, Message: message}) }
	hasScheduled := truthy(rawField(parsed, "scheduled"))
	hasDeadline := truthy(rawField(parsed, "deadline"))

	if leadRaw := rawField(parsed, "lead"); truthy(leadRaw) {
		span, ok := decodeString(leadRaw)
		if ok && span == strings.TrimSpace(span) && lead.Span(span) {
			if !hasScheduled && !hasDeadline {
				add(fmt.Sprintf("lead %s with no scheduled date or deadline to hide before", rubyInspect(leadRaw)))
			}
			if hasScheduled && hasDeadline {
				add(fmt.Sprintf("lead %s beside both a scheduled date and a deadline "+
					"(the two dates already express that window)", rubyInspect(leadRaw)))
			}
		} else {
			add(fmt.Sprintf("invalid lead %s (expected a span like 3w, 2d, 1m, 1y, 5h)", rubyInspect(leadRaw)))
		}
	}
	if !truthy(rawField(parsed, "lead_skip")) {
		return
	}
	checkDate(parsed, "lead_skip", errors)
	if !truthy(rawField(parsed, "lead")) {
		add("lead_skip without a lead to release")
	}
	if !hasScheduled && !hasDeadline {
		add("lead_skip without a scheduled date or deadline to release")
	}
}

// checkDelegation validates the marker's own shape plus the one lifecycle rule
// the object cannot state about itself: approval and delegation are
// independent owner decisions, so an undecided proposal never carries one.
func checkDelegation(parsed record.Record, errors *[]Entry, options Options) {
	raw := rawField(parsed, record.DelegationField)
	if raw == nil || strings.TrimSpace(string(raw)) == "null" {
		return
	}
	line := parsed.Line
	if state := stringField(parsed, "state"); contains(ProposedStates, state) {
		*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("delegation on a proposed task (%s)", state)})
	}
	for _, message := range record.DelegationErrorsWith(decodeAny(raw), options.Modes) {
		*errors = append(*errors, Entry{Line: line, Message: message})
	}
}

func checkSection(parsed record.Record, errors *[]Entry) {
	line := parsed.Line
	titleRaw := rawField(parsed, "title")
	title, isString := decodeString(titleRaw)
	if !truthy(titleRaw) || (isString && strings.TrimSpace(title) == "") ||
		(!isString && strings.TrimSpace(rubyToS(titleRaw)) == "") {
		*errors = append(*errors, Entry{Line: line, Message: "section has no title"})
	}
	for _, key := range sectionForbidden {
		if truthy(rawField(parsed, key)) {
			*errors = append(*errors, Entry{Line: line,
				Message: fmt.Sprintf("section must not carry %s", rubyInspectString(key))})
		}
	}
	checkDate(parsed, "archived", errors)
}

func checkDate(parsed record.Record, key string, errors *[]Entry) {
	raw := rawField(parsed, key)
	if !truthy(raw) {
		return
	}
	value, isString := decodeString(raw)
	if !isString || !storedDate.MatchString(value) {
		*errors = append(*errors, Entry{Line: parsed.Line,
			Message: fmt.Sprintf("%s %s is not a YYYY-MM-DD date", key, rubyInspect(raw))})
		return
	}
	if _, ok := temporal.ParseDate(value); !ok {
		*errors = append(*errors, Entry{Line: parsed.Line,
			Message: fmt.Sprintf("%s %s is not a real date", key, value)})
	}
}

// checkTemporalTime validates a `<field>_time` object against the date it
// qualifies. A time with no date is the one shape that is always meaningless,
// so it is reported before the object's own members are looked at.
func checkTemporalTime(parsed record.Record, field string, errors *[]Entry) {
	key := field + "_time"
	raw := rawField(parsed, key)
	if !truthy(raw) {
		return
	}
	line := parsed.Line
	add := func(message string) { *errors = append(*errors, Entry{Line: line, Message: message}) }
	if !truthy(rawField(parsed, field)) {
		add(fmt.Sprintf("%s requires %s", key, field))
		return
	}
	fields, err := record.Fields(raw)
	if err != nil {
		add(fmt.Sprintf("%s must be an object", key))
		return
	}
	unknown := []string{}
	for _, member := range fields {
		if member.Key != "local" && member.Key != "timezone" && member.Key != "fold" {
			unknown = append(unknown, member.Key)
		}
	}
	if len(unknown) > 0 {
		add(fmt.Sprintf("%s has unknown keys: %s", key, strings.Join(unknown, ", ")))
	}
	member := func(name string) json.RawMessage {
		for _, candidate := range fields {
			if candidate.Key == name {
				return candidate.Value
			}
		}
		return nil
	}
	local, localIsString := decodeString(member("local"))
	if !localIsString || !temporal.ValidLocal(local) {
		add(fmt.Sprintf("%s.local must be HH:MM", key))
		return
	}
	fold := 0
	if foldRaw := member("fold"); foldRaw != nil {
		if value, ok := strictInteger(foldRaw); ok && value == 1 {
			fold = 1
		} else {
			add(fmt.Sprintf("%s.fold must be omitted or 1", key))
		}
	}
	zoneRaw := member("timezone")
	if !truthy(zoneRaw) {
		return
	}
	zone, _ := decodeString(zoneRaw)
	date, dateOK := temporal.ParseDate(stringField(parsed, field))
	if !dateOK {
		// checkDate already reported the date; a second diagnostic derived from
		// it would double-report one defect.
		return
	}
	if _, err := temporal.NewValue(date, local, zone, fold, true); err != nil {
		add(fmt.Sprintf("%s %s", key, err.Error()))
	}
}

// storedDate is Check::DATE_RE: the stored spelling, before the date is
// checked for naming a day the calendar has.
var storedDate = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}\z`)

// truthy is Ruby's `if r[key]`: present, not null, not false.
func truthy(raw json.RawMessage) bool {
	if raw == nil {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "null" && trimmed != "false"
}

func decodeAny(raw json.RawMessage) any {
	var value any
	if raw == nil || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

// rubyToS renders a non-string JSON value the way Ruby's to_s does for the one
// place Check calls it: a section title that is not a string.
func rubyToS(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	if value, ok := decodeString(raw); ok {
		return value
	}
	return strings.TrimSpace(string(raw))
}
