package record

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const DelegationField = "delegation"

// note is APPENDED last so every record written before it existed keeps its
// exact byte layout.
var DelegationKeyOrder = []string{"kind", "mode", "status", "assignee", "at", "work_ref", "note"}

const (
	delegationAssigneeLimit = 200
	delegationWorkRefLimit  = 500
	// delegationNoteLimit bounds the receiver-facing note. 2000 characters is
	// roughly 300 words: enough for a real briefing (how to work on it, where
	// the work should land, what to avoid) and far more than work_ref's 500,
	// which holds one URL. It stops well short of prose that belongs in the
	// body, which keeps one JSONL line greppable and diffable and keeps the
	// marker a marker.
	delegationNoteLimit = 2000
)

// DelegationNoteLimit is the note bound a refusal quotes.
const DelegationNoteLimit = delegationNoteLimit

// DelegationErrors returns Ruby-compatible schema diagnostics for delegation.
// It validates modes against the BUILT-IN vocabulary; a caller holding a
// configured one calls DelegationErrorsWith instead.
func DelegationErrors(value any) []string {
	return DelegationErrorsWith(value, BuiltinModes())
}

// DelegationStoredErrors validates a marker that is ALREADY ON DISK, which is
// a different question from validating one about to be written.
//
// A marker being written must name a mode the active vocabulary lists: that is
// a refusal, and DelegationErrorsWith is what states it. A marker already in
// the file may name a mode the CURRENT configuration no longer lists — the
// user removed it, or the record came from another machine — and that must not
// invalidate the file. Records like that load, show, and check; the mode comes
// back as a warning so the user can see it and decide, and every other
// delegation rule still applies to it in full.
//
// A mode of the wrong SHAPE stays an error either way: no configuration could
// ever have produced it.
func DelegationStoredErrors(value any, modes ModeVocabulary) (errors []string, warnings []string) {
	modes = Modes(modes)
	errors = DelegationErrorsWith(value, anyWellShapedMode{modes})
	if object, ok := value.(map[string]any); ok {
		if mode, ok := object["mode"].(string); ok && ValidModeName(mode) && !modes.Valid(mode) {
			warnings = append(warnings, fmt.Sprintf(
				"delegation.mode %s is not in the configured vocabulary %s; the record is kept as it is",
				rubyInspect(mode), modes.Quoted()))
		}
	}
	return errors, warnings
}

// DelegationErrorsWith is the same check against a caller-supplied mode
// vocabulary. Nil means the built-in set.
func DelegationErrorsWith(value any, modes ModeVocabulary) []string {
	modes = Modes(modes)
	object, ok := value.(map[string]any)
	if !ok {
		return []string{"delegation must be an object"}
	}
	if len(object) == 0 {
		return []string{"delegation must not be empty"}
	}
	messages := delegationKindErrors(object, modes)
	if !DelegationTimestamp(valueAt(object)) {
		messages = append(messages, fmt.Sprintf("delegation.at %s is not a UTC timestamp (YYYY-MM-DDTHH:MM:SSZ)", rubyInspect(valueAt(object))))
	}
	if reference, present := object["work_ref"]; present {
		messages = append(messages, delegationWorkRefErrors(reference)...)
	}
	if note, present := object["note"]; present {
		messages = append(messages, delegationNoteErrors(note)...)
	}
	return messages
}

func DelegationValid(value any) bool { return len(DelegationErrors(value)) == 0 }
func DelegationObject(value any) bool {
	object, ok := value.(map[string]any)
	return ok && len(object) > 0
}
func DelegationAgent(value any) bool {
	return DelegationObject(value) && value.(map[string]any)["kind"] == "agent"
}
func DelegationHuman(value any) bool {
	return DelegationObject(value) && value.(map[string]any)["kind"] == "human"
}
func DelegationReady(value any) bool {
	return DelegationAgent(value) && value.(map[string]any)["status"] == "ready"
}
func DelegationClaimed(value any) bool {
	return DelegationAgent(value) && value.(map[string]any)["status"] == "claimed"
}

// DelegationOrdered supplies the canonical key order plus unknowns in source order.
// Go maps cannot retain insertion order, so callers that need emission order should
// use DelegationOrderedKeys with the source-field order they already possess.
func DelegationOrderedKeys(value map[string]any, sourceOrder []string) []string {
	keys := make([]string, 0, len(value))
	for _, key := range DelegationKeyOrder {
		if child, ok := value[key]; ok && !delegationAbsent(child) {
			keys = append(keys, key)
		}
	}
	known := make(map[string]bool, len(DelegationKeyOrder))
	for _, key := range DelegationKeyOrder {
		known[key] = true
	}
	for _, key := range sourceOrder {
		if child, ok := value[key]; ok && !known[key] && !delegationAbsent(child) {
			keys = append(keys, key)
		}
	}
	return keys
}

func DelegationUnknownKeys(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return []string{}
	}
	known := make(map[string]bool, len(DelegationKeyOrder))
	for _, key := range DelegationKeyOrder {
		known[key] = true
	}
	keys := make([]string, 0)
	for key := range object {
		if !known[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// delegationStampLayout is the ONE spelling of `delegation.at`: UTC, second
// precision, literal Z. Written here rather than imported so the schema layer
// keeps depending on nothing; temporal.StampLayout is the same string, and the
// projection surfaces read it from there.
const delegationStampLayout = "2006-01-02T15:04:05Z"

func DelegationTimestamp(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) != 20 {
		return false
	}
	parsed, err := time.Parse(delegationStampLayout, text)
	return err == nil && DelegationStamp(parsed) == text
}

func DelegationStamp(value time.Time) string {
	return value.UTC().Format(delegationStampLayout)
}

func delegationKindErrors(object map[string]any, modes ModeVocabulary) []string {
	switch object["kind"] {
	case "human":
		messages := []string{}
		// A mode is OPTIONAL on a human delegation: who holds the work and what
		// kind of delegation it is are orthogonal facts.
		if _, present := object["mode"]; present {
			messages = append(messages, delegationModeErrors(object, modes)...)
		}
		if object["status"] != "delegated" {
			messages = append(messages, fmt.Sprintf("delegation.status %s must be \"delegated\" for a human delegation", rubyInspect(valueAt(object, "status"))))
		}
		if !delegationEmail(valueAt(object, "assignee")) {
			messages = append(messages, fmt.Sprintf("delegation.assignee %s must be an email address (local@domain.tld, no whitespace or control characters, at most 200 chars)", rubyInspect(valueAt(object, "assignee"))))
		}
		return messages
	case "agent":
		messages := []string{}
		messages = append(messages, delegationModeErrors(object, modes)...)
		switch object["status"] {
		case "ready":
			if _, present := object["assignee"]; present {
				messages = append(messages, "delegation.assignee is not allowed while ready")
			}
		case "claimed":
			if !delegationIdentifier(valueAt(object, "assignee")) {
				messages = append(messages, fmt.Sprintf("delegation.assignee %s must be a worker id (non-empty, no whitespace or control characters, at most 200 chars)", rubyInspect(valueAt(object, "assignee"))))
			}
		default:
			messages = append(messages, fmt.Sprintf("delegation.status %s must be ready or claimed for an agent delegation", rubyInspect(valueAt(object, "status"))))
		}
		return messages
	default:
		return []string{fmt.Sprintf("delegation.kind %s must be human or agent", rubyInspect(valueAt(object, "kind")))}
	}
}

// delegationModeErrors is the ONE place a mode is checked for membership. It
// asks the vocabulary seam; there is no literal list here.
func delegationModeErrors(object map[string]any, modes ModeVocabulary) []string {
	modes = Modes(modes)
	if mode, ok := object["mode"].(string); !ok || !modes.Valid(mode) {
		return []string{fmt.Sprintf("delegation.mode %s must be one of %s",
			rubyInspect(valueAt(object, "mode")), modes.Quoted())}
	}
	return nil
}

// DelegationNoteErrors validates one note the way DelegationWorkRefErrors
// validates one reference: BEFORE a marker is built, so the writer reads the
// note's own problem rather than a whole-marker shape report.
func DelegationNoteErrors(value any) []string { return delegationNoteErrors(value) }

func delegationNoteErrors(value any) []string {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return []string{"delegation.note must be a non-empty string"}
	}
	// Newlines are allowed — a briefing has paragraphs, and JSONL escapes them.
	// Every other control character is refused at the schema boundary, because
	// the note is rendered raw by show, the TUI panel, and agent prompts.
	if strings.IndexFunc(text, func(r rune) bool {
		return r != '\n' && (r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029)
	}) >= 0 {
		return []string{"delegation.note must not contain control characters"}
	}
	if utf8.RuneCountInString(text) > delegationNoteLimit {
		return []string{fmt.Sprintf("delegation.note must be at most %d characters (got %d)",
			delegationNoteLimit, utf8.RuneCountInString(text))}
	}
	return nil
}

func valueAt(object map[string]any, keys ...string) any {
	if len(keys) == 0 {
		return object["at"]
	}
	return object[keys[0]]
}
func delegationIdentifier(value any) bool {
	text, ok := value.(string)
	return ok && utf8.ValidString(text) && text != "" && utf8.RuneCountInString(text) <= delegationAssigneeLimit && strings.IndexFunc(text, delegationForbiddenIdentifier) < 0
}
func delegationForbiddenIdentifier(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f) || unicode.IsSpace(r)
}
func delegationEmail(value any) bool {
	text, ok := value.(string)
	if !ok || !delegationIdentifier(text) {
		return false
	}
	at := strings.Split(text, "@")
	if len(at) != 2 || at[0] == "" {
		return false
	}
	labels := strings.Split(at[1], ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
	}
	return true
}

// DelegationWorkRefErrors is Delegation.work_ref_errors for one reference. The
// work-ref writer validates BEFORE it builds a marker, so the user reads the
// reference's own problem rather than a whole-marker shape report.
func DelegationWorkRefErrors(value any) []string { return delegationWorkRefErrors(value) }

func delegationWorkRefErrors(value any) []string {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return []string{"delegation.work_ref must be a non-empty string"}
	}
	if strings.ContainsAny(text, "\r\n\u2028\u2029") {
		return []string{"delegation.work_ref must be a single line"}
	}
	if strings.IndexFunc(text, func(r rune) bool { return r <= 0x1f || (r >= 0x7f && r <= 0x9f) }) >= 0 {
		return []string{"delegation.work_ref must not contain control characters"}
	}
	if utf8.RuneCountInString(text) > delegationWorkRefLimit {
		return []string{fmt.Sprintf("delegation.work_ref must be at most 500 characters (got %d)", utf8.RuneCountInString(text))}
	}
	return nil
}
func delegationAbsent(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return typed == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// rubyInspect is deliberately narrow: JSON values are enough for this schema.
func rubyInspect(value any) string {
	switch typed := value.(type) {
	case nil:
		return "nil"
	case string:
		return rubyQuote(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case []any:
		values := make([]string, len(typed))
		for i := range typed {
			values[i] = rubyInspect(typed[i])
		}
		return "[" + strings.Join(values, ", ") + "]"
	case map[string]any:
		return "{}"
	default:
		return fmt.Sprint(typed)
	}
}
func rubyQuote(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	if !utf8.ValidString(text) {
		for _, byteValue := range []byte(text) {
			if byteValue >= 0x20 && byteValue < 0x7f && byteValue != '\\' && byteValue != '"' {
				out.WriteByte(byteValue)
			} else {
				fmt.Fprintf(&out, "\\x%02X", byteValue)
			}
		}
		out.WriteByte('"')
		return out.String()
	}
	for _, r := range text {
		switch r {
		case '\\':
			out.WriteString("\\\\")
		case '"':
			out.WriteString("\\\"")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		case 0x1b:
			out.WriteString("\\e")
		default:
			if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
				fmt.Fprintf(&out, "\\u%04X", r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// The three predicates the store's plan functions need by name. They are
// exported here rather than reimplemented there because a second spelling of
// "is this an identifier" is a second answer waiting to diverge.

// DelegationIdentifier is Delegation.identifier?: real UTF-8, non-empty,
// bounded, no whitespace in any script, no control or escape characters.
func DelegationIdentifier(value any) bool { return delegationIdentifier(value) }

// DelegationEmail is Delegation.email? — an identifier that is also
// address-SHAPED, so a stray `@work` cannot become a person a task waits on.
func DelegationEmail(value any) bool { return delegationEmail(value) }

// RubyInspect renders a value the way a Ruby diagnostic quotes it, which is
// what every delegation refusal's wording depends on.
func RubyInspect(value any) string { return rubyInspect(value) }
