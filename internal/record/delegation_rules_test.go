package record

import (
	"reflect"
	"strings"
	"testing"
)

// The schema half of test/test_delegation.rb: what the module accepts, what it
// names, and the identity hygiene it enforces. The lifecycle half (claim races,
// release, revocation, undo) is the write wave's.
//
// These rules are enforced at three points — the emitter, the linter, and the
// store — from this one definition, so a gap here is a gap in all three.

const (
	delegationStamp  = "2026-07-27T18:04:11Z"
	delegationWorker = "cc/fable5/aaaa1111"
)

func agentDelegation(overrides map[string]any) map[string]any {
	value := map[string]any{"kind": "agent", "mode": "research", "status": "ready", "at": delegationStamp}
	for key, override := range overrides {
		if override == nil {
			delete(value, key)
			continue
		}
		value[key] = override
	}
	return value
}

// test_delegation_module_accepts_valid_objects_and_names_every_violation
func TestDelegationAcceptsValidObjectsAndNamesEveryViolation(t *testing.T) {
	valid := []map[string]any{
		{"kind": "human", "status": "delegated", "assignee": "pat@example.com", "at": delegationStamp},
		{"kind": "agent", "mode": "research", "status": "ready", "at": delegationStamp},
		{"kind": "agent", "mode": "implement", "status": "claimed", "assignee": delegationWorker,
			"at": delegationStamp, "work_ref": "https://example.com/pr/42"},
	}
	for _, value := range valid {
		if !DelegationValid(value) {
			t.Fatalf("%v rejected: %v", value, DelegationErrors(value))
		}
	}

	if got := DelegationErrors("nope"); !reflect.DeepEqual(got, []string{"delegation must be an object"}) {
		t.Fatalf("errors = %#v", got)
	}
	if got := DelegationErrors(map[string]any{}); !reflect.DeepEqual(got, []string{"delegation must not be empty"}) {
		t.Fatalf("errors = %#v", got)
	}

	for _, tc := range []struct {
		want  string
		value map[string]any
	}{
		{"assignee is not allowed while ready", agentDelegation(map[string]any{"assignee": delegationWorker})},
		{"must be a worker id", agentDelegation(map[string]any{"status": "claimed"})},
		{"must be ready or claimed", agentDelegation(map[string]any{"status": "delegated"})},
		{"is not a UTC timestamp", agentDelegation(map[string]any{"at": "2026-07-27 18:04:11"})},
		// Shape, then reality: an impossible day would otherwise roll over.
		{"is not a UTC timestamp", agentDelegation(map[string]any{"at": "2026-02-31T00:00:00Z"})},
		{"work_ref must be a non-empty string", agentDelegation(map[string]any{"work_ref": ""})},
	} {
		if DelegationValid(tc.value) {
			t.Fatalf("%v was accepted", tc.value)
		}
		if got := strings.Join(DelegationErrors(tc.value), " | "); !strings.Contains(got, tc.want) {
			t.Fatalf("errors = %q, want one containing %q", got, tc.want)
		}
	}
}

// A bad kind stops the cascade rather than inventing three follow-on errors
// about fields whose rules are unknown — kind is what decides them.
func TestABadKindStopsTheCascade(t *testing.T) {
	value := map[string]any{"kind": "team", "status": "ready", "at": delegationStamp}
	got := DelegationErrors(value)
	if len(got) != 1 || !strings.Contains(got[0], `kind "team" must be human or agent`) {
		t.Fatalf("errors = %#v, want only the kind diagnostic", got)
	}
	// A bad `at` is still reported, because it does not depend on the kind.
	both := map[string]any{"kind": "team", "status": "ready", "at": "nope"}
	if len(DelegationErrors(both)) != 2 {
		t.Fatalf("errors = %#v, want the kind and the timestamp", DelegationErrors(both))
	}
}

// test_control_characters_and_escapes_are_refused_in_every_identity_field.
// Control bytes are not cosmetic: a worker id carrying an erase-line/cursor-up
// sequence rewrites the terminal of the agent that LOST a claim race, because
// the conflict line names the holder verbatim.
func TestControlCharactersAndEscapesAreRefusedInEveryIdentityField(t *testing.T) {
	spoof := "\x1b[2K\x1b[1A\x1b[2K\x1b[Gagent-ready (research): Renew office lease"
	hostile := []string{spoof, "w\x00x", "w\x7fx", "w\ax", "w\bx", "w\u009bx"}
	for _, value := range hostile {
		if DelegationIdentifier(value) {
			t.Fatalf("worker id %q accepted", value)
		}
		if DelegationEmail("pat" + value + "@example.com") {
			t.Fatalf("email %q accepted", value)
		}
		errors := DelegationErrors(agentDelegation(map[string]any{"work_ref": "https://example.com/" + value}))
		if got := strings.Join(errors, " | "); !strings.Contains(got, "must not contain control characters") {
			t.Fatalf("work_ref %q: errors = %q", value, got)
		}
	}

	// Ruby's \s is ASCII-only, so these four used to pass a "no whitespace"
	// rule while still reading as a break on screen. The Unicode class covers
	// them, and so must this port.
	for _, value := range []string{"w\u00a0x", "w\u2028x", "w\u2029x", "w\u3000x"} {
		if DelegationIdentifier(value) {
			t.Fatalf("worker id %q accepted", value)
		}
		if DelegationEmail("pat" + value + "@example.com") {
			t.Fatalf("email %q accepted", value)
		}
	}

	if !DelegationIdentifier(delegationWorker) || !DelegationEmail("pat@example.com") {
		t.Fatal("the ordinary identifiers must still pass")
	}
}

// The bound applies to both identity fields: an identifier, not a mail
// integration.
func TestIdentifierIsBoundedAndNonEmpty(t *testing.T) {
	if DelegationIdentifier("") || DelegationIdentifier(nil) || DelegationIdentifier(42) {
		t.Fatal("an empty, absent, or non-string identifier was accepted")
	}
	if !DelegationIdentifier(strings.Repeat("w", 200)) {
		t.Fatal("200 characters is the limit, not one past it")
	}
	if DelegationIdentifier(strings.Repeat("w", 201)) {
		t.Fatal("201 characters was accepted")
	}
	// The bound counts characters, not bytes, so a multi-byte id is not
	// penalized for its encoding.
	if !DelegationIdentifier(strings.Repeat("ü", 200)) {
		t.Fatal("200 multi-byte characters were counted as more than 200")
	}
}

// test_an_at_prefixed_word_is_not_an_email_address. `@` is the TUI's context
// filter, so `@work` is muscle memory — and it used to become a person the task
// was now WAITING on.
func TestAnAtPrefixedWordIsNotAnEmailAddress(t *testing.T) {
	for _, value := range []string{
		"@", "@work", "@home", "pat@", "pat@example", "pat@.com", "pat@example.",
		"pat@@example.com", "pat@ex..com", "", "pat",
	} {
		if DelegationEmail(value) {
			t.Fatalf("%q accepted as an email address", value)
		}
	}
	for _, value := range []string{"pat@example.com", "p.a+t@mail.example.co.uk", "x@y.z"} {
		if !DelegationEmail(value) {
			t.Fatalf("%q rejected", value)
		}
	}
}

// test_work_ref_is_bounded_so_one_paste_cannot_own_a_jsonl_line. The reference
// shares a JSONL line with the whole record, so a pathological paste is a
// storage problem, not just an ugly one.
func TestWorkRefIsBounded(t *testing.T) {
	atLimit := "https://example.com/" + strings.Repeat("x", 500-20)
	if len(atLimit) != 500 {
		t.Fatalf("fixture is %d characters, want exactly the limit", len(atLimit))
	}
	if got := DelegationErrors(agentDelegation(map[string]any{"work_ref": atLimit})); len(got) != 0 {
		t.Fatalf("errors = %#v, want the limit itself to be legal", got)
	}
	over := strings.Repeat("x", 501)
	got := strings.Join(DelegationErrors(agentDelegation(map[string]any{"work_ref": over})), " | ")
	if !strings.Contains(got, "at most 500 characters (got 501)") {
		t.Fatalf("errors = %q", got)
	}
	// Only one work_ref diagnostic is emitted per value: the first rule it
	// breaks, so a caller fixes one thing at a time.
	multiple := DelegationErrors(agentDelegation(map[string]any{"work_ref": "a\nb\x1bc"}))
	if len(multiple) != 1 || !strings.Contains(multiple[0], "single line") {
		t.Fatalf("errors = %#v, want just the first violation", multiple)
	}
}

// test_an_unknown_nested_key_is_not_an_error_and_survives_a_write. Forward
// compatibility must not invert one level down: an unknown nested key is
// reported as a hazard, never as a validation failure.
func TestUnknownNestedKeysAreNotErrors(t *testing.T) {
	future := agentDelegation(map[string]any{"lease_until": "2026-07-28T00:00:00Z"})
	if got := DelegationErrors(future); len(got) != 0 {
		t.Fatalf("errors = %#v, want a newer binary's field to validate", got)
	}
	if got := DelegationUnknownKeys(future); !reflect.DeepEqual(got, []string{"lease_until"}) {
		t.Fatalf("unknown keys = %#v", got)
	}
	// The set is sorted, so a caller comparing two records' unknown keys is not
	// comparing map iteration order.
	several := agentDelegation(map[string]any{"hint": "x", "lease": 1, "budget_tokens": 5})
	want := []string{"budget_tokens", "hint", "lease"}
	if got := DelegationUnknownKeys(several); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown keys = %#v, want %#v", got, want)
	}
	if got := DelegationUnknownKeys("not an object"); len(got) != 0 {
		t.Fatalf("unknown keys = %#v, want none for a non-object", got)
	}
}

// The emission order: declared keys first with absent entries dropped, then the
// unknown tail in the record's own order. Keeping the tail is what lets a claim
// rewrite a record written by a newer binary without deleting its new field.
func TestDelegationOrderedKeysKeepTheUnknownTail(t *testing.T) {
	value := map[string]any{
		"work_ref": "https://example.com/pr/42", "at": delegationStamp,
		"assignee": delegationWorker, "status": "claimed", "mode": "implement",
		"kind": "agent", "lease_until": "2026-07-28T00:00:00Z", "note": "",
	}
	source := []string{"work_ref", "at", "assignee", "status", "mode", "kind", "lease_until", "note"}
	want := []string{"kind", "mode", "status", "assignee", "at", "work_ref", "lease_until"}
	if got := DelegationOrderedKeys(value, source); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v — declared order, then the unknown tail, absent dropped", got, want)
	}
}

// timestamp? is shape THEN reality, because a permissive parser rolls an
// impossible day over rather than refusing it — and `at` is spelled like the
// timestamp half of an update stamp so the two can never drift apart.
func TestDelegationTimestampRequiresARealUTCInstant(t *testing.T) {
	for _, value := range []string{delegationStamp, "2024-02-29T00:00:00Z", "1970-01-01T00:00:00Z"} {
		if !DelegationTimestamp(value) {
			t.Fatalf("%q rejected", value)
		}
	}
	for _, value := range []any{
		"2026-02-31T00:00:00Z", "2026-13-01T00:00:00Z", "2026-07-27T18:04:11+02:00",
		"2026-07-27 18:04:11", "2026-07-27T18:04:11", "2026-07-27T18:04:11.5Z",
		"", nil, 42,
	} {
		if DelegationTimestamp(value) {
			t.Fatalf("%v accepted", value)
		}
	}
}

// The lifecycle predicates the write path asks by name. Each one is narrow on
// purpose: "is this delegated at all" and "is it claimed" are different
// questions and a store that confused them would hand one task to two workers.
func TestDelegationPredicatesAreNarrow(t *testing.T) {
	ready := agentDelegation(nil)
	claimed := agentDelegation(map[string]any{"status": "claimed", "assignee": delegationWorker})
	human := map[string]any{"kind": "human", "status": "delegated", "assignee": "pat@example.com", "at": delegationStamp}

	for _, tc := range []struct {
		name string
		got  bool
		want bool
	}{
		{"empty object is not an object", DelegationObject(map[string]any{}), false},
		{"nil is not an object", DelegationObject(nil), false},
		{"ready is an agent", DelegationAgent(ready), true},
		{"human is not an agent", DelegationAgent(human), false},
		{"human is human", DelegationHuman(human), true},
		{"ready is ready", DelegationReady(ready), true},
		{"claimed is not ready", DelegationReady(claimed), false},
		{"claimed is claimed", DelegationClaimed(claimed), true},
		{"ready is not claimed", DelegationClaimed(ready), false},
		{"a human is never ready", DelegationReady(human), false},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
