package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The note is the receiver's briefing. It is optional, bounded, and — unlike
// work_ref — allowed to have paragraphs.
func TestDelegationNoteShape(t *testing.T) {
	valid := map[string]any{"kind": "agent", "mode": "research", "status": "ready",
		"at": "2026-07-27T18:04:11Z", "note": "Read docs/plan.md first.\nLand the work on a branch."}
	if got := DelegationErrors(valid); len(got) != 0 {
		t.Fatalf("errors = %#v, want a multi-line note accepted", got)
	}
	for _, tc := range []struct {
		want string
		note any
	}{
		{"delegation.note must be a non-empty string", "   "},
		{"delegation.note must be a non-empty string", 7},
		{"delegation.note must not contain control characters", "clear the screen \x1b[2K"},
		{"delegation.note must be at most 2000 characters (got 2001)", strings.Repeat("x", 2001)},
	} {
		marker := map[string]any{"kind": "agent", "mode": "research", "status": "ready",
			"at": "2026-07-27T18:04:11Z", "note": tc.note}
		got := DelegationErrors(marker)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("errors = %#v, want %q", got, tc.want)
		}
	}
	if got := DelegationErrors(map[string]any{"kind": "agent", "mode": "research",
		"status": "ready", "at": "2026-07-27T18:04:11Z", "note": strings.Repeat("x", 2000)}); len(got) != 0 {
		t.Fatalf("errors = %#v, want the bound itself accepted", got)
	}
	if got := DelegationNoteErrors("fine"); got != nil {
		t.Fatalf("errors = %#v", got)
	}
}

// note is a KNOWN key and emits last, so a record that predates it keeps its
// byte layout while a record that carries one has a stable place for it.
func TestNoteIsTheLastDeclaredKey(t *testing.T) {
	if last := DelegationKeyOrder[len(DelegationKeyOrder)-1]; last != "note" {
		t.Fatalf("last declared key = %q, want note appended after work_ref", last)
	}
	marker := map[string]any{"note": "brief", "kind": "agent", "mode": "research",
		"status": "ready", "at": "2026-07-27T18:04:11Z", "work_ref": "https://example.invalid/x"}
	want := []string{"kind", "mode", "status", "at", "work_ref", "note"}
	if got := DelegationOrderedKeys(marker, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered keys = %#v, want %#v", got, want)
	}
	if got := DelegationUnknownKeys(marker); len(got) != 0 {
		t.Fatalf("unknown keys = %#v, want note to be known", got)
	}
}

// Who holds the work and what KIND of delegation it is are orthogonal, so a
// person can be handed a task in a mode. Membership is still enforced.
func TestModeIsAllowedOnAHumanDelegation(t *testing.T) {
	human := map[string]any{"kind": "human", "mode": "refine", "status": "delegated",
		"assignee": "pat@example.com", "at": "2026-07-27T18:04:11Z", "note": "tighten the acceptance criteria"}
	if got := DelegationErrors(human); len(got) != 0 {
		t.Fatalf("errors = %#v, want a human delegation with a mode accepted", got)
	}
	human["mode"] = "vibes"
	want := `delegation.mode "vibes" must be one of refine/research/implement`
	if got := DelegationErrors(human); len(got) != 1 || got[0] != want {
		t.Fatalf("errors = %#v, want %q", got, want)
	}
	// A mode remains REQUIRED for an agent: the pool needs to know the authority.
	agent := map[string]any{"kind": "agent", "status": "ready", "at": "2026-07-27T18:04:11Z"}
	if got := DelegationErrors(agent); len(got) != 1 || !strings.Contains(got[0], "must be one of") {
		t.Fatalf("errors = %#v, want a missing agent mode refused", got)
	}
}

// The vocabulary is ONE seam, and it is a VALUE. Feeding a different set
// changes membership and the refusal wording for that call and no other.
func TestModeVocabularyIsInjectable(t *testing.T) {
	custom := ModeSet{"triage", "implement"}
	marker := map[string]any{"kind": "agent", "mode": "research", "status": "ready", "at": "2026-07-27T18:04:11Z"}
	want := `delegation.mode "research" must be one of triage/implement`
	if got := DelegationErrorsWith(marker, custom); len(got) != 1 || got[0] != want {
		t.Fatalf("errors = %#v, want %q", got, want)
	}
	marker["mode"] = "triage"
	if got := DelegationErrorsWith(marker, custom); len(got) != 0 {
		t.Fatalf("errors = %#v, want the injected vocabulary honoured", got)
	}
	// Nil means the built-in set, at every entry point.
	if got := Modes(nil).Quoted(); got != "refine/research/implement" {
		t.Fatalf("quoted = %q", got)
	}
	if got := DelegationErrorsWith(marker, nil); len(got) != 1 ||
		got[0] != `delegation.mode "triage" must be one of refine/research/implement` {
		t.Fatalf("errors = %#v", got)
	}
	if got := BuiltinModes().Modes(); !reflect.DeepEqual(got, []string{"refine", "research", "implement"}) {
		t.Fatalf("builtin modes = %#v", got)
	}
	if BuiltinModes().Valid("vibes") {
		t.Fatal("vibes is not a built-in mode")
	}
}

// THE REGRESSION THAT KEEPS THE SEAM A VALUE.
//
// An earlier draft held the vocabulary in a package-level slot with a setter.
// It was race-free and still wrong: one caller's configuration became every
// other caller's, and these two tests — parallel, as any pair of tests may be —
// failed each other. They pass now only because there is nothing to set. If
// someone reintroduces process-wide mode state, this pair fails again.
func TestParallelCallerAConfiguresItsOwnVocabulary(t *testing.T) {
	t.Parallel()
	marker := map[string]any{"kind": "agent", "mode": "triage", "status": "ready", "at": "2026-07-27T18:04:11Z"}
	for i := 0; i < 200; i++ {
		if got := DelegationErrorsWith(marker, ModeSet{"triage"}); len(got) != 0 {
			t.Fatalf("errors = %#v, want caller A's own vocabulary", got)
		}
	}
}

func TestParallelCallerBIsUnaffectedByCallerA(t *testing.T) {
	t.Parallel()
	marker := map[string]any{"kind": "agent", "mode": "research", "status": "ready", "at": "2026-07-27T18:04:11Z"}
	for i := 0; i < 200; i++ {
		if got := DelegationErrors(marker); len(got) != 0 {
			t.Fatalf("errors = %#v, want the built-in vocabulary uncontaminated", got)
		}
	}
}

// The hard compatibility guarantee: a store written by the CURRENT release —
// markers with no note — parses and re-emits byte for byte. Adding a key to the
// end of the order must be invisible to every record that does not use it.
func TestPreNoteStoreReEmitsByteIdentical(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "fixtures", "compat", "delegation-pre-note", "store", "tasks.jsonl")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed := Parse(original)
	if !parsed.OK() {
		t.Fatalf("parse errors = %#v", parsed.Errors)
	}
	for _, item := range parsed.Records {
		for _, field := range item.Fields {
			if field.Key != DelegationField {
				continue
			}
			if got := DelegationErrors(decodeMarker(t, field)); len(got) != 0 {
				t.Fatalf("line %d: errors = %#v", item.Line, got)
			}
		}
	}
	dumped, err := Dump(parsed.Records)
	if err != nil {
		t.Fatal(err)
	}
	if dumped != string(original) {
		t.Fatalf("re-emitted bytes differ:\n%s\nwant:\n%s", dumped, original)
	}
}

func decodeMarker(t *testing.T, field Field) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(field.Value, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
