package merge

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/record"
)

// entry is one parsed JSONL record held the way Ruby holds it: an ordered
// key/value map. Insertion order is load-bearing — a key this binary does not
// know is emitted after the canonical ones in the order it arrived, so a field
// a newer writer added survives a merge by this one.
//
// The parser's physical line number is kept OUT of the field list rather than
// stamped into it as Ruby's Format.parse does. Ruby's merge strips it again at
// every comparison (`clean`), so keeping it beside the fields makes `clean` the
// identity it effectively is and removes the chance of a `line` key leaking
// into a merged record.
type entry struct {
	line   int
	keys   []string
	values map[string]json.RawMessage
}

func newEntry() *entry {
	return &entry{values: map[string]json.RawMessage{}}
}

// fromRecord is Format.parse's record minus the line stamp. A record that
// literally carries a "line" member loses it here, exactly as Ruby loses it:
// the parser overwrites that member with the physical number and `clean`
// removes it before anything compares or emits.
func fromRecord(parsed record.Record) *entry {
	built := newEntry()
	built.line = parsed.Line
	for _, field := range parsed.Fields {
		if field.Key == record.LineKey {
			continue
		}
		built.set(field.Key, field.Value)
	}
	return built
}

func (e *entry) get(key string) json.RawMessage {
	if e == nil {
		return nil
	}
	return e.values[key]
}

func (e *entry) has(key string) bool {
	if e == nil {
		return false
	}
	_, ok := e.values[key]
	return ok
}

// set replaces a value in place when the key is already present, which is Ruby
// Hash assignment: the position is the first one the key took.
func (e *entry) set(key string, value json.RawMessage) {
	if _, exists := e.values[key]; !exists {
		e.keys = append(e.keys, key)
	}
	e.values[key] = value
}

func (e *entry) delete(key string) {
	if _, exists := e.values[key]; !exists {
		return
	}
	delete(e.values, key)
	for index, name := range e.keys {
		if name == key {
			e.keys = append(e.keys[:index:index], e.keys[index+1:]...)
			return
		}
	}
}

// assign is JsonlMerge.assign: a nil or empty value means an absent field, so
// it deletes rather than writing an empty one.
func (e *entry) assign(key string, value json.RawMessage) {
	if emptyValue(value) {
		e.delete(key)
		return
	}
	e.set(key, value)
}

func (e *entry) clone() *entry {
	copied := newEntry()
	copied.line = e.line
	for _, key := range e.keys {
		copied.set(key, e.values[key])
	}
	return copied
}

func (e *entry) toRecord() record.Record {
	fields := make([]record.Field, 0, len(e.keys))
	for _, key := range e.keys {
		fields = append(fields, record.Field{Key: key, Value: e.values[key]})
	}
	return record.Record{Fields: fields, Line: e.line}
}

// str is the decoded value when it is a JSON string, and "" otherwise. It is
// only ever used where Ruby compares a field to a string literal, so a
// non-string value must compare unequal rather than approximately equal.
func (e *entry) str(key string) string {
	value, ok := decodeString(e.get(key))
	if !ok {
		return ""
	}
	return value
}

func (e *entry) id() string { return e.str("id") }

func decodeString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(trimmed, &value) != nil {
		return "", false
	}
	return value, true
}

// emptyValue is Format.omit?: nil, the empty string, the empty array and the
// empty object all mean an absent field.
func emptyValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0, bytes.Equal(trimmed, []byte("null")), bytes.Equal(trimmed, []byte(`""`)):
		return true
	case trimmed[0] == '[' || trimmed[0] == '{':
		return len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0
	}
	return false
}

// valueOf is JsonlMerge.value_of: an absent record or an absent field is nil.
func valueOf(e *entry, field string) json.RawMessage {
	if e == nil {
		return nil
	}
	return e.values[field]
}

// nilValue distinguishes Ruby's nil from every real value. An absent field and
// a field explicitly set to JSON null are the same nil to Ruby, so they are the
// same here.
func nilValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// sameValue is Ruby's `==` over two parsed JSON values: order-insensitive for
// objects, and 1 == 1.0 for numbers, because that is what comparing two Ruby
// Hashes does. Comparing the raw bytes instead would call `{"a":1,"b":2}` and
// `{"b":2,"a":1}` a conflict and hand one side's spelling to the loser.
func sameValue(left, right json.RawMessage) bool {
	return canonical(left) == canonical(right)
}

func sameEntry(left, right *entry) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if len(left.values) != len(right.values) {
		return false
	}
	for key, value := range left.values {
		other, ok := right.values[key]
		if !ok || !sameValue(value, other) {
			return false
		}
	}
	return true
}

// canonical renders a parsed value as a comparison key. Types are tagged so a
// string "1" never compares equal to the number 1, and object members are
// sorted so member order cannot masquerade as a difference.
func canonical(raw json.RawMessage) string {
	if nilValue(raw) {
		return "n"
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		// Unparseable bytes cannot reach here from a validated side, but if they
		// did, comparing them literally is the only honest answer.
		return "!" + string(raw)
	}
	var out strings.Builder
	writeCanonical(&out, value)
	return out.String()
}

func writeCanonical(out *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		out.WriteString("n")
	case bool:
		out.WriteString("b")
		out.WriteString(strconv.FormatBool(typed))
	case float64:
		out.WriteString("#")
		out.WriteString(strconv.FormatFloat(typed, 'g', -1, 64))
	case string:
		out.WriteString("s")
		out.WriteString(strconv.Quote(typed))
	case []any:
		out.WriteString("[")
		for index, item := range typed {
			if index > 0 {
				out.WriteString(",")
			}
			writeCanonical(out, item)
		}
		out.WriteString("]")
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteString("{")
		for index, key := range keys {
			if index > 0 {
				out.WriteString(",")
			}
			out.WriteString(strconv.Quote(key))
			out.WriteString(":")
			writeCanonical(out, typed[key])
		}
		out.WriteString("}")
	}
}

// orderedUnion is JsonlMerge.ordered_union: every value from every list, first
// occurrence wins, order preserved. It is what keeps the merged file ours-first.
func orderedUnion(lists ...[]string) []string {
	seen := map[string]bool{}
	union := make([]string, 0)
	for _, list := range lists {
		for _, value := range list {
			if seen[value] {
				continue
			}
			seen[value] = true
			union = append(union, value)
		}
	}
	return union
}
