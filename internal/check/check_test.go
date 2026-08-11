package check

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMetadataAndIDFixtureDiagnosticsMatchRubyOracle(t *testing.T) {
	cases := []struct {
		name string
		path string
		want []Entry
	}{
		{"missing meta", fixture("malformed", "missing-meta", "tasks.jsonl"), []Entry{{1, `line 1 must be a meta record ({"type":"meta","version":2})`}}},
		{"empty", fixture("malformed", "empty-file", "tasks.jsonl"), []Entry{{1, "missing meta record on line 1"}}},
		{"later meta", fixture("malformed", "meta-out-of-place", "tasks.jsonl"), []Entry{{1, `line 1 must be a meta record ({"type":"meta","version":2})`}, {2, "unexpected meta record (only valid on line 1)"}, {8, "unexpected meta record (only valid on line 1)"}}},
		{"duplicate", fixture("malformed", "duplicate-ids", "tasks.jsonl"), []Entry{{4, `duplicate id "c0000002" (lines 3, 4) — id refs will be wrong`}}},
		{"future schema", fixture("compat", "future-schema-v3", "tasks.jsonl"), []Entry{{1, "unsupported meta version 3 (expected 2)"}}},
		{"wrong types", fixture("malformed", "wrong-types", "tasks.jsonl"), []Entry{
			{3, "malformed id 12345678 (expected 8 hex chars)"},
			{4, `invalid state "STARTED" (expected PROPOSED/INBOX/TODO/NEXT/WAITING/DONE/CANCELLED)`},
			{5, `invalid priority "Z" (expected A, B, or C)`},
			{6, "title must be a string"},
			{7, "tags must be an array"},
			{8, "tags must all be strings"},
			{9, "scheduled 2026-02-30 is not a real date"},
			{10, `deadline "14/06/2026" is not a YYYY-MM-DD date`},
			{11, "closed date on an open task (TODO)"},
			{12, `invalid recur cookie "every week" (expected e.g. .+1w, ++1m, +2d, w:mon, m:15, y:07-04)`},
			{13, `lead "3w" with no scheduled date or deadline to hide before`},
			{14, `lead "2d" beside both a scheduled date and a deadline (the two dates already express that window)`},
			{15, "scheduled_time requires scheduled"},
			{16, "scheduled_time.local must be HH:MM"},
			{17, `updated "2026-06-01T10:00:00Z" is not an RFC3339 UTC timestamp with device slug`},
			{18, "delegation on a proposed task (PROPOSED)"},
			{18, "delegation.mode nil must be one of refine/research/implement"},
			{19, `section must not carry "state"`},
			{19, `section must not carry "deadline"`},
			{19, `section must not carry "tags"`},
			{20, `malformed id "short" (expected 8 hex chars)`},
			{21, "record missing id"},
			{22, "task has no title"},
			{23, `unknown record type "widget"`},
		}},
		{"null id", fixture("malformed", "null-id", "tasks.jsonl"), []Entry{
			{2, "record missing id"},
			{2, "invalid state nil (expected PROPOSED/INBOX/TODO/NEXT/WAITING/DONE/CANCELLED)"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Check(tc.path).Errors; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("errors = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestStoreChecksCrossFileDuplicateAgainstRubyOracle(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "malformed", "cross-file-duplicate-id", "store")
	got := CheckStore(filepath.Join(root, "tasks.jsonl"), filepath.Join(root, "archive.jsonl"))
	want := []Entry{{3, `id "c0000002" appears in both tasks.jsonl line 3 and archive.jsonl line 3`}}
	if !reflect.DeepEqual(got.Errors, want) {
		t.Fatalf("errors = %#v, want %#v", got.Errors, want)
	}
}

func TestMissingFileAndMetadataVersionInspectionMatchRubyOracle(t *testing.T) {
	if got, want := Check("/definitely-not-present/tasks.jsonl").Errors, []Entry{{0, "file not found: /definitely-not-present/tasks.jsonl"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing file = %#v, want %#v", got, want)
	}
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"decimal float", `{"type":"meta","version":2.0}`, "unsupported meta version 2.0 (expected 2)"},
		{"exponent float", `{"type":"meta","version":2e0}`, "unsupported meta version 2.0 (expected 2)"},
		{"null", `{"type":"meta","version":null}`, "unsupported meta version nil (expected 2)"},
		{"control characters", `{"type":"meta","version":"\u0000\u0007\u001b\u001f"}`, `unsupported meta version "\u0000\a\e\u001F" (expected 2)`},
		{"object preserves member order", `{"type":"meta","version":{"b":1,"a":2}}`, `unsupported meta version {"b" => 1, "a" => 2} (expected 2)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := CheckText([]byte(tc.text)).Errors, []Entry{{1, tc.want}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("errors = %#v, want %#v", got, want)
			}
		})
	}
}

// A record outside the type vocabulary is reported ONCE, as an unknown type.
// Ruby short-circuits before validating its id, its keys or its fields, so a
// port that fell through would report a missing id for a record it has already
// said it cannot interpret.
func TestUnknownTypeIsTheOnlyDiagnosticItEarns(t *testing.T) {
	text := []byte("{\"type\":\"meta\",\"version\":2}\n{\"type\":\"widget\"}")
	want := []Entry{{2, `unknown record type "widget"`}}
	if got := CheckText(text).Errors; !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown type diagnostics = %#v, want %#v", got, want)
	}
}

// The unknown-key hazard is a WARNING, not an error: the write path preserves a
// key it does not understand, so a store written by a newer binary still
// validates. The delegation object gets the same treatment one level down, and
// both report in the record's own member order.
func TestForwardCompatibleKeysWarnInSourceOrder(t *testing.T) {
	result := Check(fixture("compat", "forward-compat-unknown-keys", "tasks.jsonl"))
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v, want none", result.Errors)
	}
	want := []Entry{
		{3, `unknown key "energy"`},
		{3, `unknown delegation key "budget_tokens"`},
		{4, `unknown key "review_after"`},
	}
	if !reflect.DeepEqual(result.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
	}
}

// Invalid encoding short-circuits everything: the file cannot be split into
// records at all, so a "missing meta record" beside it would describe a file
// nobody could read.
func TestInvalidEncodingIsTheOnlyDiagnostic(t *testing.T) {
	want := []Entry{{0, "file is not valid UTF-8"}}
	if got := CheckText([]byte{0xff, 0xfe, 0x00}).Errors; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
}

func TestFormalLinkValidation(t *testing.T) {
	valid := []byte("{\"type\":\"meta\",\"version\":2}\n" +
		`{"type":"task","id":"aaaa0001","state":"TODO","title":"linked","links":[{"url":"https://example.com/a","label":"A"},{"url":"http://example.org/b"}]}`)
	if got := CheckText(valid).Errors; len(got) != 0 {
		t.Fatalf("valid links errors = %#v", got)
	}

	cases := []struct{ name, links, want string }{
		{"not array", `{}`, "links must be an array"},
		{"entry not object", `["https://example.com"]`, "links[0] must be an object"},
		{"missing url", `[{"label":"x"}]`, "links[0].url must be a string"},
		{"wrong scheme", `[{"url":"file:///tmp/x"}]`, "links[0].url must be an http or https URL with a host"},
		{"duplicate", `[{"url":"https://example.com"},{"url":"https://example.com"}]`, "links[1].url duplicates an earlier formal link"},
		{"empty label", `[{"url":"https://example.com","label":""}]`, "links[0].label must be non-empty and trimmed"},
		{"unknown member", `[{"url":"https://example.com","description":"x"}]`, `links[0] has unknown key "description"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := []byte("{\"type\":\"meta\",\"version\":2}\n" +
				`{"type":"task","id":"aaaa0001","state":"TODO","title":"linked","links":` + tc.links + `}`)
			got := CheckText(text).Errors
			found := false
			for _, entry := range got {
				if entry.Message == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors = %#v, want message %q", got, tc.want)
			}
		})
	}

	section := []byte("{\"type\":\"meta\",\"version\":2}\n" +
		`{"type":"section","id":"aaaa0001","title":"S","links":[{"url":"https://example.com"}]}`)
	if got := CheckText(section).Errors; !containsEntry(got, `section must not carry "links"`) {
		t.Fatalf("section errors = %#v", got)
	}

	entry := `{"url":"https://example.com/unique"}`
	tooMany := "[" + strings.TrimSuffix(strings.Repeat(entry+",", 51), ",") + "]"
	countText := []byte("{\"type\":\"meta\",\"version\":2}\n" +
		`{"type":"task","id":"aaaa0001","state":"TODO","title":"linked","links":` + tooMany + `}`)
	if got := CheckText(countText).Errors; !containsEntry(got, "links has 51 entries (maximum 50)") {
		t.Fatalf("count errors = %#v", got)
	}
}

func containsEntry(entries []Entry, message string) bool {
	for _, entry := range entries {
		if entry.Message == message {
			return true
		}
	}
	return false
}

// Duplicate open titles are the hazard that makes a fuzzy ref ambiguous, which
// is a warning rather than an error: the store is still coherent, the human
// just cannot name one of them uniquely.
func TestDuplicateOpenTitlesWarn(t *testing.T) {
	result := Check(fixture("malformed", "duplicate-open-titles", "tasks.jsonl"))
	want := []Entry{{8, `duplicate open title "replace the bathroom bulb" (lines 3, 8) — fuzzy refs will be ambiguous`}}
	if !reflect.DeepEqual(result.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
	}
}

func TestIDGrammarProperty(t *testing.T) {
	alphabet := "0123456789abcdefABCDEF-"
	for index := 0; index < 500; index++ {
		length := rand.IntN(14)
		var id strings.Builder
		for char := 0; char < length; char++ {
			id.WriteByte(alphabet[rand.IntN(len(alphabet))])
		}
		text := fmt.Sprintf("{\"type\":\"meta\",\"version\":2}\n{\"type\":\"task\",\"id\":%q}", id.String())
		// The stateless task in the fixture text earns its own diagnostics; the
		// property under test is only whether the ID grammar fired.
		got := []Entry{}
		for _, entry := range CheckText([]byte(text)).Errors {
			if strings.HasPrefix(entry.Message, "malformed id ") || entry.Message == "record missing id" {
				got = append(got, entry)
			}
		}
		valid := len(id.String()) == 8
		for _, char := range id.String() {
			valid = valid && (char >= '0' && char <= '9' || char >= 'a' && char <= 'f')
		}
		if valid && len(got) != 0 {
			t.Fatalf("valid id %q yielded %#v", id.String(), got)
		}
		if !valid && (len(got) != 1 || got[0].Line != 2) {
			t.Fatalf("invalid id %q yielded %#v", id.String(), got)
		}
	}
}

func fixture(class, name, file string) string {
	return filepath.Join("..", "..", "testdata", "fixtures", class, name, "store", file)
}
