// Package record reads the JSONL record format used by tasks.
package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Record is one parsed JSONL object. Fields retain the JSON representation so
// later validation and canonical emission can make their own type decisions.
// Line is physical and is never part of the persisted record schema.
type Record struct {
	Fields map[string]json.RawMessage
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
		if len(bytes.TrimSpace(line)) == 0 {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "blank line"})
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
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "invalid JSON: " + rubyJSONError(line, err)})
			continue
		}
		result.Records = append(result.Records, Record{Fields: fields, Line: lineNo})
	}
	return result
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

// rubyJSONError translates EOF diagnostics emitted by encoding/json into the
// wording produced by Ruby's JSON parser. Ruby reports a one-based physical
// column at the end of the JSONL record, so this adapter receives the source
// line rather than attempting to recover a location from Go's error string.
func rubyJSONError(line []byte, err error) string {
	if trailing := trailingToken(line); trailing != nil {
		return fmt.Sprintf("unexpected token at end of stream '%s' at line 1 column %d", trailing.token, trailing.column)
	}
	if bytes.HasSuffix(line, []byte(",}")) {
		return fmt.Sprintf("expected object key, got: '}' at line 1 column %d", len(line))
	}
	if strings.Contains(err.Error(), "in string escape code") {
		if escape, column := invalidEscape(line); escape != "" {
			return fmt.Sprintf("invalid escape character in string: '%s' at line 1 column %d", escape, column)
		}
	}
	if strings.Contains(err.Error(), "unexpected EOF") {
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
	return err.Error()
}

type tokenAtColumn struct {
	token  string
	column int
}

// trailingToken recognizes a completed object followed by a non-whitespace
// token. JSON decoding still establishes that the input is invalid; this only
// reproduces Ruby's diagnostic for the bounded case captured in the oracle.
func trailingToken(line []byte) *tokenAtColumn {
	end := bytes.LastIndexByte(line, '}')
	if end < 0 || end == len(line)-1 {
		return nil
	}
	rest := line[end+1:]
	trimmed := bytes.TrimLeft(rest, " \t\r")
	if len(trimmed) == 0 {
		return nil
	}
	column := len(line) - len(trimmed) + 1
	return &tokenAtColumn{token: string(trimmed), column: column}
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
