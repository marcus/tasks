// Package record reads the JSONL record format used by tasks.
package record

import (
	"bytes"
	"encoding/json"
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
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "invalid JSON: " + rubyJSONError(err)})
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "expected a JSON object, got " + rubyClass(value)})
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			result.Errors = append(result.Errors, ParseError{Line: lineNo, Message: "invalid JSON: " + rubyJSONError(err)})
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

// rubyJSONError keeps the stable portion of Ruby's parser diagnostics. The
// exact token/location rendering is completed with the CLI error adapter,
// where the conformance runner observes it; Go's encoding/json diagnostics are
// otherwise implementation-specific.
func rubyJSONError(err error) string {
	if strings.Contains(err.Error(), "unexpected EOF") {
		return "unexpected end of input"
	}
	return err.Error()
}
