package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tasks-go/internal/record"
)

// The per-record task rules: the state and priority vocabularies, the title,
// the date fields, the recurrence cookie, closed-on-open, the tag shape, and
// the update stamp — plus the two warning channels, unknown keys and duplicate
// open titles.
//
// Three of Check#check_task's rules are deliberately absent: check_lead,
// check_temporal_time, and check_delegation belong to other slices, and
// emitting their diagnostics here would claim behavior this slice has not
// characterized. Their absence changes no message this file does emit, because
// Ruby appends them in the same per-record pass and the report is sorted by
// line afterwards.
//
// Every rule type-guards before it inspects. Ruby's Check runs inside
// with_history AFTER a write, so a raise on malformed data would bypass the
// rollback; a bad type is always reported, never fatal.

var (
	proposedStates = []string{"PROPOSED"}
	openStates     = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	closedStates   = []string{"DONE", "CANCELLED"}
	states         = append(append(append([]string{}, proposedStates...), openStates...), closedStates...)
	priorities     = []string{"A", "B", "C"}
)

var datePattern = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}\z`)

// knownKeys is Format::KEY_ORDER plus the out-of-band line stamp and the meta
// version. The Go parser keeps the line number beside the record rather than
// in it, so "line" can never appear as a field; it stays listed because the
// vocabulary, not the parser's representation, is what Ruby compares against.
var knownKeys = buildKnownKeys()

func buildKnownKeys() map[string]bool {
	keys := map[string]bool{record.LineKey: true, "version": true}
	for _, key := range record.KeyOrder {
		keys[key] = true
	}
	return keys
}

// checkKeys warns about any key outside the schema, at the top level and
// inside the delegation object. A newer binary's key is preserved on write
// rather than dropped, so it is a hazard to flag, not a file to fail.
func checkKeys(parsed record.Record, warnings *[]Entry) {
	for _, field := range parsed.Fields {
		if !knownKeys[field.Key] {
			*warnings = append(*warnings, Entry{Line: parsed.Line, Message: "unknown key " + rubyInspectString(field.Key)})
		}
	}
	for _, key := range record.DelegationUnknownKeys(decodeValue(rawField(parsed, record.DelegationField))) {
		*warnings = append(*warnings, Entry{Line: parsed.Line, Message: "unknown delegation key " + rubyInspectString(key)})
	}
}

func checkTask(parsed record.Record, errors *[]Entry) {
	line := parsed.Line
	state := rawField(parsed, "state")
	if stateName, ok := decodeString(state); !ok || !contains(states, stateName) {
		*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("invalid state %s (expected %s)", rubyInspect(state), strings.Join(states, "/"))})
	}
	priority := rawField(parsed, "priority")
	if truthy(priority) {
		if name, ok := decodeString(priority); !ok || !contains(priorities, name) {
			*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("invalid priority %s (expected A, B, or C)", rubyInspect(priority))})
		}
	}
	title := rawField(parsed, "title")
	titleText, titleIsString := decodeString(title)
	switch {
	case isNil(title) || (titleIsString && rubyStrip(titleText) == ""):
		*errors = append(*errors, Entry{Line: line, Message: "task has no title"})
	case !titleIsString:
		*errors = append(*errors, Entry{Line: line, Message: "title must be a string"})
	}
	for _, key := range []string{"scheduled", "deadline", "closed"} {
		checkDate(parsed, key, errors)
	}
	// check_temporal_time for scheduled and deadline sits here in Ruby; it is
	// the campaign 5 grammar, not this slice's.
	checkDate(parsed, "archived", errors)
	// The stored value is the exact canonical spelling, so the padding guard
	// comes before the grammar: Recur tolerates surrounding whitespace on
	// input and a stored value must not carry any.
	if recur := rawField(parsed, "recur"); truthy(recur) {
		text, isString := decodeString(recur)
		if !isString || text != rubyStrip(text) || !recurCookie(text) {
			*errors = append(*errors, Entry{
				Line:    line,
				Message: fmt.Sprintf("invalid recur cookie %s (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)", rubyInspect(recur)),
			})
		}
	}
	// check_lead sits here in Ruby; it is the campaign 5 span grammar.
	if truthy(rawField(parsed, "closed")) {
		stateName, _ := decodeString(state)
		switch {
		case contains(openStates, stateName):
			*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("closed date on an open task (%s)", rubyToS(state))})
		case contains(proposedStates, stateName):
			*errors = append(*errors, Entry{Line: line, Message: fmt.Sprintf("closed date on a proposed task (%s)", rubyToS(state))})
		}
	}
	tags := rawField(parsed, "tags")
	tagList, tagsAreArray := decodeArray(tags)
	switch {
	case truthy(tags) && !tagsAreArray:
		*errors = append(*errors, Entry{Line: line, Message: "tags must be an array"})
	case tagsAreArray && anyNonString(tagList):
		*errors = append(*errors, Entry{Line: line, Message: "tags must all be strings"})
	}
	if updated := rawField(parsed, "updated"); updated != nil {
		if !updateStampValid(updated) {
			*errors = append(*errors, Entry{
				Line:    line,
				Message: fmt.Sprintf("updated %s is not an RFC3339 UTC timestamp with device slug", rubyInspect(updated)),
			})
		}
	}
	// check_delegation closes check_task in Ruby; the delegation shape rules
	// belong to the delegation-record-shape slice.
}

// checkDate accepts only a real YYYY-MM-DD calendar date. The two rejections
// differ in more than wording: the shape error quotes the value with inspect
// while the calendar error, reached only for a well-formed string, does not.
func checkDate(parsed record.Record, key string, errors *[]Entry) {
	raw := rawField(parsed, key)
	if !truthy(raw) {
		return
	}
	text, isString := decodeString(raw)
	if !isString || !datePattern.MatchString(text) {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: fmt.Sprintf("%s %s is not a YYYY-MM-DD date", key, rubyInspect(raw))})
		return
	}
	year := atoiFixed(text[0:4])
	month := atoiFixed(text[5:7])
	day := atoiFixed(text[8:10])
	if !validDate(year, month, day) {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: fmt.Sprintf("%s %s is not a real date", key, text)})
	}
}

var updateStampPattern = regexp.MustCompile(`\A(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})Z#([a-z0-9]+)\z`)

// updateStampValid is UpdateStamp.valid?. Ruby validates the timestamp half
// with Time.iso8601, which range-checks the components without demanding a
// real calendar date: 2026-02-31 is accepted (Time rolls it over) while month
// 13 is not, hour 24 is legal only at exactly 24:00:00, and second 60 is
// allowed for leap seconds.
func updateStampValid(raw json.RawMessage) bool {
	value, ok := decodeString(raw)
	if !ok {
		return false
	}
	match := updateStampPattern.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	month := atoiFixed(match[2])
	day := atoiFixed(match[3])
	hour := atoiFixed(match[4])
	minute := atoiFixed(match[5])
	second := atoiFixed(match[6])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	if hour > 24 || (hour == 24 && (minute != 0 || second != 0)) {
		return false
	}
	return minute <= 59 && second <= 60
}

// duplicateOpenTitles warns where two open tasks share a title: fuzzy refs
// resolve by title, so the pair makes every such ref ambiguous. Only the open
// states participate — a done or proposed twin is ordinary history.
type duplicateOpenTitles struct {
	order []string
	lines map[string][]int
}

func newDuplicateOpenTitles() *duplicateOpenTitles {
	return &duplicateOpenTitles{lines: map[string][]int{}}
}

func (d *duplicateOpenTitles) observe(parsed record.Record) {
	stateName, _ := decodeString(rawField(parsed, "state"))
	if !contains(openStates, stateName) {
		return
	}
	title := strings.ToLower(rubyToS(rawField(parsed, "title")))
	if _, seen := d.lines[title]; !seen {
		d.order = append(d.order, title)
	}
	d.lines[title] = append(d.lines[title], parsed.Line)
}

func (d *duplicateOpenTitles) warnings() []Entry {
	entries := make([]Entry, 0)
	for _, title := range d.order {
		lines := d.lines[title]
		if len(lines) < 2 {
			continue
		}
		entries = append(entries, Entry{
			Line:    lines[len(lines)-1],
			Message: fmt.Sprintf("duplicate open title %s (lines %s) — fuzzy refs will be ambiguous", rubyInspectString(title), joinLines(lines)),
		})
	}
	return entries
}

func contains(vocabulary []string, value string) bool {
	for _, candidate := range vocabulary {
		if candidate == value {
			return true
		}
	}
	return false
}

// truthy is Ruby's notion of it: only nil and false are falsey, so a present
// but empty string or a zero is a value the rule still has to judge.
func truthy(raw json.RawMessage) bool {
	return raw != nil && !isNil(raw) && !bytes.Equal(raw, []byte("false"))
}

func isNil(raw json.RawMessage) bool {
	return raw == nil || bytes.Equal(raw, []byte("null"))
}

func decodeArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if raw == nil || len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return items, true
}

func anyNonString(items []json.RawMessage) bool {
	for _, item := range items {
		if _, ok := decodeString(item); !ok {
			return true
		}
	}
	return false
}

func decodeValue(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

// rubyToS is Object#to_s for the values JSON can carry: nil renders empty, a
// string renders itself, and every other shape renders as it inspects.
func rubyToS(raw json.RawMessage) string {
	if isNil(raw) {
		return ""
	}
	if text, ok := decodeString(raw); ok {
		return text
	}
	return rubyInspect(raw)
}

// rubyStrip is String#strip, which removes NUL as well as ASCII whitespace.
func rubyStrip(value string) string {
	return strings.Trim(value, "\x00\t\n\v\f\r ")
}

func atoiFixed(digits string) int {
	value := 0
	for index := 0; index < len(digits); index++ {
		value = value*10 + int(digits[index]-'0')
	}
	return value
}

// validDate is Date.valid_date? for the proleptic Gregorian calendar Ruby's
// Date uses by default over the range a stored date can express.
func validDate(year, month, day int) bool {
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	stamp := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return stamp.Year() == year && int(stamp.Month()) == month && stamp.Day() == day
}
