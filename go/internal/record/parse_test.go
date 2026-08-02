package record

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestParsePreservesPhysicalLinesAcrossErrors(t *testing.T) {
	result := Parse([]byte("{\"type\":\"meta\"}\nnot json\n{\"type\":\"task\",\"title\":\"after\"}\n42\n"))

	if got, want := recordLines(result), []int{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("record lines = %v, want %v", got, want)
	}
	if got, want := errorLines(result), []int{2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("error lines = %v, want %v", got, want)
	}
	if got := result.Errors[1].Message; got != "expected a JSON object, got Integer" {
		t.Fatalf("scalar error = %q", got)
	}
}

func TestParseBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		input   []byte
		records []int
		errors  []ParseError
	}{
		{name: "empty", input: []byte{}, records: []int{}},
		{name: "trailing newline", input: []byte("{\"type\":\"meta\"}\n"), records: []int{1}},
		{name: "lone newline", input: []byte("\n"), records: []int{}, errors: []ParseError{{Line: 1, Message: "blank line"}}},
		{name: "bom", input: append([]byte{0xef, 0xbb, 0xbf}, []byte("{\"type\":\"meta\"}\n")...), records: []int{1}},
		{name: "bad utf8", input: []byte{0xff}, records: []int{}, errors: []ParseError{{Line: 0, Message: "file is not valid UTF-8"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.input)
			if lines := recordLines(got); !reflect.DeepEqual(lines, tc.records) {
				t.Fatalf("record lines = %v, want %v", lines, tc.records)
			}
			if !reflect.DeepEqual(got.Errors, tc.errors) {
				t.Fatalf("errors = %#v, want %#v", got.Errors, tc.errors)
			}
		})
	}
}

func TestParseFixtureInputsRemainLenient(t *testing.T) {
	fixtures := []struct {
		name        string
		recordCount int
		errorLines  []int
	}{
		{name: "invalid-json", recordCount: 6, errorLines: []int{4}},
		{name: "non-record-lines", recordCount: 7, errorLines: []int{4, 6, 8}},
		{name: "truncated-final-line", recordCount: 6, errorLines: []int{7}},
		{name: "wrong-key-order", recordCount: 7, errorLines: []int{}},
		{name: "bom-prefixed", recordCount: 2, errorLines: []int{}},
		{name: "mid-write-torn-file", recordCount: 6, errorLines: []int{7}},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "porting", "fixtures", fixtureClass(fixture.name), fixture.name, "store", "tasks.jsonl")
			input, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := Parse(input)
			if len(got.Records) != fixture.recordCount {
				t.Fatalf("records = %d, want %d", len(got.Records), fixture.recordCount)
			}
			if lines := errorLines(got); !reflect.DeepEqual(lines, fixture.errorLines) {
				t.Fatalf("error lines = %v, want %v", lines, fixture.errorLines)
			}
		})
	}
}

func FuzzParseKeepsPhysicalLineBounds(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte("\n"),
		[]byte("{\"type\":\"meta\"}\nnot json\n42\n"),
		{0xef, 0xbb, 0xbf, '{', '}'},
		{0xff},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		result := Parse(input)
		if !utf8.Valid(input) {
			if !reflect.DeepEqual(result.Errors, []ParseError{{Line: 0, Message: "file is not valid UTF-8"}}) {
				t.Fatalf("invalid UTF-8 result = %#v", result)
			}
			return
		}

		lineCount := bytes.Count(input, []byte{'\n'}) + 1
		if len(input) == 0 {
			lineCount = 0
		}
		for _, record := range result.Records {
			if record.Line < 1 || record.Line > lineCount {
				t.Fatalf("record line %d outside 1..%d", record.Line, lineCount)
			}
		}
		for _, parseError := range result.Errors {
			if parseError.Line < 1 || parseError.Line > lineCount {
				t.Fatalf("error line %d outside 1..%d", parseError.Line, lineCount)
			}
		}
	})
}

func fixtureClass(name string) string {
	if name == "mid-write-torn-file" {
		return "adversarial"
	}
	if name == "wrong-key-order" || name == "bom-prefixed" {
		return "malformed"
	}
	return "malformed"
}

func recordLines(result Result) []int {
	lines := make([]int, len(result.Records))
	for index, record := range result.Records {
		lines[index] = record.Line
	}
	return lines
}

func errorLines(result Result) []int {
	lines := make([]int, len(result.Errors))
	for index, parseError := range result.Errors {
		lines[index] = parseError.Line
	}
	return lines
}
