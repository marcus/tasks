// Package check validates the store-wide metadata and ID invariants.
package check

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"

	"tasks-go/internal/record"
)

const Version = 2

var idPattern = regexp.MustCompile(`\A[0-9a-f]{8}\z`)

// Entry is one structural diagnostic, ordered by physical line.
type Entry struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Result is the subset of the Ruby check report this package owns so far.
// Future check slices append task-field and tree diagnostics to the same
// report at the application boundary.
type Result struct {
	Errors   []Entry `json:"errors"`
	Warnings []Entry `json:"warnings"`
}

func (r Result) OK() bool { return len(r.Errors) == 0 }

// Check reads one JSONL store. A missing store is a structural result, not an
// I/O error, just as it is in the Ruby oracle.
func Check(path string) Result {
	input, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Result{Errors: []Entry{{Line: 0, Message: fmt.Sprintf("file not found: %s", path)}}}
	}
	if err != nil {
		return Result{Errors: []Entry{{Line: 0, Message: err.Error()}}}
	}
	return CheckText(input)
}

// CheckText validates parsed metadata and IDs while retaining parser errors.
func CheckText(input []byte) Result {
	parsed := record.Parse(input)
	errors := make([]Entry, 0, len(parsed.Errors)+1)
	for _, parseErr := range parsed.Errors {
		errors = append(errors, Entry{Line: parseErr.Line, Message: parseErr.Message})
	}

	checkMeta(parsed.Records, &errors)
	duplicates := map[string][]int{}
	for _, parsedRecord := range parsed.Records {
		if stringField(parsedRecord, "type") == "meta" {
			if parsedRecord.Line != 1 {
				errors = append(errors, Entry{Line: parsedRecord.Line, Message: "unexpected meta record (only valid on line 1)"})
			}
			continue
		}
		checkID(parsedRecord, &errors, duplicates)
	}
	for id, lines := range duplicates {
		if len(lines) > 1 {
			errors = append(errors, Entry{Line: lines[len(lines)-1], Message: fmt.Sprintf("duplicate id %q (lines %s) — id refs will be wrong", id, joinLines(lines))})
		}
	}
	sortEntries(errors)
	return Result{Errors: errors, Warnings: []Entry{}}
}

// CheckStore validates both stores and rejects an ID visible in each file.
func CheckStore(livePath, archivePath string) Result {
	live := Check(livePath)
	archive := Result{}
	if _, err := os.Stat(archivePath); err == nil {
		archive = Check(archivePath)
	}
	errors := annotate(live.Errors, "tasks.jsonl")
	errors = append(errors, annotate(archive.Errors, "archive.jsonl")...)
	errors = append(errors, crossFileDuplicates(livePath, archivePath)...)
	sortEntries(errors)
	return Result{Errors: errors, Warnings: append(annotate(live.Warnings, "tasks.jsonl"), annotate(archive.Warnings, "archive.jsonl")...)}
}

func checkMeta(records []record.Record, errors *[]Entry) {
	var first *record.Record
	for index := range records {
		if records[index].Line == 1 {
			first = &records[index]
			break
		}
	}
	if first == nil {
		for _, entry := range *errors {
			if entry.Line == 1 {
				return
			}
		}
		*errors = append(*errors, Entry{Line: 1, Message: "missing meta record on line 1"})
		return
	}
	if stringField(*first, "type") != "meta" {
		*errors = append(*errors, Entry{Line: 1, Message: `line 1 must be a meta record ({"type":"meta","version":2})`})
		return
	}
	version, ok := integerField(*first, "version")
	if !ok || version != Version {
		*errors = append(*errors, Entry{Line: 1, Message: fmt.Sprintf("unsupported meta version %s (expected 2)", rubyInspect(rawField(*first, "version")))})
	}
}

func checkID(parsed record.Record, errors *[]Entry, duplicates map[string][]int) {
	raw := rawField(parsed, "id")
	if raw == nil || string(raw) == `""` {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: "record missing id"})
		return
	}
	id, ok := decodeString(raw)
	if !ok || !idPattern.MatchString(id) {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: fmt.Sprintf("malformed id %s (expected 8 hex chars)", rubyInspect(raw))})
		return
	}
	duplicates[id] = append(duplicates[id], parsed.Line)
}

func crossFileDuplicates(livePath, archivePath string) []Entry {
	live := idsFor(livePath)
	archive := idsFor(archivePath)
	errors := make([]Entry, 0)
	for id, archiveLine := range archive {
		if liveLine, exists := live[id]; exists {
			errors = append(errors, Entry{Line: archiveLine, Message: fmt.Sprintf("id %q appears in both tasks.jsonl line %d and archive.jsonl line %d", id, liveLine, archiveLine)})
		}
	}
	return errors
}

func idsFor(path string) map[string]int {
	input, err := os.ReadFile(path)
	if err != nil {
		return map[string]int{}
	}
	ids := map[string]int{}
	for _, parsed := range record.Parse(input).Records {
		if id, ok := decodeString(rawField(parsed, "id")); ok {
			if _, exists := ids[id]; !exists {
				ids[id] = parsed.Line
			}
		}
	}
	return ids
}

func rawField(parsed record.Record, key string) json.RawMessage {
	for _, field := range parsed.Fields {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

func stringField(parsed record.Record, key string) string {
	value, _ := decodeString(rawField(parsed, key))
	return value
}

func decodeString(raw json.RawMessage) (string, bool) {
	var value string
	if raw == nil || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func integerField(parsed record.Record, key string) (int, bool) {
	var value int
	raw := rawField(parsed, key)
	return value, raw != nil && json.Unmarshal(raw, &value) == nil
}

func rubyInspect(raw json.RawMessage) string {
	if raw == nil {
		return "nil"
	}
	return string(raw)
}

func joinLines(lines []int) string {
	result := ""
	for index, line := range lines {
		if index > 0 {
			result += ", "
		}
		result += fmt.Sprint(line)
	}
	return result
}

func annotate(entries []Entry, source string) []Entry {
	annotated := make([]Entry, len(entries))
	for index, entry := range entries {
		annotated[index] = Entry{Line: entry.Line, Message: source + ": " + entry.Message}
	}
	return annotated
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].Line < entries[right].Line })
}
