package merge

import (
	"reflect"
	"strings"
	"testing"
)

// The note and the mode are MEMBERS of the marker, not fields of their own, so
// they inherit the atomic rule: the merged record takes one side's whole
// object. A note is never spliced onto the other side's marker — a briefing
// that belonged to a delegation the merge discarded would brief the wrong work.
func TestDelegationNoteTravelsWithTheWholeMarker(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	later := ready(map[string]any{"at": "2026-07-27T19:00:00Z", "note": "land it on a branch"})
	earlier := ready(map[string]any{"at": "2026-07-27T18:30:00Z", "mode": "refine", "note": "just tighten the wording"})
	ours := base.change("10000002", map[string]any{"delegation": earlier, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": later, "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q / %q", forward.Error, reverse.Error)
	}
	if got := delegationOf(parseDoc(t, forward.Text), "10000002"); !reflect.DeepEqual(got, anyMap(later)) {
		t.Fatalf("delegation = %#v, want the later owner intent whole", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if forward.Events[0].Delegation.Reason != ReasonLaterIntent {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
}

// Two notes stated at the SAME instant still resolve deterministically, because
// the last tiebreak is canonical bytes over the whole marker.
func TestSimultaneousNotesTiebreakOnCanonicalBytes(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	left := ready(map[string]any{"at": "2026-07-27T19:00:00Z", "note": "aaa"})
	right := ready(map[string]any{"at": "2026-07-27T19:00:00Z", "note": "bbb"})
	ours := base.change("10000002", map[string]any{"delegation": right, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": left, "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q / %q", forward.Error, reverse.Error)
	}
	if got := delegationOf(parseDoc(t, forward.Text), "10000002"); !reflect.DeepEqual(got, anyMap(left)) {
		t.Fatalf("delegation = %#v, want the smaller canonical bytes", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
}

// A claim outranks a fresher note, because the note is owner intent and the
// claim is a fact about a worker holding the task — and undelegate still wins
// over both.
func TestClaimOutranksANoteAndRemovalBeatsBoth(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	held := claim("worker/zzz", "2026-07-27T18:04:11Z", map[string]any{"note": "old briefing"})
	briefed := ready(map[string]any{"at": "2026-07-27T23:00:00Z", "note": "new briefing"})
	ours := base.change("10000002", map[string]any{"delegation": briefed, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": held, "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	if got := delegationOf(merged, "10000002"); !reflect.DeepEqual(got, anyMap(held)) {
		t.Fatalf("delegation = %#v, want the claim to hold", got)
	}
	if result.Events[0].Delegation.Reason != ReasonClaimHolds {
		t.Fatalf("reason = %v", result.Events[0].Delegation.Reason)
	}

	// Same pair, but our side revoked: removal absorbs the note and the claim.
	revoked := base.change("10000002", map[string]any{"updated": homeStamp, "delegation": nil})
	mergedAway, awayResult := mergeDocs(t, base, revoked, theirs)
	if got := delegationOf(mergedAway, "10000002"); got != nil {
		t.Fatalf("delegation = %#v, want undelegate to win", got)
	}
	if awayResult.Events[0].Delegation.Reason != ReasonRemovalWins {
		t.Fatalf("reason = %v", awayResult.Events[0].Delegation.Reason)
	}
}

// The scenario that made stamping a note write necessary: one device CLEARS a
// stale briefing, another edits it earlier. Because a note write restamps `at`,
// the clear is the later intent and wins. If notes shared the delegation's
// original stamp, both sides would tie on `at`, the canonical-byte tiebreak
// would decide, and an agent could read a retracted instruction as live.
func TestALaterNoteClearBeatsAnEarlierNoteEdit(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"delegation": ready(map[string]any{"note": "original briefing"})})
	cleared := ready(map[string]any{"at": "2026-07-27T20:00:00Z"})
	edited := ready(map[string]any{"at": "2026-07-27T19:00:00Z", "note": "tweaked wording"})
	ours := base.change("10000002", map[string]any{"delegation": cleared, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": edited, "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q / %q", forward.Error, reverse.Error)
	}
	if got := delegationOf(parseDoc(t, forward.Text), "10000002"); !reflect.DeepEqual(got, anyMap(cleared)) {
		t.Fatalf("delegation = %#v, want the later clear to hold", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
}

// A mode on a HUMAN delegation is merged the same way as any other member: it
// cannot be combined with the other side's assignee.
func TestHumanDelegationWithAModeMergesAtomically(t *testing.T) {
	human := func(assignee, mode, at string) map[string]any {
		return map[string]any{"kind": "human", "status": "delegated",
			"mode": mode, "assignee": assignee, "at": at}
	}
	base := baseRecords().change("10000002", map[string]any{
		"delegation": human("pat@example.com", "refine", "2026-07-27T18:00:00Z")})
	ours := base.change("10000002", map[string]any{
		"delegation": human("pat@example.com", "research", "2026-07-27T18:30:00Z"), "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{
		"delegation": human("sam@example.com", "refine", "2026-07-27T19:00:00Z"), "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	want := human("sam@example.com", "refine", "2026-07-27T19:00:00Z")
	if got := delegationOf(merged, "10000002"); !reflect.DeepEqual(got, anyMap(want)) {
		t.Fatalf("delegation = %#v, want the later intent whole (no spliced mode)", got)
	}
	if !containsString(result.Events[0].Conflicts, "delegation") {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
	if log := strings.Join(result.LogLines(""), "\n"); !strings.Contains(log, "delegation=later_intent") {
		t.Fatalf("log = %s", log)
	}
}
