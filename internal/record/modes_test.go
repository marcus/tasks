package record

import (
	"reflect"
	"strings"
	"testing"
)

// ParseModeList is the ONE parser for a configured vocabulary. The syntax is
// deliberately boring — bare words separated by commas, no labels — so this is
// the whole of what it must accept and refuse.
func TestParseModeListAcceptsABoringList(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  ModeSet
	}{
		{"refine, research, implement", ModeSet{"refine", "research", "implement"}},
		{"triage,ship", ModeSet{"triage", "ship"}},
		{"  triage ,   ship  ", ModeSet{"triage", "ship"}},
		{"implement", ModeSet{"implement"}},
		{"deep_research, tier2", ModeSet{"deep_research", "tier2"}},
		// A trailing comma is a typo, not a refusal: nothing is ambiguous
		// about it and refusing would cost the user their whole vocabulary.
		{"triage, ship,", ModeSet{"triage", "ship"}},
	} {
		got, problem := ParseModeList(tc.input)
		if problem != "" {
			t.Fatalf("%q: %s", tc.input, problem)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q => %#v, want %#v", tc.input, got, tc.want)
		}
	}
}

func TestParseModeListRefusesAListItCannotHonour(t *testing.T) {
	for input, want := range map[string]string{
		"Triage":            "is not a mode name",
		"ship!":             "is not a mode name",
		"2fast":             "is not a mode name",
		"deep research":     "is not a mode name",
		"triage, triage":    "is listed twice",
		"":                  "the list is empty",
		"  ,  ":             "the list is empty",
		"triage, ship, TOP": "is not a mode name",
	} {
		got, problem := ParseModeList(input)
		if problem == "" {
			t.Fatalf("%q was accepted as %#v", input, got)
		}
		if !strings.Contains(problem, want) {
			t.Fatalf("%q => %q, want it to contain %q", input, problem, want)
		}
		if got != nil {
			t.Fatalf("%q returned a partial list %#v; degradation is total", input, got)
		}
	}
}

// The two questions a mode raises are separate, and the on-disk validator is
// where that separation earns its keep: SHAPE is schema and always an error;
// MEMBERSHIP is configuration, and configuration changes, so a record written
// under another vocabulary warns instead of invalidating the file.
func TestStoredModesSeparateShapeFromMembership(t *testing.T) {
	vocabulary := ModeSet{"triage", "ship"}
	marker := func(mode any) map[string]any {
		return map[string]any{"kind": "agent", "mode": mode, "status": "ready", "at": "2026-07-20T11:00:00Z"}
	}

	errors, warnings := DelegationStoredErrors(marker("research"), vocabulary)
	if len(errors) != 0 {
		t.Fatalf("an unconfigured mode invalidated the record: %v", errors)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `delegation.mode "research" is not in the configured vocabulary triage/ship`) {
		t.Fatalf("warnings = %v", warnings)
	}

	if errors, warnings := DelegationStoredErrors(marker("ship"), vocabulary); len(errors) != 0 || len(warnings) != 0 {
		t.Fatalf("a configured mode was not clean: %v / %v", errors, warnings)
	}

	for _, bad := range []any{nil, 7, "Research", "sh ip", ""} {
		errors, warnings := DelegationStoredErrors(marker(bad), vocabulary)
		if len(errors) == 0 {
			t.Fatalf("mode %#v was accepted on disk", bad)
		}
		if !strings.Contains(errors[0], "must be one of triage/ship") {
			t.Fatalf("mode %#v => %v, want the configured set quoted", bad, errors)
		}
		if len(warnings) != 0 {
			t.Fatalf("mode %#v warned as well as failed: %v", bad, warnings)
		}
	}

	// Everything else about the marker is still validated in full.
	broken := map[string]any{"kind": "agent", "mode": "research", "status": "delegated", "at": "2026-07-20T11:00:00Z"}
	if errors, _ := DelegationStoredErrors(broken, vocabulary); len(errors) != 1 ||
		!strings.Contains(errors[0], "must be ready or claimed") {
		t.Fatalf("errors = %v", errors)
	}

	// A WRITE is still refused: the leniency is about records already on disk.
	if got := DelegationErrorsWith(marker("research"), vocabulary); len(got) != 1 ||
		got[0] != `delegation.mode "research" must be one of triage/ship` {
		t.Fatalf("write path = %v", got)
	}

	// And refused on a HUMAN delegation too. A mode is optional there, which is
	// exactly why it needs its own assertion: an implementation that skipped the
	// membership check whenever the mode was optional would pass every
	// agent-marker test in the suite.
	human := map[string]any{
		"kind": "human", "mode": "research", "status": "delegated",
		"assignee": "pat@example.com", "at": "2026-07-20T11:00:00Z",
	}
	if got := DelegationErrorsWith(human, vocabulary); len(got) != 1 ||
		got[0] != `delegation.mode "research" must be one of triage/ship` {
		t.Fatalf("human write path = %v", got)
	}
	delete(human, "mode")
	if got := DelegationErrorsWith(human, vocabulary); len(got) != 0 {
		t.Fatalf("a human delegation without a mode was refused: %v", got)
	}
}

// A mode may not be a word a surface already spends on an action: `release` is
// a delegation verb and `off`/`none`/`clear` clear a work reference or a note,
// so a mode spelled like one of them would make an instruction and a mode
// indistinguishable wherever both are written in one place. The collision is
// reported when the config is READ, not when the verb is needed.
func TestParseModeListRefusesReservedWords(t *testing.T) {
	for _, reserved := range ReservedModeNames {
		got, problem := ParseModeList("triage, " + reserved)
		if problem == "" {
			t.Fatalf("%q was accepted as a mode: %#v", reserved, got)
		}
		if !strings.Contains(problem, "is reserved") || !strings.Contains(problem, reserved) {
			t.Fatalf("%q => %q", reserved, problem)
		}
		if got != nil {
			t.Fatalf("%q returned a partial list %#v", reserved, got)
		}
	}
	// Nothing else is reserved: `release_notes` is a fine mode.
	if _, problem := ParseModeList("release_notes, offboard, cleared"); problem != "" {
		t.Fatalf("a word merely CONTAINING a reserved one was refused: %s", problem)
	}
}
