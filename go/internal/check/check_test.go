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
		{"non string id", fixture("malformed", "wrong-types", "tasks.jsonl"), []Entry{{3, "malformed id 12345678 (expected 8 hex chars)"}, {20, `malformed id "short" (expected 8 hex chars)`}, {21, "record missing id"}}},
		{"null id", fixture("malformed", "null-id", "tasks.jsonl"), []Entry{{2, "record missing id"}}},
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
	root := filepath.Join("..", "..", "..", "porting", "fixtures", "malformed", "cross-file-duplicate-id", "store")
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

func TestUnknownTypeDoesNotAlsoValidateID(t *testing.T) {
	text := []byte("{\"type\":\"meta\",\"version\":2}\n{\"type\":\"widget\"}")
	if got := CheckText(text).Errors; len(got) != 0 {
		t.Fatalf("unknown type ID diagnostics = %#v, want none until the shared report owns unknown types", got)
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
		got := CheckText([]byte(text)).Errors
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
	return filepath.Join("..", "..", "..", "porting", "fixtures", class, name, "store", file)
}
