package query

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
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
	return ":" + inspectString(name)
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
	return len([]rune(name)) == 1
}

// identifier is Ruby's local-or-constant name shape: a leading letter or
// underscore, then letters, digits, and underscores. Every non-ASCII rune
// counts as a letter, which is why `:é?` and `:αβ` print bare.
func identifier(name string) bool {
	for index, character := range name {
		switch {
		case character == '_' || unicode.IsLetter(character) || character > unicode.MaxASCII:
		case index > 0 && unicode.IsDigit(character):
		default:
			return false
		}
	}
	return name != ""
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
