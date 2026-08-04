package record

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// The write path edits records the way Ruby edits a Hash, and Ruby's Hash
// preserves insertion order: assigning to an EXISTING key keeps its position,
// assigning to a new one appends. Canonical emission reorders known keys
// anyway, but unknown forward-compatible keys are emitted in source order, so
// getting this wrong changes the bytes of a store written by a newer binary.

// Clone is `duplicate_records`' per-record half: a detached copy whose fields
// can be replaced without disturbing the original. Values are json.RawMessage
// and are only ever replaced, never mutated in place, so copying the slice is a
// deep copy in practice.
func Clone(r Record) Record {
	fields := make([]Field, len(r.Fields))
	copy(fields, r.Fields)
	return Record{Fields: fields, Line: r.Line}
}

// CloneAll is `duplicate_records`.
func CloneAll(records []Record) []Record {
	out := make([]Record, len(records))
	for index, parsed := range records {
		out[index] = Clone(parsed)
	}
	return out
}

// Get is `rec[key]`: the raw value, or ok=false when the key is absent. The
// FIRST occurrence wins, matching the parser's own duplicate-key handling.
func (r Record) Get(key string) (json.RawMessage, bool) {
	for _, field := range r.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return nil, false
}

// Has reports key presence, including a present-but-null key.
func (r Record) Has(key string) bool {
	_, ok := r.Get(key)
	return ok
}

// String is `rec[key]` decoded as a String, or "" for absent and non-string
// values. Every caller here treats a non-string exactly as Ruby's `.to_s` on a
// nil would: as the empty string.
func (r Record) String(key string) string {
	raw, ok := r.Get(key)
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// Truthy is Ruby truthiness for a stored value: anything but absent, null and
// false.
func (r Record) Truthy(key string) bool {
	raw, ok := r.Get(key)
	return ok && string(raw) != "null" && string(raw) != "false"
}

// Set is `rec[key] = value`: an existing key keeps its position, a new one is
// appended.
func (r *Record) Set(key string, value json.RawMessage) {
	for index := range r.Fields {
		if r.Fields[index].Key == key {
			r.Fields[index].Value = value
			return
		}
	}
	r.Fields = append(r.Fields, Field{Key: key, Value: value})
}

// SetString is Set for a plain string value.
func (r *Record) SetString(key, value string) { r.Set(key, RawString(value)) }

// SetDefault is Ruby's `rec[key] ||= value`: it writes only when the key is
// absent or holds nil/false.
func (r *Record) SetDefault(key string, value json.RawMessage) {
	if r.Truthy(key) {
		return
	}
	r.Set(key, value)
}

// Delete is `rec.delete(key)`, removing every occurrence.
func (r *Record) Delete(key string) {
	fields := r.Fields[:0]
	for _, field := range r.Fields {
		if field.Key != key {
			fields = append(fields, field)
		}
	}
	r.Fields = fields
}

// SetOptional is Store#replace_optional: an absent, null or empty value deletes
// the key rather than writing a present-but-empty one, because the format reads
// those as the same thing and writing one would change the bytes for no
// semantic difference.
func (r *Record) SetOptional(key string, value json.RawMessage) {
	if Absent(value) {
		r.Delete(key)
		return
	}
	r.Set(key, value)
}

// Absent is Delegation.absent?: nil, the empty string, the empty array and the
// empty object all mean "this field is not here".
func Absent(value json.RawMessage) bool {
	switch string(value) {
	case "", "null", `""`, "[]", "{}":
		return true
	}
	return false
}

// RawString encodes a Go string as a canonical JSON string, with Ruby's escape
// set rather than Go's — encoding/json is never the writer here.
func RawString(value string) json.RawMessage {
	var out bytes.Buffer
	encodeString(&out, value)
	return json.RawMessage(out.Bytes())
}

// RawStrings encodes a list of strings as a canonical JSON array.
func RawStrings(values []string) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		encodeString(&out, value)
	}
	out.WriteByte(']')
	return json.RawMessage(out.Bytes())
}

// RawInt encodes an integer.
func RawInt(value int) json.RawMessage { return json.RawMessage(strconv.Itoa(value)) }

// DecodeStrings reads a stored string array, skipping members that are not
// strings — which is what Ruby's tag handling does with a foreign writer's
// list.
func DecodeStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []any
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
