// Package check validates the store-wide metadata and ID invariants.
package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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

// CheckText validates a whole file's bytes. Invalid encoding short-circuits
// everything else: the file cannot be split into records at all, so reporting
// a missing meta record beside it would describe a file nobody could read.
func CheckText(input []byte) Result {
	if !utf8.Valid(input) {
		return Result{Errors: []Entry{{Line: 0, Message: "file is not valid UTF-8"}}, Warnings: []Entry{}}
	}
	return CheckParsed(record.Parse(input))
}

// CheckParsed validates bytes a caller has already parsed. It is the seam the
// store's API-grade read uses: validation and the canonical resources have to
// derive from ONE read taken under the store lock, rather than from a path
// checked and then reopened.
func CheckParsed(parsed record.Result) Result { return CheckParsedVersion(parsed, Version) }

// CheckParsedVersion is CheckParsed against a stated schema version rather than
// this build's. Only one caller needs it: the merge validates a common ancestor
// against the version that ancestor DECLARES, because a v1 base under two v2
// sides is the ordinary shape of a merge reaching back past a schema upgrade,
// and a faithful v1 file must not fail the lint for being one.
func CheckParsedVersion(parsed record.Result, version int) Result {
	errors := make([]Entry, 0, len(parsed.Errors)+1)
	for _, parseErr := range parsed.Errors {
		errors = append(errors, Entry{Line: parseErr.Line, Message: parseErr.Message})
	}
	warnings := make([]Entry, 0)

	checkMeta(parsed.Records, &errors, version)
	duplicates := &duplicateIndex{lines: map[string][]int{}}
	validate(parsed.Records, &errors, &warnings, duplicates)
	for _, id := range duplicates.order {
		lines := duplicates.lines[id]
		if len(lines) < 2 {
			continue
		}
		errors = append(errors, Entry{Line: lines[len(lines)-1],
			Message: fmt.Sprintf("duplicate id %s (lines %s) — id refs will be wrong", rubyInspectString(id), joinLines(lines))})
	}
	sortEntries(errors)
	sortEntries(warnings)
	return Result{Errors: errors, Warnings: warnings}
}

// duplicateIndex is id → the lines it appeared on, in first-seen order. Ruby
// walks its Hash in insertion order when it emits these diagnostics, and Go map
// iteration would reorder two ids duplicated on the same line.
type duplicateIndex struct {
	lines map[string][]int
	order []string
}

func (d *duplicateIndex) add(id string, line int) {
	if _, seen := d.lines[id]; !seen {
		d.order = append(d.order, id)
	}
	d.lines[id] = append(d.lines[id], line)
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
	warnings := append(annotate(live.Warnings, "tasks.jsonl"), annotate(archive.Warnings, "archive.jsonl")...)
	sortEntries(errors)
	// Ruby sorts BOTH lists by line. Sorting only the errors left the warnings
	// interleaved live-then-archive, which reads as two files' diagnostics
	// concatenated rather than one store's report.
	sortEntries(warnings)
	return Result{Errors: errors, Warnings: warnings}
}

// UnsupportedVersion reports the version a store DECLARES when it is one this
// binary cannot read. The gate is deliberately narrow in two ways Ruby is
// narrow: it consults the FIRST record rather than the record on line 1, and a
// non-Integer version is not "unsupported" — it is malformed, and checkMeta
// reports it as such.
func UnsupportedVersion(records []record.Record) (json.RawMessage, bool) {
	if len(records) == 0 {
		return nil, false
	}
	first := records[0]
	if stringField(first, "type") != "meta" {
		return nil, false
	}
	raw := rawField(first, "version")
	version, ok := strictInteger(raw)
	if !ok || version == Version {
		return nil, false
	}
	return raw, true
}

// strictInteger is Ruby's `declared.is_a?(Integer)` test. It cannot be
// integerField: encoding/json accepts a JSON null into an int and leaves the
// zero value behind, which would make a `"version":null` store report itself
// as version 0 — an UNSUPPORTED schema rather than the malformed meta record
// it actually is, and the two statuses send a caller somewhere different.
func strictInteger(raw json.RawMessage) (int, bool) {
	if raw == nil || string(bytes.TrimSpace(raw)) == "null" {
		return 0, false
	}
	var value int
	return value, json.Unmarshal(raw, &value) == nil
}

// UnsupportedVersionMessage is the one wording for that condition, shared by
// `check`, by every refusal, and by the store's read path.
func UnsupportedVersionMessage(declared json.RawMessage) string {
	return fmt.Sprintf("unsupported meta version %s (expected %d)", rubyInspect(declared), Version)
}

func checkMeta(records []record.Record, errors *[]Entry, version int) {
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
		*errors = append(*errors, Entry{Line: 1,
			Message: fmt.Sprintf(`line 1 must be a meta record ({"type":"meta","version":%d})`, version)})
		return
	}
	declared, ok := integerField(*first, "version")
	if !ok || declared != version {
		*errors = append(*errors, Entry{Line: 1,
			Message: fmt.Sprintf("unsupported meta version %s (expected %d)", rubyInspect(rawField(*first, "version")), version)})
	}
}

func checkID(parsed record.Record, errors *[]Entry, duplicates *duplicateIndex) {
	raw := rawField(parsed, "id")
	if raw == nil || string(raw) == `""` || string(raw) == "null" {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: "record missing id"})
		return
	}
	id, ok := decodeString(raw)
	if !ok || !idPattern.MatchString(id) {
		*errors = append(*errors, Entry{Line: parsed.Line, Message: fmt.Sprintf("malformed id %s (expected 8 hex chars)", rubyInspect(raw))})
		return
	}
	duplicates.add(id, parsed.Line)
}

// crossFileDuplicates reports every id visible in BOTH stores. Ruby emits them
// in sorted id order; Go map iteration is randomized, so two ids duplicated on
// the same line would otherwise swap places between runs — and the stable sort
// by line that follows cannot put them back.
func crossFileDuplicates(livePath, archivePath string) []Entry {
	live := idsFor(livePath)
	archive := idsFor(archivePath)
	shared := make([]string, 0)
	for id := range archive {
		if _, exists := live[id]; exists {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	errors := make([]Entry, 0, len(shared))
	for _, id := range shared {
		errors = append(errors, Entry{Line: archive[id],
			Message: fmt.Sprintf("id %q appears in both tasks.jsonl line %d and archive.jsonl line %d", id, live[id], archive[id])})
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := rubyInspectJSONValue(decoder)
	if err != nil {
		return string(raw)
	}
	return value
}

// rubyInspectJSONValue renders a JSON value with the Ruby Object#inspect
// spelling Check exposes in diagnostics. It walks decoder tokens rather than
// decoding objects into a map, because Ruby Hash#inspect preserves JSON member
// insertion order while Go map iteration does not.
func rubyInspectJSONValue(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	switch typed := token.(type) {
	case nil:
		return "nil", nil
	case bool:
		return strconv.FormatBool(typed), nil
	case string:
		return rubyInspectString(typed), nil
	case json.Number:
		return rubyInspectNumber(string(typed)), nil
	case json.Delim:
		switch typed {
		case '[':
			items := []string{}
			for decoder.More() {
				item, err := rubyInspectJSONValue(decoder)
				if err != nil {
					return "", err
				}
				items = append(items, item)
			}
			if _, err := decoder.Token(); err != nil {
				return "", err
			}
			return "[" + strings.Join(items, ", ") + "]", nil
		case '{':
			pairs := []string{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return "", err
				}
				keyString, ok := key.(string)
				if !ok {
					return "", fmt.Errorf("object key is not a string")
				}
				value, err := rubyInspectJSONValue(decoder)
				if err != nil {
					return "", err
				}
				pairs = append(pairs, rubyInspectString(keyString)+" => "+value)
			}
			if _, err := decoder.Token(); err != nil {
				return "", err
			}
			return "{" + strings.Join(pairs, ", ") + "}", nil
		}
		return "", fmt.Errorf("unexpected JSON delimiter %q", typed)
	default:
		return "", fmt.Errorf("unexpected JSON token %T", token)
	}
}

// rubyInspectString matches Ruby String#inspect for the valid UTF-8 strings
// JSON can decode. strconv.Quote differs for several control characters: for
// example Ruby spells ESC as \e and NUL as \u0000, while Go uses \x1b and
// \x00. Check exposes these diagnostics directly, so the spelling is part of
// the compatibility contract.
func rubyInspectString(value string) string {
	var result strings.Builder
	result.WriteByte('"')
	for _, char := range value {
		switch char {
		case '\\':
			result.WriteString(`\\`)
		case '"':
			result.WriteString(`\"`)
		case '\a':
			result.WriteString(`\a`)
		case '\b':
			result.WriteString(`\b`)
		case '\t':
			result.WriteString(`\t`)
		case '\n':
			result.WriteString(`\n`)
		case '\v':
			result.WriteString(`\v`)
		case '\f':
			result.WriteString(`\f`)
		case '\r':
			result.WriteString(`\r`)
		case 0x1b:
			result.WriteString(`\e`)
		default:
			if char < 0x20 || (char >= 0x7f && char <= 0x9f) || char == 0x2028 || char == 0x2029 {
				fmt.Fprintf(&result, `\u%04X`, char)
			} else {
				result.WriteRune(char)
			}
		}
	}
	result.WriteByte('"')
	return result.String()
}

func rubyInspectNumber(raw string) string {
	if !strings.ContainsAny(raw, ".eE") {
		return raw
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	formatted := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(formatted, ".eE") {
		return formatted + ".0"
	}
	if !strings.Contains(formatted, ".") {
		if exponent := strings.IndexAny(formatted, "eE"); exponent >= 0 {
			formatted = formatted[:exponent] + ".0" + formatted[exponent:]
		}
	}
	return formatted
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
