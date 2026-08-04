// Package jsonout writes JSON documents the way Ruby's JSON.generate writes
// them: members in the order the caller states, no whitespace, and Ruby's
// escape set for strings.
//
// It exists because every structured CLI surface is compared byte for byte
// against the Ruby oracle, and encoding/json cannot produce those bytes: Go
// sorts map keys, escapes `<`, `>`, `&`, U+2028 and U+2029, and spells floats
// with its own shortest-round-trip algorithm. The record package already owns
// the Ruby-compatible primitives (string escaping, number spelling, whole-value
// emission); this package is the ordered-object writer built on top of them.
package jsonout

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/marcus/tasks/internal/record"
)

// Writer accumulates one JSON document. Members appear in call order.
type Writer struct {
	buf      bytes.Buffer
	counts   []int
	afterKey bool
	err      error
}

// New returns an empty writer.
func New() *Writer { return &Writer{} }

// Bytes is the document written so far.
func (w *Writer) Bytes() []byte { return w.buf.Bytes() }

// String is the document written so far.
func (w *Writer) String() string { return w.buf.String() }

// Err reports the first error a value refused to encode with.
func (w *Writer) Err() error { return w.err }

// separate writes the comma that precedes every member after the first. A
// value that directly follows a key is that key's value, not a new member, so
// it skips the comma.
func (w *Writer) separate() {
	if w.afterKey {
		w.afterKey = false
		return
	}
	if len(w.counts) == 0 {
		return
	}
	if w.counts[len(w.counts)-1] > 0 {
		w.buf.WriteByte(',')
	}
	w.counts[len(w.counts)-1]++
}

// BeginObject opens `{`.
func (w *Writer) BeginObject() {
	w.separate()
	w.buf.WriteByte('{')
	w.counts = append(w.counts, 0)
}

// EndObject closes `}`.
func (w *Writer) EndObject() {
	w.buf.WriteByte('}')
	w.counts = w.counts[:len(w.counts)-1]
}

// BeginArray opens `[`.
func (w *Writer) BeginArray() {
	w.separate()
	w.buf.WriteByte('[')
	w.counts = append(w.counts, 0)
}

// EndArray closes `]`.
func (w *Writer) EndArray() {
	w.buf.WriteByte(']')
	w.counts = w.counts[:len(w.counts)-1]
}

// Key writes an object member name; the next value written becomes its value.
func (w *Writer) Key(name string) {
	w.separate()
	record.EncodeString(&w.buf, name)
	w.buf.WriteByte(':')
	w.afterKey = true
}

// Str writes a JSON string.
func (w *Writer) Str(value string) {
	w.separate()
	record.EncodeString(&w.buf, value)
}

// Null writes `null`.
func (w *Writer) Null() {
	w.separate()
	w.buf.WriteString("null")
}

// Bool writes `true` or `false`.
func (w *Writer) Bool(value bool) {
	w.separate()
	w.buf.WriteString(strconv.FormatBool(value))
}

// Int writes an integer.
func (w *Writer) Int(value int) {
	w.separate()
	w.buf.WriteString(strconv.Itoa(value))
}

// Raw writes an already-parsed JSON value with Ruby's spelling. A nil or empty
// value is `null`, which is how an absent record field reads.
func (w *Writer) Raw(raw json.RawMessage) {
	if len(raw) == 0 {
		w.Null()
		return
	}
	w.separate()
	if err := record.EncodeJSON(&w.buf, raw); err != nil && w.err == nil {
		w.err = err
	}
}

// StrOrNull writes a string, or `null` when it is empty.
func (w *Writer) StrOrNull(value string) {
	if value == "" {
		w.Null()
		return
	}
	w.Str(value)
}

// Strings writes an array of strings.
func (w *Writer) Strings(values []string) {
	w.BeginArray()
	for _, value := range values {
		w.Str(value)
	}
	w.EndArray()
}

// KeyStr, KeyStrOrNull, KeyBool and KeyInt are the one-call spellings of the
// overwhelmingly common key/value pair.
func (w *Writer) KeyStr(name, value string)       { w.Key(name); w.Str(value) }
func (w *Writer) KeyStrOrNull(name, value string) { w.Key(name); w.StrOrNull(value) }
func (w *Writer) KeyBool(name string, value bool) { w.Key(name); w.Bool(value) }
func (w *Writer) KeyInt(name string, value int)   { w.Key(name); w.Int(value) }
func (w *Writer) KeyNull(name string)             { w.Key(name); w.Null() }
