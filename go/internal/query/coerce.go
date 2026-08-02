package query

import (
	"fmt"
	"sort"
	"strings"
)

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

// rubyArray is Kernel#Array for the JSON value shapes: nil becomes empty, an
// Array is itself, a Hash becomes its [key, value] pairs, and any other value
// is wrapped in a one-element Array.
func rubyArray(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case map[string]any:
		pairs := make([]any, 0, len(typed))
		for _, key := range sortedKeys(typed) {
			pairs = append(pairs, []any{key, typed[key]})
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
	case map[string]any:
		if len(typed) == 0 {
			return "{}"
		}
		pairs := make([]string, 0, len(typed))
		for _, key := range sortedKeys(typed) {
			pairs = append(pairs, inspectString(key)+" => "+rubyInspect(typed[key]))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	case fmt.Stringer:
		// json.Number keeps the literal digits, which is what Integer#to_s and
		// Float#to_s produce for every literal Ruby's JSON parser accepts.
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

// sortedKeys keeps Hash rendering deterministic. JSON objects decode into an
// unordered Go map, so insertion order — which Ruby preserves — is already
// lost at the boundary; sorting makes the loss visible and reproducible
// instead of varying per run.
func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var stringEscapes = map[rune]string{
	'"': `\"`, '\\': `\\`, '\n': `\n`, '\t': `\t`, '\r': `\r`,
	'\f': `\f`, '\v': `\v`, '\b': `\b`, '\a': `\a`, 0x1b: `\e`,
}

// inspectString is String#inspect for the characters JSON can deliver.
func inspectString(text string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	runes := []rune(text)
	for index, character := range runes {
		switch {
		case stringEscapes[character] != "":
			builder.WriteString(stringEscapes[character])
		case character == '#' && index+1 < len(runes) && strings.ContainsRune("{$@", runes[index+1]):
			builder.WriteString(`\#`)
		case character < 0x20 || character == 0x7f:
			fmt.Fprintf(&builder, `\x%02X`, character)
		default:
			builder.WriteRune(character)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
