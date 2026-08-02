package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// Object is a Ruby Hash as it arrives from JSON. Ruby's Hash preserves
// insertion order and JSON.parse inserts in document order, so `to_s` renders
// keys in the order the caller wrote them. A Go map cannot carry that order,
// so the boundary decoder produces this instead and no map ever reaches the
// rendering below.
type Object []Entry

// Entry is one Hash pair. Keys are always Strings: JSON has no other key type.
type Entry struct {
	Key   string
	Value any
}

// index reports where key already sits, or -1. Ruby's Hash#[]= keeps a
// repeated key in its first position while taking the later value.
func (object Object) index(key string) int {
	for position, entry := range object {
		if entry.Key == key {
			return position
		}
	}
	return -1
}

// DecodeValue decodes one JSON document into the shapes this file renders:
// nil, bool, string, json.Number, []any, and Object. It is the only decoder a
// dynamic boundary may use — `encoding/json`'s generic decode loses both Hash
// order and the distinction between an integer and a float literal.
func DecodeValue(document []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err == nil {
		return nil, fmt.Errorf("unexpected trailing JSON after the document")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		return decodeObject(decoder)
	case '[':
		return decodeArray(decoder)
	default:
		return nil, fmt.Errorf("unexpected %v in JSON document", delimiter)
	}
}

func decodeObject(decoder *json.Decoder) (Object, error) {
	object := Object{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, isKey := token.(string)
		if !isKey {
			return nil, fmt.Errorf("JSON object key is not a string: %v", token)
		}
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		if position := object.index(key); position >= 0 {
			object[position].Value = value
			continue
		}
		object = append(object, Entry{Key: key, Value: value})
	}
	_, err := decoder.Token() // consumes `}`
	return object, err
}

func decodeArray(decoder *json.Decoder) ([]any, error) {
	elements := []any{}
	for decoder.More() {
		element, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		elements = append(elements, element)
	}
	_, err := decoder.Token() // consumes `]`
	return elements, err
}

// CoerceStrings mirrors Ruby's `Array(values).map { |value| value.to_s }`, the
// coercion TaskFilter#initialize applies to contexts, tags, and text before any
// other rule sees them. Go's typed constructor cannot express it, so dynamic
// boundaries (the JSON kwargs probe, and later any dynamic adapter) apply this
// rule before building FilterOptions rather than rejecting the input.
//
// The value is a JSON document decoded into Go's generic shapes, with numbers
// preserved as json.Number so integers stay integers as they do in Ruby.
func CoerceStrings(value any) []string {
	elements := rubyArray(value)
	coerced := make([]string, 0, len(elements))
	for _, element := range elements {
		coerced = append(coerced, rubyToS(element))
	}
	return coerced
}

// CoerceString mirrors Ruby's `value.to_s`, the coercion TaskFilter#initialize
// applies to scope, priority, and state before any domain rule sees them. A
// dynamic boundary applies it so a non-String value is rejected by the ported
// domain message rather than by a decoder.
func CoerceString(value any) string {
	return rubyToS(value)
}

// CoerceBool mirrors Ruby's `!!value`, the coercion TaskFilter#initialize
// applies to every boolean keyword. Only nil and false are false in Ruby: `0`
// and `""` are truthy, so JSON falsiness must not be mapped onto it.
func CoerceBool(value any) bool {
	if value == nil {
		return false
	}
	boolean, isBool := value.(bool)
	return !isBool || boolean
}

// InspectSymbol mirrors Ruby's Symbol#inspect, which is how the interpreter
// renders an unrecognised keyword name in the ArgumentError it raises before
// TaskFilter#initialize runs. A name the parser would accept as a symbol
// literal prints bare; anything else is quoted with String#inspect.
func InspectSymbol(name string) string {
	if bareSymbolName(name) {
		return ":" + name
	}
	return ":" + quoteSymbol(name)
}

// bareSymbolName reports whether Ruby prints this symbol without quotes: a
// local or constant name with an optional single trailing `?`, `!`, or `=`; an
// instance, class, or global variable name; or one of the operator methods.
// `~@` and `!@` are deliberately absent — Ruby quotes those two.
func bareSymbolName(name string) bool {
	switch name {
	case "+", "-", "*", "/", "%", "**", "==", "===", "!=", ">", ">=", "<", "<=",
		"<=>", "<<", ">>", "~", "!", "=~", "!~", "&", "|", "^", "[]", "[]=",
		"+@", "-@", "`":
		return true
	}
	switch {
	case strings.HasPrefix(name, "@@"):
		return identifier(name[2:])
	case strings.HasPrefix(name, "@"):
		return identifier(name[1:])
	case strings.HasPrefix(name, "$"):
		return globalName(name[1:])
	}
	if trimmed := strings.TrimRight(name, "?!="); len(trimmed) == len(name)-1 {
		// One trailing `?`, `!`, or `=` is part of a method name; two are not,
		// so `a?=` falls through to the quoted form as Ruby renders it.
		name = trimmed
	}
	return identifier(name)
}

// globalName covers `$foo`, the digit globals `$1`, and the single-character
// specials such as `$!` and `$;`, all of which Ruby prints bare.
func globalName(name string) bool {
	if name == "" {
		return false
	}
	if identifier(name) {
		return true
	}
	if strings.TrimLeft(name, "0123456789") == "" {
		return true
	}
	return len([]rune(name)) == 1 && strings.ContainsAny(name, specialGlobals)
}

// specialGlobals is Ruby's fixed one-character global vocabulary, the whole of
// it. It is a set, not "any single character": `$;` is a global and `$%` is
// not, so `:$%` quotes. `$` alone is not in the set either — globalName's
// empty-name guard rejects that before this test.
const specialGlobals = "!\"$&'*+,./:;<=>?@\\`~"

// identifier is Ruby's local-or-constant name shape: a leading letter or
// underscore, then letters, digits, and underscores. A printable non-ASCII
// rune counts as a letter in any position, which is why `:é?` and `:αβ` print
// bare — but a non-printable one does not, so `:""` quotes.
func identifier(name string) bool {
	for index, character := range name {
		switch {
		case character > unicode.MaxASCII:
			if !symbolPrintable(character) {
				return false
			}
		case character == '_' || unicode.IsLetter(character):
		case index > 0 && unicode.IsDigit(character):
		default:
			return false
		}
	}
	return name != ""
}

// symbolPrintable is the non-ASCII half of Ruby's bare-symbol predicate. It is
// String#inspect's printable set plus U+0085, the one codepoint a String
// escapes (`""`) and a Symbol still prints bare.
func symbolPrintable(character rune) bool {
	return unicode.Is(rubyPrintable, character) || character == nextLine
}

// nextLine is U+0085 NEL, the sole exception in symbolPrintable.
const nextLine = 0x85

// rubyArray is Kernel#Array for the JSON value shapes: nil becomes empty, an
// Array is itself, a Hash becomes its [key, value] pairs, and any other value
// is wrapped in a one-element Array.
func rubyArray(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case Object:
		pairs := make([]any, 0, len(typed))
		for _, entry := range typed {
			pairs = append(pairs, []any{entry.Key, entry.Value})
		}
		return pairs
	default:
		return []any{value}
	}
}

// rubyToS is Object#to_s for the JSON value shapes. Only String differs from
// rubyInspect: `"a".to_s` is `a` while `"a".inspect` is `"a"`, which is why a
// Hash element stringifies to `{"key" => "value"}`.
func rubyToS(value any) string {
	switch typed := value.(type) {
	case nil:
		// NilClass#to_s is the empty string, unlike NilClass#inspect.
		return ""
	case string:
		return typed
	default:
		return rubyInspect(value)
	}
}

// rubyInspect is Object#inspect for the JSON value shapes, in the Ruby 3.4
// Hash format (`{"key" => "value"}`) the recorded oracle shows.
func rubyInspect(value any) string {
	switch typed := value.(type) {
	case nil:
		return "nil"
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case string:
		return inspectString(typed)
	case []any:
		elements := make([]string, 0, len(typed))
		for _, element := range typed {
			elements = append(elements, rubyInspect(element))
		}
		return "[" + strings.Join(elements, ", ") + "]"
	case Object:
		if len(typed) == 0 {
			return "{}"
		}
		pairs := make([]string, 0, len(typed))
		for _, entry := range typed {
			pairs = append(pairs, inspectString(entry.Key)+" => "+rubyInspect(entry.Value))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	case json.Number:
		return rubyNumber(typed)
	default:
		return fmt.Sprint(value)
	}
}

// rubyNumber renders a JSON number the way Ruby's JSON parser and then to_s
// do. Ruby decides Integer or Float from the literal's shape — a `.`, `e`, or
// `E` makes it a Float — and each class re-renders the value rather than
// echoing the literal's digits.
func rubyNumber(number json.Number) string {
	literal := number.String()
	if !strings.ContainsAny(literal, ".eE") {
		// Integer#to_s of an arbitrary-precision value. Ruby's Integer has no
		// width limit, so a literal wider than int64 must not be truncated;
		// `-0` is the one literal JSON allows that this renormalises.
		if integer, ok := new(big.Int).SetString(literal, 10); ok {
			return integer.String()
		}
		return literal
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		// Out of range is the only failure a JSON literal can reach, and
		// ParseFloat still returns the saturated infinity Ruby's parser makes.
		if !errors.Is(err, strconv.ErrRange) {
			return literal
		}
	}
	return rubyFloatToS(value)
}

// rubyFloatToS is Float#to_s. Ruby renders the shortest digit string that
// round-trips, then chooses a layout from the decimal point's position: fixed
// while the point sits within the first DBL_DIG (15) digits, fixed at 16 as
// long as a fractional digit remains, `0.000ddd` down to a point four places
// left of the digits, and exponent form outside that. A fixed rendering always
// carries a fractional digit, so `100.0` never prints as `100`.
func rubyFloatToS(value float64) string {
	sign := ""
	if math.Signbit(value) {
		sign = "-"
	}
	if math.IsInf(value, 0) {
		return sign + "Infinity"
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(math.Abs(value), 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	power, err := strconv.Atoi(exponent)
	if err != nil {
		return sign + mantissa
	}
	// point is where the decimal point falls among the digits: the value is
	// 0.<digits> * 10**point, which is dtoa's `decpt`.
	point := power + 1
	switch {
	case point > 0 && (point <= 15 || (point == 16 && len(digits) > point)):
		if len(digits) <= point {
			return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
		}
		return sign + digits[:point] + "." + digits[point:]
	case point <= 0 && point > -4:
		return sign + "0." + strings.Repeat("0", -point) + digits
	default:
		fraction := digits[1:]
		if fraction == "" {
			fraction = "0"
		}
		return fmt.Sprintf("%s%s.%se%s%02d", sign, digits[:1], fraction, exponentSign(point-1), abs(point-1))
	}
}

func exponentSign(power int) string {
	if power < 0 {
		return "-"
	}
	return "+"
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

var stringEscapes = map[rune]string{
	'"': `\"`, '\\': `\\`, '\n': `\n`, '\t': `\t`, '\r': `\r`,
	'\f': `\f`, '\v': `\v`, '\b': `\b`, '\a': `\a`, 0x1b: `\e`,
}

// inspectString is String#inspect for the characters JSON can deliver, which
// is always a UTF-8 String. `\xNN` is Ruby's binary-string form and never
// appears here: a character with no named escape that Ruby does not consider
// printable is rendered `\uNNNN`, or `\u{NNNNN}` above the BMP, in uppercase
// hex. text_query downcases the rendered text afterwards, so the same
// character reaches `text` uppercase and `text_query` lowercase.
func inspectString(text string) string {
	return quoteRuby(text, false)
}

// quoteSymbol is the quoted form of Symbol#inspect, which shares String's
// named escapes and `#` rule but spells an unnamed C0 or DEL character `\xNN`
// where a String spells it `\uNNNN`. The two vocabularies really do differ:
// U+0001 is `""` as a String and `:"\x01"` as a Symbol.
func quoteSymbol(name string) string {
	return quoteRuby(name, true)
}

// quoteRuby renders the shared body of String#inspect and Symbol#inspect.
// hexC0 selects the caller's spelling for an unnamed character below U+0020 or
// U+007F; everything else is identical between the two.
func quoteRuby(text string, hexC0 bool) string {
	var builder strings.Builder
	builder.WriteByte('"')
	runes := []rune(text)
	for index, character := range runes {
		switch {
		case stringEscapes[character] != "":
			builder.WriteString(stringEscapes[character])
		case character == '#' && index+1 < len(runes) && strings.ContainsRune("{$@", runes[index+1]):
			builder.WriteString(`\#`)
		case unicode.Is(rubyPrintable, character):
			builder.WriteRune(character)
		case hexC0 && (character < 0x20 || character == 0x7F):
			fmt.Fprintf(&builder, `\x%02X`, character)
		case character > 0xFFFF:
			fmt.Fprintf(&builder, `\u{%X}`, character)
		default:
			fmt.Fprintf(&builder, `\u%04X`, character)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
