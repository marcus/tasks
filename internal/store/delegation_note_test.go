package store

import (
	"strings"
	"testing"
)

// The note is the receiver's briefing: it is written after the delegation, it
// emits LAST so a marker without one keeps its old byte layout, and it survives
// a claim and a work-ref write.
func TestDelegationNoteIsWrittenLastAndSurvivesTheLifecycle(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if result := store.Delegate("1a2b3c02", "agent", "implement", "", "key"); result.Status != MutationOK {
		t.Fatalf("delegate status = %q, errors = %v", result.Status, result.Errors)
	}
	if result := store.SetDelegationNote("1a2b3c02", "Land it on a branch.", ""); result.Status != MutationOK {
		t.Fatalf("note status = %q, errors = %v", result.Status, result.Errors)
	}
	want := `"delegation":{"kind":"agent","mode":"implement","status":"ready","at":"2026-03-14T15:09:26Z","note":"Land it on a branch."}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Fatalf("note not written last:\n%s", got)
	}
	// Re-stating the same note is settled work, not an undo slot.
	if again := store.SetDelegationNote("1a2b3c02", "Land it on a branch.", ""); again.Status != MutationNoChange {
		t.Fatalf("status = %q, want no_change", again.Status)
	}
	// Re-delegating at a new mode is the same delegation, so the briefing stays.
	if result := store.Delegate("1a2b3c02", "agent", "research", "", "key2"); result.Status != MutationOK {
		t.Fatalf("re-delegate status = %q", result.Status)
	}
	if got := readStore(t, store); !containsText(got, `"note":"Land it on a branch."`) {
		t.Fatalf("note dropped by a same-kind re-delegation:\n%s", got)
	}
	if result := store.Claim("1a2b3c02", "worker-alpha", ""); result.Status != MutationOK {
		t.Fatalf("claim status = %q", result.Status)
	}
	if result := store.SetWorkRef("1a2b3c02", "https://example.invalid/pr/1", "worker-alpha", ""); result.Status != MutationOK {
		t.Fatalf("work ref status = %q, errors = %v", result.Status, result.Errors)
	}
	got := readStore(t, store)
	if !containsText(got, `"work_ref":"https://example.invalid/pr/1","note":"Land it on a branch."`) {
		t.Fatalf("note lost or misplaced through claim/work_ref:\n%s", got)
	}
	// Handing the task to a PERSON is a different delegation: reference and
	// briefing both belonged to the agent. (The live claim is revoked first;
	// delegation over a claim is a conflict by design.)
	if result := store.Undelegate("1a2b3c02", ""); result.Status != MutationOK {
		t.Fatalf("undelegate status = %q", result.Status)
	}
	if result := store.Delegate("1a2b3c02", "agent", "implement", "", "key3"); result.Status != MutationOK {
		t.Fatalf("re-delegate status = %q", result.Status)
	}
	if result := store.SetDelegationNote("1a2b3c02", "Land it on a branch.", ""); result.Status != MutationOK {
		t.Fatalf("note status = %q", result.Status)
	}
	if result := store.Delegate("1a2b3c02", "human", "", "pat@example.com", "key4"); result.Status != MutationOK {
		t.Fatalf("human delegate status = %q, errors = %v", result.Status, result.Errors)
	}
	if got := readStore(t, store); containsText(got, `"note":"Land it on a branch."`) {
		t.Fatalf("note survived a kind change:\n%s", got)
	}
	// A person gets a briefing too, and clearing it removes the key entirely.
	if result := store.SetDelegationNote("1a2b3c02", "Chase the landlord by Friday.", ""); result.Status != MutationOK {
		t.Fatalf("human note status = %q, errors = %v", result.Status, result.Errors)
	}
	if result := store.SetDelegationNote("1a2b3c02", "", ""); result.Status != MutationOK {
		t.Fatalf("clear status = %q", result.Status)
	}
	if got := readStore(t, store); containsText(got, `"note"`) {
		t.Fatalf("cleared note left a key behind:\n%s", got)
	}
}

func TestDelegationNoteRefusals(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if result := store.SetDelegationNote("1a2b3c02", "brief", ""); result.Status != MutationInvalid ||
		result.Errors[0] != "task is not delegated" {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if result := store.Delegate("1a2b3c02", "agent", "implement", "", "key"); result.Status != MutationOK {
		t.Fatalf("delegate status = %q", result.Status)
	}
	long := store.SetDelegationNote("1a2b3c02", strings.Repeat("x", 2001), "")
	if long.Status != MutationInvalid ||
		long.Errors[0] != "delegation.note must be at most 2000 characters (got 2001)" {
		t.Fatalf("status = %q, errors = %v, want the bound quoted", long.Status, long.Errors)
	}
}

// Who holds the work and what kind of delegation it is are orthogonal, so a
// person may be handed a task in a mode. The mode is still a member check, and
// the whole thing goes through the ONE vocabulary seam.
func TestHumanDelegationMayCarryAMode(t *testing.T) {
	store, _ := writerFixture(t, fixtureStore)
	if result := store.Delegate("1a2b3c02", "human", "refine", "pat@example.com", "key"); result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	want := `"delegation":{"kind":"human","mode":"refine","status":"delegated","assignee":"pat@example.com","at":"2026-03-14T15:09:26Z"}`
	if got := readStore(t, store); !containsText(got, want) {
		t.Fatalf("marker = \n%s\nwant %s", got, want)
	}
	bad := store.Delegate("1a2b3c02", "human", "vibes", "pat@example.com", "key2")
	if bad.Status != MutationInvalid || bad.Errors[0] != `mode "vibes" must be one of refine/research/implement` {
		t.Fatalf("status = %q, errors = %v", bad.Status, bad.Errors)
	}
	if got := DelegationModes().Modes(); strings.Join(got, "/") != "refine/research/implement" {
		t.Fatalf("modes = %v, want the built-in vocabulary", got)
	}
}
