// Package record reads the JSONL record format used by tasks.
package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Field is one parsed object member. The parser retains source member order so
// a later canonical emitter can append forward-compatible keys in the same
// order Ruby's Hash preserves them.
type Field struct {
	Key   string
	Value json.RawMessage
}

// Record is one parsed JSONL object. Fields retain JSON representations and
// source order so later validation and canonical emission can make their own
// type decisions. Line is physical and is never part of the persisted record
// schema.
type Record struct {
	Fields []Field
	Line   int
}

// ParseError is a non-fatal defect in an input file. Parsing continues after
// per-line errors so callers can still inspect the records that were sound.
type ParseError struct {
	Line    int
	Message string
}

// Result is the complete, non-failing outcome of parsing a JSONL file.
type Result struct {
	Records []Record
	Errors  []ParseError
}

// OK reports whether the input had no parse errors.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Parse reads JSONL leniently. A malformed or non-object line becomes an
// error, while valid records retain their physical one-based line number.
// Invalid UTF-8 is a whole-file error because Ruby rejects it before it can
// safely split the file into JSONL lines.
func Parse(input []byte) Result {
	if !utf8.Valid(input) {
		return Result{Errors: []ParseError{{Line: 0, Message: "file is not valid UTF-8"}}}
	}

	input = bytes.TrimPrefix(input, []byte{0xef, 0xbb, 0xbf})
	if len(input) == 0 {
		return Result{}
	}
	lines := bytes.Split(input, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 && bytes.HasSuffix(input, []byte{'\n'}) {
		lines = lines[:len(lines)-1]
	}

	result := Result{}
	for index, line := range lines {
		lineNo := index + 1
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(rubyStrip(line)) == 0 {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "blank line"})
			continue
		}
		// encoding/json replaces an unpaired UTF-16 surrogate with U+FFFD,
		// while Ruby rejects the source escape before materializing a string.
		if escape, column := unpairedSurrogate(line); escape != "" {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: fmt.Sprintf("invalid JSON: incomplete surrogate pair at '%s' at line 1 column %d", escape, column)})
			continue
		}

		value, err := decode(line)
		if err != nil {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "invalid JSON: " + rubyJSONError(line, err)})
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "expected a JSON object, got " + rubyClass(value)})
			continue
		}
		fields, err := parseFields(line)
		if err != nil {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "invalid JSON: " + rubyJSONError(line, err)})
			continue
		}
		result.Records = append(result.Records, Record{Fields: fields, Line: lineNo})
	}
	return result
}

// rubyStrip implements the byte-level whitespace set used by Ruby
// String#strip for the already UTF-8-validated JSONL line. In particular,
// NUL is blank to Ruby while non-breaking space is not; bytes.TrimSpace has
// the opposite behavior for those two boundary cases.
func rubyStrip(line []byte) []byte {
	start, end := 0, len(line)
	for start < end && rubyWhitespace(line[start]) {
		start++
	}
	for end > start && rubyWhitespace(line[end-1]) {
		end--
	}
	return line[start:end]
}

func rubyWhitespace(char byte) bool {
	return char == 0 || char == ' ' || (char >= '\t' && char <= '\r')
}

// parseFields decodes an already-validated object without collapsing it into a
// Go map. A duplicate key has Ruby Hash semantics: its first position remains
// stable while its final value wins.
func parseFields(line []byte) ([]Field, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}

	fields := make([]Field, 0)
	positions := make(map[string]int)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if position, exists := positions[key]; exists {
			fields[position].Value = value
			continue
		}
		positions[key] = len(fields)
		fields = append(fields, Field{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}

func decode(line []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("additional JSON value")
		}
		return nil, err
	}
	return value, nil
}

func rubyClass(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NilClass"
	case []any:
		return "Array"
	case string:
		return "String"
	case bool:
		if typed {
			return "TrueClass"
		}
		return "FalseClass"
	case json.Number:
		if strings.ContainsAny(string(typed), ".eE") {
			return "Float"
		}
		return "Integer"
	default:
		return "Object"
	}
}

// rubyJSONError translates malformed source into the wording produced by
// Ruby's JSON parser. Ruby reports source-aware clauses and columns, while
// encoding/json's wording is an implementation detail, so this adapter only
// uses the decoder to establish invalidity and derives its diagnostics from
// the original JSONL record.
func rubyJSONError(line []byte, err error) string {
	_ = err
	// Only the first BOM is stripped before line splitting, matching
	// String#sub(/\A<U+FEFF>/, "") in the Ruby oracle. A second (or line-two) BOM is
	// an observable JSON parser error, not whitespace.
	if bytes.HasPrefix(line, []byte{0xef, 0xbb, 0xbf}) {
		return fmt.Sprintf("unexpected character: '%s' at line 1 column 1", line)
	}
	if bytes.Equal(line, []byte{0xc2, 0xa0}) {
		return "unexpected character: '' at line 1 column 1"
	}
	if bytes.Equal(bytes.TrimSpace(line), []byte("[")) {
		return fmt.Sprintf("unexpected end of input at line 1 column %d", len(line)+1)
	}
	if trailing := trailingToken(line); trailing != nil {
		return fmt.Sprintf("unexpected token at end of stream '%s' at line 1 column %d", trailing.token, trailing.column)
	}
	if diagnostic := malformedTokenDiagnostic(line); diagnostic != "" {
		return diagnostic
	}
	if bytes.HasSuffix(line, []byte(",}")) {
		return fmt.Sprintf("expected object key, got: '}' at line 1 column %d", len(line))
	}
	if escape, column := invalidEscape(line); escape != "" {
		return fmt.Sprintf("invalid escape character in string: '%s' at line 1 column %d", escape, column)
	}
	if isUnexpectedEOF(line) {
		column := len(line) + 1
		if hasUnclosedString(line) {
			return fmt.Sprintf("unexpected end of input, expected closing \" at line 1 column %d", column)
		}
		if bytes.HasSuffix(line, []byte(":")) {
			return fmt.Sprintf("unexpected end of input at line 1 column %d", column)
		}
		if hasUnclosedArrayValue(line) {
			return fmt.Sprintf("expected ',' or ']' after array value at line 1 column %d", column)
		}
		separator := ""
		if bytes.HasSuffix(line, []byte(",")) {
			separator = ":"
		}
		return fmt.Sprintf("expected object key, got%s EOF at line 1 column %d", separator, column)
	}
	return "unexpected token at end of stream"
}

// malformedTokenDiagnostic covers the Ruby parser's non-EOF lexical cases.
// Every clause is selected from source tokens and physical offsets, never from
// encoding/json error strings, which change across Go releases.
func malformedTokenDiagnostic(line []byte) string {
	if diagnostic := missingColonDiagnostic(line); diagnostic != "" {
		return diagnostic
	}
	if diagnostic := missingSeparatorDiagnostic(line); diagnostic != "" {
		return diagnostic
	}
	if diagnostic := malformedNumberDiagnostic(line); diagnostic != "" {
		return diagnostic
	}
	if escape, column := unpairedSurrogate(line); escape != "" {
		return fmt.Sprintf("incomplete surrogate pair at '%s' at line 1 column %d", escape, column)
	}
	if start := bytes.Index(line, []byte(`\u`)); start >= 0 {
		escape := line[start:]
		if len(escape) >= 6 && !hex4(escape[2:6]) {
			return fmt.Sprintf("incomplete unicode character escape sequence at '%s' at line 1 column %d", escape, start+1)
		}
	}
	if index := bytes.Index(line, []byte(`" "`)); index >= 0 {
		return fmt.Sprintf("expected ':' after object key at line 1 column %d", index+3)
	}
	if index := bytes.Index(line, []byte(`:,}`)); index >= 0 {
		return fmt.Sprintf("unexpected character: ',}' at line 1 column %d", index+2)
	}
	if index := bytes.Index(line, []byte(`,]}`)); index >= 0 {
		return fmt.Sprintf("unexpected character: ']}' at line 1 column %d", index+2)
	}
	if diagnostic := adjacentLiteralDiagnostic(line); diagnostic != "" {
		return diagnostic
	}
	if index := bytes.Index(line, []byte(`tru}`)); index >= 0 {
		return fmt.Sprintf("unexpected token 'tru}' at line 1 column %d", index+1)
	}
	if index := bytes.Index(line, []byte(`01}`)); index >= 0 {
		return fmt.Sprintf("invalid number: '01}' at line 1 column %d", index+1)
	}
	if index := bytes.Index(line, []byte(`1e}`)); index >= 0 {
		return fmt.Sprintf("invalid number: '1e}' at line 1 column %d", index+1)
	}
	if index := bytes.Index(line, []byte(`}{`)); index >= 0 {
		return fmt.Sprintf("unexpected token at end of stream '%s' at line 1 column %d", line[index+1:], index+2)
	}
	return ""
}

// adjacentLiteralDiagnostic identifies two JSON literals written together. A
// decoder rejects this in either container, but Ruby names the containing
// separator and the first byte of the second literal.
func adjacentLiteralDiagnostic(line []byte) string {
	for _, first := range [][]byte{[]byte("true"), []byte("false"), []byte("null")} {
		for _, second := range [][]byte{[]byte("true"), []byte("false"), []byte("null")} {
			needle := append(append([]byte{}, first...), second...)
			index := bytes.Index(line, needle)
			if index < 0 {
				continue
			}
			secondIndex := index + len(first)
			if containerAt(line, secondIndex) == '[' {
				return fmt.Sprintf("expected ',' or ']' after array value at line 1 column %d", secondIndex+1)
			}
			if containerAt(line, secondIndex) == '{' {
				return fmt.Sprintf("expected ',' or '}' after object value, got: '%s' at line 1 column %d", tokenThroughClose(line[secondIndex:]), secondIndex+1)
			}
		}
	}
	return ""
}

func unpairedSurrogate(line []byte) (string, int) {
	for start := 0; start+6 <= len(line); start++ {
		if !bytes.Equal(line[start:start+2], []byte(`\u`)) || !hex4(line[start+2:start+6]) {
			continue
		}
		code, err := strconv.ParseUint(string(line[start+2:start+6]), 16, 16)
		if err != nil || code < 0xd800 || code > 0xdbff {
			continue
		}
		if start+12 > len(line) || !bytes.Equal(line[start+6:start+8], []byte(`\u`)) || !hex4(line[start+8:start+12]) {
			return string(line[start:]), start + 1
		}
		low, err := strconv.ParseUint(string(line[start+8:start+12]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return string(line[start:]), start + 1
		}
	}
	return "", 0
}

func hex4(value []byte) bool {
	if len(value) != 4 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}

func isUnexpectedEOF(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return false
	}
	last := trimmed[len(trimmed)-1]
	return last == '{' || last == '[' || last == ':' || last == ',' || last == '"' || hasUnclosedString(line) || hasUnclosedArrayValue(line)
}

type tokenAtColumn struct {
	token  string
	column int
}

// trailingToken recognizes a completed object followed by a non-whitespace
// token. JSON decoding still establishes that the input is invalid; this only
// reproduces Ruby's diagnostic for the bounded case captured in the oracle.
func trailingToken(line []byte) *tokenAtColumn {
	end := completeValueEnd(line)
	if end < 0 || end == len(line) {
		return nil
	}
	rest := line[end:]
	trimmed := bytes.TrimLeft(rest, " \t\r")
	if len(trimmed) == 0 {
		return nil
	}
	column := len(line) - len(trimmed) + 1
	return &tokenAtColumn{token: string(trimmed), column: column}
}

// completeValueEnd returns the byte immediately after a balanced top-level
// object or array. It is deliberately lexical: json.Decoder establishes
// validity, while this preserves the source position Ruby names when a valid
// value is followed by malformed JSON.
func completeValueEnd(line []byte) int {
	start := len(line) - len(bytes.TrimLeft(line, " \t\r"))
	if start == len(line) || (line[start] != '{' && line[start] != '[') {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(line); index++ {
		char := line[index]
		if inString {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return -1
}

// missingSeparatorDiagnostic identifies a second value in an object or array
// after the first value was complete. Its offsets come from JSON tokens rather
// than from encoding/json's error text or a particular literal spelling.
func missingSeparatorDiagnostic(line []byte) string {
	for index := 0; index < len(line); index++ {
		if line[index] != ' ' && line[index] != '\t' {
			continue
		}
		previous := previousNonSpace(line, index-1)
		next := nextNonSpace(line, index+1)
		if previous < 0 || next >= len(line) || !startsValue(line[next]) {
			continue
		}
		if containerAt(line, index) == '[' && endsValue(line[previous]) {
			return fmt.Sprintf("expected ',' or ']' after array value at line 1 column %d", next+1)
		}
		if containerAt(line, index) == '{' && endsValue(line[previous]) {
			return fmt.Sprintf("expected ',' or '}' after object value, got: '%s' at line 1 column %d", tokenThroughClose(line[next:]), next+1)
		}
	}
	inString := false
	escaped := false
	stringStart := 0
	for index, char := range line {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char != '"' {
				continue
			}
			inString = false
			next := nextNonSpace(line, index+1)
			previous := previousNonSpace(line, stringStart-1)
			container := containerAt(line, index)
			objectValue := container == '{' && previous >= 0 && line[previous] == ':'
			arrayValue := container == '[' && previous >= 0 && (line[previous] == '[' || line[previous] == ',')
			if next < len(line) && startsValue(line[next]) && (objectValue || arrayValue) {
				if container == '[' {
					return fmt.Sprintf("expected ',' or ']' after array value at line 1 column %d", next+1)
				}
				return fmt.Sprintf("expected ',' or '}' after object value, got: '%s' at line 1 column %d", tokenThroughClose(line[next:]), next+1)
			}
			continue
		}
		if char == '"' {
			inString = true
			stringStart = index
		}
	}
	return ""
}

func missingColonDiagnostic(line []byte) string {
	inString := false
	escaped := false
	stringStart := 0
	for index, char := range line {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char != '"' {
				continue
			}
			inString = false
			next := nextNonSpace(line, index+1)
			previous := previousNonSpace(line, stringStart-1)
			if containerAt(line, index) == '{' && previous >= 0 && (line[previous] == '{' || line[previous] == ',') && next < len(line) && startsValue(line[next]) {
				return fmt.Sprintf("expected ':' after object key at line 1 column %d", next+1)
			}
			continue
		}
		if char == '"' {
			inString = true
			stringStart = index
		}
	}
	return ""
}

func malformedNumberDiagnostic(line []byte) string {
	for _, malformed := range [][]byte{[]byte("-}"), []byte("1e+}")} {
		if index := bytes.Index(line, malformed); index >= 0 {
			return fmt.Sprintf("invalid number: '%s' at line 1 column %d", line[index:index+len(malformed)], index+1)
		}
	}
	if index := bytes.Index(line, []byte(".1}")); index >= 0 {
		return fmt.Sprintf("unexpected character: '%s' at line 1 column %d", line[index:], index+1)
	}
	for index := 0; index+1 < len(line); index++ {
		if line[index] != '.' || line[index+1] != '}' {
			continue
		}
		start := index
		for start > 0 && (line[start-1] >= '0' && line[start-1] <= '9') {
			start--
		}
		if start < index {
			return fmt.Sprintf("invalid number: '%s' at line 1 column %d", line[start:index+2], start+1)
		}
	}
	return ""
}

func previousNonSpace(line []byte, index int) int {
	for ; index >= 0; index-- {
		if line[index] != ' ' && line[index] != '\t' && line[index] != '\r' {
			return index
		}
	}
	return -1
}

func nextNonSpace(line []byte, index int) int {
	for ; index < len(line); index++ {
		if line[index] != ' ' && line[index] != '\t' && line[index] != '\r' {
			return index
		}
	}
	return len(line)
}

func startsValue(char byte) bool {
	return char == '"' || char == '{' || char == '[' || char == '-' || (char >= '0' && char <= '9') || char == 't' || char == 'f' || char == 'n'
}

func endsValue(char byte) bool {
	return char == '"' || char == '}' || char == ']' || (char >= '0' && char <= '9') || char == 'e' || char == 'l'
}

func containerAt(line []byte, before int) byte {
	stack := make([]byte, 0, 4)
	inString := false
	escaped := false
	for _, char := range line[:before] {
		if inString {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		if char == '{' || char == '[' {
			stack = append(stack, char)
		}
		if (char == '}' || char == ']') && len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) == 0 {
		return 0
	}
	return stack[len(stack)-1]
}

func tokenThroughClose(line []byte) string {
	end := bytes.IndexByte(line, '}')
	if end < 0 {
		return string(line)
	}
	return string(line[:end+1])
}

func invalidEscape(line []byte) (string, int) {
	for index := 0; index+1 < len(line); index++ {
		if line[index] == '\\' && !bytes.ContainsRune([]byte{'"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u'}, rune(line[index+1])) {
			return string(line[index:]), index + 1
		}
	}
	return "", 0
}

func hasUnclosedArrayValue(line []byte) bool {
	open := bytes.LastIndexByte(line, '[')
	if open < 0 || bytes.LastIndexByte(line, ']') > open {
		return false
	}
	value := bytes.TrimSpace(line[open+1:])
	return len(value) > 0 && value[len(value)-1] != ','
}

// hasUnclosedString recognizes JSON string delimiters while respecting escape
// runs. It is intentionally a lexical helper: encoding/json remains the
// parser, while this only chooses Ruby's EOF diagnostic clause.
func hasUnclosedString(line []byte) bool {
	inString := false
	escaped := false
	for _, char := range line {
		if inString && escaped {
			escaped = false
			continue
		}
		if inString && char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			inString = !inString
		}
	}
	return inString
}
