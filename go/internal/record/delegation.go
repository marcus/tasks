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

var DelegationKeyOrder = []string{"kind", "mode", "status", "assignee", "at", "work_ref"}

const (
	delegationAssigneeLimit = 200
	delegationWorkRefLimit  = 500
)

// DelegationErrors returns Ruby-compatible schema diagnostics for delegation.
func DelegationErrors(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return []string{"delegation must be an object"}
	}
	if len(object) == 0 {
		return []string{"delegation must not be empty"}
	}
	messages := delegationKindErrors(object)
	if !DelegationTimestamp(valueAt(object)) {
		messages = append(messages, fmt.Sprintf("delegation.at %s is not a UTC timestamp (YYYY-MM-DDTHH:MM:SSZ)", rubyInspect(valueAt(object))))
	}
	if reference, present := object["work_ref"]; present {
		messages = append(messages, delegationWorkRefErrors(reference)...)
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

func DelegationTimestamp(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) != 20 {
		return false
	}
	parsed, err := time.Parse("2006-01-02T15:04:05Z", text)
	return err == nil && DelegationStamp(parsed) == text
}
func DelegationStamp(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05Z") }

func delegationKindErrors(object map[string]any) []string {
	switch object["kind"] {
	case "human":
		messages := []string{}
		if _, present := object["mode"]; present {
			messages = append(messages, "delegation.mode is not allowed for a human delegation")
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
		if mode, ok := object["mode"].(string); !ok || (mode != "refine" && mode != "research" && mode != "implement") {
			messages = append(messages, fmt.Sprintf("delegation.mode %s must be one of refine/research/implement", rubyInspect(valueAt(object, "mode"))))
		}
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
