package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// KeyOrder is the canonical field order for a serialized record. A key present
// on a record but absent here is emitted after these, in the record's own
// source order, so a field a newer writer added survives a write by this one.
var KeyOrder = []string{
	"type", "id", "parent", "state", "priority", "title", "tags", "scheduled", "scheduled_time",
	"deadline", "deadline_time", "recur", "lead", "lead_skip", "delegation",
	"closed", "archived", "body", "updated",
}

// LineKey is the physical line number the parser stamps onto a record. It is
// bookkeeping, never part of the schema, so emission always drops it.
const LineKey = "line"

// GeneratorError reports a value JSON cannot represent. Ruby's generator
// raises JSON::GeneratorError for a non-finite float; nothing else in the
// record format can reach this layer unrepresentable, because every value
// arrives from a successful parse.
type GeneratorError struct {
	Key   string
	Value string
}

func (e *GeneratorError) Error() string {
	return fmt.Sprintf("%s not allowed in JSON at key %q", e.Value, e.Key)
}

// Dump serializes records to full file text: one record per line with a
// trailing newline at EOF. An empty list yields the empty string.
func Dump(records []Record) (string, error) {
	if len(records) == 0 {
		return "", nil
	}
	var out strings.Builder
	for _, record := range records {
		line, err := DumpRecord(record)
		if err != nil {
			return "", err
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// DumpRecord serializes one record to a single JSON line with no trailing
// newline. Known keys emit in KeyOrder, then unknown keys in source order.
// Null, the empty string, the empty array, and the empty object are omitted:
// they mean an absent field, not a present-but-empty one.
func DumpRecord(record Record) (string, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	written := 0

	emit := func(field Field, value json.RawMessage) error {
		if written > 0 {
			out.WriteByte(',')
		}
		written++
		encodeString(&out, field.Key)
		out.WriteByte(':')
		return encodeValue(&out, field.Key, value)
	}

	known := make(map[string]int, len(record.Fields))
	for index, field := range record.Fields {
		if _, seen := known[field.Key]; !seen {
			known[field.Key] = index
		}
	}
	ordered := make(map[string]bool, len(KeyOrder))
	for _, key := range KeyOrder {
		ordered[key] = true
		index, present := known[key]
		if !present {
			continue
		}
		value, err := nestedObject(key, record.Fields[index].Value)
		if err != nil {
			return "", err
		}
		if omit(value) {
			continue
		}
		if err := emit(record.Fields[index], value); err != nil {
			return "", err
		}
	}
	for index, field := range record.Fields {
		if known[field.Key] != index || field.Key == LineKey || ordered[field.Key] || omit(field.Value) {
			continue
		}
		if err := emit(field, field.Value); err != nil {
			return "", err
		}
	}

	out.WriteByte('}')
	return out.String(), nil
}

var nestedKeyOrder = map[string][]string{
	"scheduled_time": {"local", "timezone", "fold"},
	"deadline_time":  {"local", "timezone", "fold"},
	DelegationField:  DelegationKeyOrder,
}

// nestedObject applies Ruby's one-level canonical nested-object rule. Temporal
// objects discard unknown keys; delegation retains them after its declared
// fields in source order. Non-objects pass through for Check to diagnose.
func nestedObject(key string, raw json.RawMessage) (json.RawMessage, error) {
	order, recognized := nestedKeyOrder[key]
	trimmed := bytes.TrimSpace(raw)
	if !recognized || len(trimmed) == 0 || trimmed[0] != '{' {
		return raw, nil
	}
	fields, err := parseFields(trimmed)
	if err != nil {
		return nil, err
	}
	first := make(map[string]int, len(fields))
	for index, field := range fields {
		if _, seen := first[field.Key]; !seen {
			first[field.Key] = index
		}
	}
	known := make(map[string]bool, len(order))
	for _, child := range order {
		known[child] = true
	}
	ordered := make([]Field, 0, len(fields))
	for _, child := range order {
		if index, present := first[child]; present && !omit(fields[index].Value) {
			ordered = append(ordered, fields[index])
		}
	}
	if key == DelegationField {
		for index, field := range fields {
			if first[field.Key] != index || known[field.Key] || omit(field.Value) {
				continue
			}
			ordered = append(ordered, field)
		}
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for index, field := range ordered {
		if index > 0 {
			out.WriteByte(',')
		}
		encodeString(&out, field.Key)
		out.WriteByte(':')
		if err := encodeValue(&out, field.Key, field.Value); err != nil {
			return nil, err
		}
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// omit reports whether a value represents an absent field. Ruby omits nil and
// anything that answers empty? — the empty string, array, and object — while
// false, zero, and every other scalar are present values.
func omit(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0, bytes.Equal(trimmed, []byte("null")), bytes.Equal(trimmed, []byte(`""`)):
		return true
	case trimmed[0] == '[' || trimmed[0] == '{':
		return len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0
	}
	return false
}

// encodeValue re-serializes a parsed value the way Ruby's generator does:
// compact, non-ASCII left unescaped, and members in the order they arrived.
// Source spelling is not preserved — a value round-trips through parse and
// generate in Ruby too, so `1.50` canonicalizes to `1.5`.
func encodeValue(out *bytes.Buffer, key string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &GeneratorError{Key: key, Value: "empty value"}
	}
	switch trimmed[0] {
	case '{':
		fields, err := parseFields(trimmed)
		if err != nil {
			return err
		}
		out.WriteByte('{')
		for index, field := range fields {
			if index > 0 {
				out.WriteByte(',')
			}
			encodeString(out, field.Key)
			out.WriteByte(':')
			if err := encodeValue(out, field.Key, field.Value); err != nil {
				return err
			}
		}
		out.WriteByte('}')
		return nil
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal(trimmed, &elements); err != nil {
			return err
		}
		out.WriteByte('[')
		for index, element := range elements {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := encodeValue(out, key, element); err != nil {
				return err
			}
		}
		out.WriteByte(']')
		return nil
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
		encodeString(out, value)
		return nil
	case 't', 'f', 'n':
		out.Write(trimmed)
		return nil
	default:
		return encodeNumber(out, key, string(trimmed))
	}
}

// encodeNumber emits an Integer as its digits and a Float as Ruby's Float#to_s
// spelling. The distinction is the source literal's shape, exactly as Ruby's
// parser makes it: a fraction or an exponent produces a Float.
func encodeNumber(out *bytes.Buffer, key, literal string) error {
	if !strings.ContainsAny(literal, ".eE") {
		return encodeInteger(out, key, literal)
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return &GeneratorError{Key: key, Value: literal}
	}
	switch {
	case math.IsNaN(value):
		return &GeneratorError{Key: key, Value: "NaN"}
	case math.IsInf(value, 0):
		// Ruby's parser overflows to Float::INFINITY, which its generator then
		// refuses. Underflow to zero is not an error in either language.
		return &GeneratorError{Key: key, Value: "Infinity"}
	}
	out.WriteString(rubyFloat(value))
	return nil
}

// encodeInteger emits an integer literal's digits. Ruby has no integer
// ceiling, so the digits carry through unparsed; only a signed zero needs
// canonicalizing, since JSON forbids the other redundant spellings.
func encodeInteger(out *bytes.Buffer, key, literal string) error {
	digits := strings.TrimPrefix(literal, "-")
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return &GeneratorError{Key: key, Value: literal}
	}
	if strings.Trim(digits, "0") == "" {
		out.WriteString("0")
		return nil
	}
	out.WriteString(literal)
	return nil
}

// rubyFloat renders a float the way Ruby's JSON generator does, by running the
// same generator: fpconv_dtoa, Ruby's vendored Grisu2, ported in fpconv.go.
// strconv cannot stand in for it — strconv always emits the shortest
// round-tripping digits and Grisu2 sometimes does not, so 1e23 is
// 9.999999999999999e+22 to Ruby and 1e+23 to Go.
func rubyFloat(value float64) string {
	return fpconvDtoa(value)
}

// encodeString writes a JSON string with Ruby's escape set: the two structural
// escapes, the five short control escapes, \u00XX for the remaining control
// characters, and every other character verbatim. Go's encoder additionally
// escapes <, >, &, U+2028, and U+2029, none of which Ruby escapes by default.
func encodeString(out *bytes.Buffer, value string) {
	out.WriteByte('"')
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case char == '"':
			out.WriteString(`\"`)
		case char == '\\':
			out.WriteString(`\\`)
		case char == '\b':
			out.WriteString(`\b`)
		case char == '\f':
			out.WriteString(`\f`)
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case char < 0x20:
			fmt.Fprintf(out, `\u%04x`, char)
		default:
			out.WriteByte(char)
		}
	}
	out.WriteByte('"')
}

// EncodeJSON writes one already-parsed JSON value the way Ruby's
// JSON.generate writes it: no whitespace, object members in source order, and
// Ruby's escape set and number spellings. It is the seam the store's revision
// digests need — those digest `JSON.generate(...)` output, so a Go emitter
// that escaped `<` or reordered a nested object would compute a different
// token from identical bytes.
func EncodeJSON(out *bytes.Buffer, raw json.RawMessage) error {
	return encodeValue(out, "", raw)
}

// EncodeString writes a Go string as a JSON string with Ruby's escape set.
func EncodeString(out *bytes.Buffer, value string) { encodeString(out, value) }

// Fields parses a JSON object's members, retaining source order and each
// member's raw representation.
func Fields(raw json.RawMessage) ([]Field, error) { return parseFields(raw) }
