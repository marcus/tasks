package merge

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

const (
	homeStamp = "2026-07-16T10:00:00Z#home"
	workStamp = "2026-07-16T11:00:00Z#work"
)

// doc is a whole file as an ordered list of records, the shape test_jsonl_merge
// builds its sides in. Everything reaches Merge as canonical bytes, so a test
// can never assert against a spelling the store could not hold.
type doc []map[string]any

func baseRecords() doc {
	return doc{
		{"type": "meta", "version": 2},
		{"type": "section", "id": "10000001", "title": "Work"},
		{"type": "task", "id": "10000002", "parent": "10000001", "state": "NEXT",
			"title": "Book Sixt car", "tags": []any{"@computer"}, "scheduled": "2026-07-18",
			"body": "Reservation started."},
		{"type": "task", "id": "10000003", "parent": "10000001", "state": "TODO",
			"title": "Call PSE"},
		{"type": "task", "id": "10000004", "parent": "10000001", "state": "TODO",
			"title": "Review Stash"},
	}
}

func (d doc) copy() doc {
	copied := make(doc, 0, len(d))
	for _, source := range d {
		record := map[string]any{}
		for key, value := range source {
			record[key] = value
		}
		copied = append(copied, record)
	}
	return copied
}

// change is the Ruby helper of the same name: copy the file, then set or (on a
// nil value) delete fields on one record.
func (d doc) change(id string, fields map[string]any) doc {
	changed := d.copy()
	target := changed.find(id)
	if target == nil {
		panic("no record " + id)
	}
	for key, value := range fields {
		if value == nil {
			delete(target, key)
			continue
		}
		target[key] = value
	}
	return changed
}

func (d doc) find(id string) map[string]any {
	for _, candidate := range d {
		if candidate["id"] == id {
			return candidate
		}
	}
	return nil
}

func (d doc) without(ids ...string) doc {
	dropped := map[string]bool{}
	for _, id := range ids {
		dropped[id] = true
	}
	kept := make(doc, 0, len(d))
	for _, candidate := range d.copy() {
		if id, ok := candidate["id"].(string); ok && dropped[id] {
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

// text renders the file the way the store would write it: through the canonical
// emitter, so field order and omission are the store's and not the test's.
func (d doc) text(t *testing.T) string {
	t.Helper()
	var raw strings.Builder
	for _, candidate := range d {
		line, err := json.Marshal(candidate)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		raw.Write(line)
		raw.WriteByte('\n')
	}
	parsed := record.Parse([]byte(raw.String()))
	if !parsed.OK() {
		t.Fatalf("fixture does not parse: %v", parsed.Errors)
	}
	canonical, err := record.Dump(parsed.Records)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	return canonical
}

func mergeDocs(t *testing.T, base, ours, theirs doc) (doc, Result) {
	t.Helper()
	result := Merge(base.text(t), ours.text(t), theirs.text(t))
	if !result.OK() {
		t.Fatalf("merge failed: %s", result.Error)
	}
	return parseDoc(t, result.Text), result
}

func parseDoc(t *testing.T, text string) doc {
	t.Helper()
	parsed := doc{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("merged output line is not JSON: %q", line)
		}
		parsed = append(parsed, value)
	}
	return parsed
}

func (d doc) indexOf(id string) int {
	for position, candidate := range d {
		if candidate["id"] == id {
			return position
		}
	}
	return -1
}

func decisions(result Result) []string {
	names := make([]string, 0, len(result.Events))
	for _, event := range result.Events {
		names = append(names, event.Decision)
	}
	return names
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func checkOK(text string) bool { return check.CheckText([]byte(text)).OK() }

func assertValid(t *testing.T, text string) {
	t.Helper()
	if result := check.CheckText([]byte(text)); !result.OK() {
		t.Fatalf("merged output does not validate: %v", result.Errors)
	}
}

// -- field-level resolution ---------------------------------------------------

func TestNonOverlappingFieldsMergeWithoutConflict(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"tags": []any{"@computer", "travel"}, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"scheduled": "2026-07-19", "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if got := task["tags"]; !reflect.DeepEqual(got, []any{"@computer", "travel"}) {
		t.Fatalf("tags = %#v", got)
	}
	if task["scheduled"] != "2026-07-19" {
		t.Fatalf("scheduled = %v", task["scheduled"])
	}
	if task["updated"] != workStamp {
		t.Fatalf("updated = %v", task["updated"])
	}
	if len(result.Events[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
}

func TestSameFieldUsesNewerUpdatedAndIsCommutative(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000003", map[string]any{"title": "Call utility", "updated": homeStamp})
	theirs := base.change("10000003", map[string]any{"title": "Call PSE billing", "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q / %q", forward.Error, reverse.Error)
	}
	if got := parseDoc(t, forward.Text).find("10000003")["title"]; got != "Call PSE billing" {
		t.Fatalf("title = %v", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("swapping ours/theirs changed the bytes:\n%s\n%s", forward.Text, reverse.Text)
	}
}

func TestPreTimestampConflictIsOursWinsAndLoggedLowConfidence(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000003", map[string]any{"title": "Ours title"})
	theirs := base.change("10000003", map[string]any{"title": "Theirs title"})

	merged, result := mergeDocs(t, base, ours, theirs)

	if got := merged.find("10000003")["title"]; got != "Ours title" {
		t.Fatalf("title = %v", got)
	}
	if !containsString(result.Events[0].LowConfidence, "title") {
		t.Fatalf("low confidence = %v", result.Events[0].LowConfidence)
	}
	if log := strings.Join(result.LogLines(""), "\n"); !strings.Contains(log, "low-confidence=title") {
		t.Fatalf("log = %s", log)
	}
}

func TestTemporalPairConflictTakesTheWholePairFromTheLWWWinner(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"scheduled":      "2026-07-20",
		"scheduled_time": map[string]any{"local": "09:00", "timezone": "America/Los_Angeles"}})
	ours := base.change("10000002", map[string]any{"scheduled": "2026-07-21", "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{
		"scheduled": "2026-07-25", "scheduled_time": map[string]any{"local": "14:00"}, "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if task["scheduled"] != "2026-07-25" {
		t.Fatalf("scheduled = %v", task["scheduled"])
	}
	if got := task["scheduled_time"]; !reflect.DeepEqual(got, map[string]any{"local": "14:00"}) {
		t.Fatalf("scheduled_time = %#v", got)
	}
	if !containsString(result.Events[0].Conflicts, "scheduled") {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative:\n%s\n%s", forward.Text, reverse.Text)
	}
}

func TestTemporalPairSingleSideChangeWinsWithoutConflict(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"scheduled": "2026-07-20", "scheduled_time": map[string]any{"local": "09:00"}})
	ours := base.change("10000002", map[string]any{"tags": []any{"@computer", "travel"}, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{
		"scheduled":      "2026-07-22",
		"scheduled_time": map[string]any{"local": "17:00", "timezone": "Europe/London"},
		"updated":        workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if task["scheduled"] != "2026-07-22" {
		t.Fatalf("scheduled = %v", task["scheduled"])
	}
	if got := task["scheduled_time"]; !reflect.DeepEqual(got, map[string]any{"local": "17:00", "timezone": "Europe/London"}) {
		t.Fatalf("scheduled_time = %#v", got)
	}
	if got := task["tags"]; !reflect.DeepEqual(got, []any{"@computer", "travel"}) {
		t.Fatalf("tags = %#v", got)
	}
	if len(result.Events[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
}

func TestUndateVsRetimeNeverEmitsOrphanTimeMetadata(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"scheduled":      "2026-07-20",
		"scheduled_time": map[string]any{"local": "09:00", "timezone": "Europe/London"}})
	ours := base.change("10000002", map[string]any{"scheduled": nil, "scheduled_time": nil, "updated": workStamp})
	theirs := base.change("10000002", map[string]any{
		"scheduled_time": map[string]any{"local": "10:30", "timezone": "Europe/London"}, "updated": homeStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if _, present := task["scheduled"]; present {
		t.Fatal("ours undate should win the whole pair")
	}
	if _, present := task["scheduled_time"]; present {
		t.Fatal("orphan time metadata must never survive")
	}
	if !containsString(result.Events[0].Conflicts, "scheduled") {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
}

func TestTagsUnionPreservesBaseOrderAndSortsConcurrentAdditions(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"tags": []any{"@computer", "important"}})
	ours := base.change("10000002", map[string]any{"tags": []any{"@computer", "important", "zeta"}, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"tags": []any{"@computer", "important", "alpha"}, "updated": workStamp})

	merged, _ := mergeDocs(t, base, ours, theirs)

	if got := merged.find("10000002")["tags"]; !reflect.DeepEqual(got, []any{"@computer", "important", "alpha", "zeta"}) {
		t.Fatalf("tags = %#v", got)
	}
}

func TestProgressedStateBeatsOpenStateAndCarriesClosedDate(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"state": "DONE", "closed": "2026-07-16", "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"state": "TODO", "updated": workStamp})

	merged, _ := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if task["state"] != "DONE" {
		t.Fatalf("state = %v", task["state"])
	}
	if task["closed"] != "2026-07-16" {
		t.Fatalf("closed = %v", task["closed"])
	}
}

func TestBodyPrefixChoosesLongerAppend(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"body": "Reservation started.\nConfirmation 1", "updated": homeStamp})
	theirs := ours.change("10000002", map[string]any{
		"body": "Reservation started.\nConfirmation 1\nConfirmation 2", "updated": workStamp})

	merged, _ := mergeDocs(t, base, ours, theirs)

	if got := merged.find("10000002")["body"]; got != "Reservation started.\nConfirmation 1\nConfirmation 2" {
		t.Fatalf("body = %q", got)
	}
}

// -- delegation ---------------------------------------------------------------

func ready(fields map[string]any) map[string]any {
	marker := map[string]any{"kind": "agent", "mode": "research", "status": "ready", "at": "2026-07-27T18:00:00Z"}
	for key, value := range fields {
		marker[key] = value
	}
	return marker
}

func claim(assignee, at string, fields map[string]any) map[string]any {
	marker := map[string]any{"kind": "agent", "mode": "research", "status": "claimed",
		"assignee": assignee, "at": at}
	for key, value := range fields {
		marker[key] = value
	}
	return marker
}

func delegationOf(d doc, id string) any {
	record := d.find(id)
	if record == nil {
		return nil
	}
	return record["delegation"]
}

func TestOneSidedDelegationChangeWinsWithoutConflict(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"tags": []any{"@computer", "travel"}, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": ready(nil), "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)

	if got := delegationOf(merged, "10000002"); !reflect.DeepEqual(got, anyMap(ready(nil))) {
		t.Fatalf("delegation = %#v", got)
	}
	if len(result.Events[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %v", result.Events[0].Conflicts)
	}
}

// anyMap round-trips a fixture marker through JSON so a comparison against a
// parsed merge result compares like with like.
func anyMap(value map[string]any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		panic(err)
	}
	return decoded
}

func TestConcurrentClaimsResolveToTheEarlierAtSymmetrically(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	early := claim("worker/zzz", "2026-07-27T18:04:11Z", nil)
	late := claim("worker/aaa", "2026-07-27T18:09:00Z", nil)
	ours := base.change("10000002", map[string]any{"delegation": late, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": early, "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q / %q", forward.Error, reverse.Error)
	}
	if got := delegationOf(parseDoc(t, forward.Text), "10000002"); !reflect.DeepEqual(got, anyMap(early)) {
		t.Fatalf("first claim must hold the task, got %#v", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if !containsString(forward.Events[0].Conflicts, "delegation") {
		t.Fatalf("conflicts = %v", forward.Events[0].Conflicts)
	}
	if forward.Events[0].Delegation.Reason != ReasonEarlierClaim {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
	if log := strings.Join(forward.LogLines(""), "\n"); !strings.Contains(log, "delegation=earlier_claim holder=worker/zzz") {
		t.Fatalf("log = %s", log)
	}
}

func TestSimultaneousClaimsTiebreakOnTheSmallerAssignee(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	at := "2026-07-27T18:04:11Z"
	ours := base.change("10000002", map[string]any{"delegation": claim("worker/bbb", at, nil), "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": claim("worker/aaa", at, nil), "updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed")
	}
	marker, _ := delegationOf(parseDoc(t, forward.Text), "10000002").(map[string]any)
	if marker["assignee"] != "worker/aaa" {
		t.Fatalf("assignee = %v", marker["assignee"])
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
}

func TestDelegationIsTakenWholeNeverFieldByField(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	oursMarker := claim("worker/zzz", "2026-07-27T18:04:11Z",
		map[string]any{"mode": "implement", "work_ref": "https://example.com/ours"})
	theirsMarker := claim("worker/aaa", "2026-07-27T18:20:00Z",
		map[string]any{"mode": "refine", "work_ref": "https://example.com/theirs"})
	ours := base.change("10000002", map[string]any{"delegation": oursMarker, "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": theirsMarker, "updated": workStamp})

	merged, _ := mergeDocs(t, base, ours, theirs)

	if got := delegationOf(merged, "10000002"); !reflect.DeepEqual(got, anyMap(oursMarker)) {
		t.Fatalf("the winning side supplies every field of the object, got %#v", got)
	}
}

func TestOwnerUndelegateBeatsAConcurrentClaimInEitherDirection(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	revoked := base.change("10000002", map[string]any{"delegation": nil, "updated": homeStamp})
	claimed := base.change("10000002", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T18:04:11Z", nil), "updated": workStamp})

	forward := Merge(base.text(t), revoked.text(t), claimed.text(t))
	reverse := Merge(base.text(t), claimed.text(t), revoked.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed")
	}
	if _, present := parseDoc(t, forward.Text).find("10000002")["delegation"]; present {
		t.Fatal("revocation wins")
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if forward.Events[0].Delegation.Reason != ReasonRemovalWins {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
}

func TestOwnerUndelegateAgainstAnUnchangedSideSimplyRemovesIt(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	revoked := base.change("10000002", map[string]any{"delegation": nil, "updated": homeStamp})

	merged, result := mergeDocs(t, base, revoked, base)

	if _, present := merged.find("10000002")["delegation"]; present {
		t.Fatal("the removal must stand")
	}
	if containsString(result.Events[0].Conflicts, "delegation") {
		t.Fatalf("a one-sided removal is not a conflict: %v", result.Events[0].Conflicts)
	}
}

func TestNonClaimDelegationConflictsTakeTheMostRecentOwnerIntent(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(map[string]any{"mode": "refine"})})
	ours := base.change("10000002", map[string]any{
		"delegation": ready(map[string]any{"mode": "implement", "at": "2026-07-27T19:00:00Z"}),
		"updated":    homeStamp})
	theirs := base.change("10000002", map[string]any{
		"delegation": map[string]any{"kind": "human", "status": "delegated",
			"assignee": "pat@example.com", "at": "2026-07-27T20:00:00Z"},
		"updated": workStamp})

	forward := Merge(base.text(t), ours.text(t), theirs.text(t))
	reverse := Merge(base.text(t), theirs.text(t), ours.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed: %q %q", forward.Error, reverse.Error)
	}
	marker, _ := delegationOf(parseDoc(t, forward.Text), "10000002").(map[string]any)
	if marker["kind"] != "human" {
		t.Fatalf("the newer intent wins, got %#v", marker)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if forward.Events[0].Delegation.Reason != ReasonLaterIntent {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
}

func TestCloseAgainstClaimKeepsProvenanceButDropsAReadyMarker(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	claimed := claim("worker/aaa", "2026-07-27T18:04:11Z", map[string]any{"work_ref": "https://example.com/pr/42"})
	ours := base.change("10000002", map[string]any{"state": "DONE", "closed": "2026-07-27", "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"delegation": claimed, "updated": workStamp})

	merged, _ := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")
	if task["state"] != "DONE" {
		t.Fatalf("state = %v", task["state"])
	}
	if got := task["delegation"]; !reflect.DeepEqual(got, anyMap(claimed)) {
		t.Fatalf("who did it and where must survive the close, got %#v", got)
	}

	unclaimed, result := mergeDocs(t, base, ours, base)
	if _, present := unclaimed.find("10000002")["delegation"]; present {
		t.Fatal("a merely ready marker is dropped by the close")
	}
	if result.Events[0].Delegation.Reason != ClearedOnClose {
		t.Fatalf("reason = %#v", result.Events[0].Delegation)
	}
}

func TestDelegationAgainstAConcurrentProposalIsDroppedNotFatal(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": nil})
	ours := base.change("10000002", map[string]any{"delegation": ready(nil), "updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"state": "PROPOSED", "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)
	task := merged.find("10000002")

	if task["state"] != "PROPOSED" {
		t.Fatalf("state = %v", task["state"])
	}
	if _, present := task["delegation"]; present {
		t.Fatal("a proposal carries no delegation")
	}
	if result.Events[0].Delegation.Reason != ClearedOnProposal {
		t.Fatalf("reason = %#v", result.Events[0].Delegation)
	}
	assertValid(t, result.Text)
}

func TestMergedDelegationStillValidatesAndLandsInCanonicalOrder(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{"delegation": ready(nil)})
	ours := base.change("10000002", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T18:04:11Z",
			map[string]any{"work_ref": "https://example.com/pr/42"}),
		"updated": homeStamp})
	theirs := base.change("10000002", map[string]any{"title": "Book the Sixt car", "updated": workStamp})

	_, result := mergeDocs(t, base, ours, theirs)

	assertValid(t, result.Text)
	wanted := `"delegation":{"kind":"agent","mode":"research","status":"claimed",` +
		`"assignee":"worker/aaa","at":"2026-07-27T18:04:11Z",` +
		`"work_ref":"https://example.com/pr/42"}`
	if !strings.Contains(result.Text, wanted) {
		t.Fatalf("merged text does not carry the canonical marker:\n%s", result.Text)
	}
}

// The regression that motivated the single total order: with `at` deciding two
// claims but record-level last-write-wins deciding everything else, the holder
// depended on the order the devices happened to sync in.
func TestPairwiseMergeOrderCannotChangeTheClaimHolder(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"delegation": ready(map[string]any{"at": "2026-07-27T09:00:00Z"})})
	first := base.change("10000002", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T10:00:00Z", nil), "updated": "2026-07-27T10:00:00Z#a"})
	widened := base.change("10000002", map[string]any{
		"delegation": ready(map[string]any{"mode": "implement", "at": "2026-07-27T10:10:00Z"}),
		"updated":    "2026-07-27T10:10:00Z#c"})
	second := base.change("10000002", map[string]any{
		"delegation": claim("worker/bbb", "2026-07-27T10:30:00Z", nil), "updated": "2026-07-27T10:30:00Z#b"})

	holders := make([]string, 0, 3)
	for _, order := range [][]doc{{first, second, widened}, {first, widened, second}, {second, widened, first}} {
		pair := Merge(base.text(t), order[0].text(t), order[1].text(t))
		if !pair.OK() {
			t.Fatalf("pair merge failed: %s", pair.Error)
		}
		whole := Merge(base.text(t), pair.Text, order[2].text(t))
		if !whole.OK() {
			t.Fatalf("whole merge failed: %s", whole.Error)
		}
		encoded, _ := json.Marshal(delegationOf(parseDoc(t, whole.Text), "10000002"))
		holders = append(holders, string(encoded))
	}
	for _, holder := range holders {
		if holder != holders[0] {
			t.Fatalf("every sync order must converge on one holder: %v", holders)
		}
	}
	if !strings.Contains(holders[0], `"worker/aaa"`) {
		t.Fatalf("the first claim holds the task: %s", holders[0])
	}
}

func TestALiveClaimOutranksAConcurrentReleaseAndKeepsItsProvenance(t *testing.T) {
	held := claim("worker/aaa", "2026-07-27T10:00:00Z", map[string]any{"work_ref": "https://example.com/pr/42"})
	base := baseRecords().change("10000002", map[string]any{"delegation": held})
	closed := base.change("10000002", map[string]any{"state": "DONE", "closed": "2026-07-27", "updated": homeStamp})
	released := base.change("10000002", map[string]any{
		"delegation": ready(map[string]any{"at": "2026-07-27T10:02:00Z"}), "updated": workStamp})

	forward := Merge(base.text(t), closed.text(t), released.text(t))
	reverse := Merge(base.text(t), released.text(t), closed.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed")
	}
	task := parseDoc(t, forward.Text).find("10000002")
	if task["state"] != "DONE" {
		t.Fatalf("state = %v", task["state"])
	}
	if got := task["delegation"]; !reflect.DeepEqual(got, anyMap(held)) {
		t.Fatalf("the holder and work_ref must survive the close, got %#v", got)
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if forward.Events[0].Delegation.Reason != ReasonClaimHolds {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
}

func TestRemovalStillAbsorbsALiveClaimFromEitherSide(t *testing.T) {
	base := baseRecords().change("10000002", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T10:00:00Z", nil)})
	revoked := base.change("10000002", map[string]any{"delegation": nil, "updated": homeStamp})
	reclaimed := base.change("10000002", map[string]any{
		"delegation": claim("worker/bbb", "2026-07-27T09:00:00Z", nil), "updated": workStamp})

	forward := Merge(base.text(t), revoked.text(t), reclaimed.text(t))
	reverse := Merge(base.text(t), reclaimed.text(t), revoked.text(t))
	if !forward.OK() || !reverse.OK() {
		t.Fatalf("merge failed")
	}
	if _, present := parseDoc(t, forward.Text).find("10000002")["delegation"]; present {
		t.Fatal("undelegate always wins")
	}
	if forward.Text != reverse.Text {
		t.Fatalf("not commutative")
	}
	if forward.Events[0].Delegation.Reason != ReasonRemovalWins {
		t.Fatalf("reason = %v", forward.Events[0].Delegation.Reason)
	}
}

func TestDelegationOnARecordThatMergedIntoASectionIsDroppedNotFatal(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000003", map[string]any{
		"delegation": claim("worker/aaa", "2026-07-27T18:04:11Z", nil), "updated": homeStamp})
	theirs := base.copy()
	for position, candidate := range theirs {
		if candidate["id"] == "10000003" {
			theirs[position] = map[string]any{"type": "section", "id": "10000003", "parent": "10000001",
				"title": "Call PSE", "updated": workStamp}
		}
	}

	merged, result := mergeDocs(t, base, ours, theirs)
	found := merged.find("10000003")

	if found["type"] != "section" {
		t.Fatalf("type = %v", found["type"])
	}
	if _, present := found["delegation"]; present {
		t.Fatal("only a task can carry a delegation")
	}
	if result.Events[0].Delegation.Reason != ClearedOnNonTask {
		t.Fatalf("reason = %#v", result.Events[0].Delegation)
	}
	assertValid(t, result.Text)
}

// Delegation resolution must be a maximum over ONE total order, which makes it
// associative and commutative: no sequence of pairwise device syncs can end on
// two different markers. States stay live here on purpose — clearing a marker on
// a closed or proposed task is the state machine's rule, and the state merge's
// own ordering is out of this property's scope.
func TestDelegationResolutionIsOrderIndependentAcrossThreeDevices(t *testing.T) {
	rng := newLCG(20260727)
	stamps := []string{"2026-07-27T09:00:00Z", "2026-07-27T10:00:00Z", "2026-07-27T11:00:00Z"}
	modes := []string{"refine", "research", "implement"}
	workers := []string{"aaa", "bbb", "ccc"}
	states := []string{"TODO", "NEXT", "WAITING"}

	shape := func() any {
		switch rng.intn(5) {
		case 0:
			return nil
		case 1:
			return ready(map[string]any{"mode": modes[rng.intn(3)], "at": stamps[rng.intn(3)]})
		case 2:
			return claim("worker/"+workers[rng.intn(3)], stamps[rng.intn(3)],
				map[string]any{"mode": modes[rng.intn(3)]})
		case 3:
			return claim("worker/"+workers[rng.intn(2)], stamps[rng.intn(3)],
				map[string]any{"work_ref": "https://example.com/" + string(rune('0'+rng.intn(2)))})
		default:
			return map[string]any{"kind": "human", "status": "delegated", "at": stamps[rng.intn(3)],
				"assignee": "p" + string(rune('0'+rng.intn(2))) + "@example.com"}
		}
	}

	for round := 0; round < 300; round++ {
		base := baseRecords().change("10000002", map[string]any{"delegation": shape()})
		devices := make([]doc, 3)
		for index := range devices {
			devices[index] = base.change("10000002", map[string]any{
				"delegation": shape(),
				"state":      states[rng.intn(3)],
				"updated": "2026-07-27T1" + string(rune('0'+rng.intn(3))) + ":0" +
					string(rune('0'+rng.intn(6))) + ":00Z#d" + string(rune('0'+index)),
			})
		}
		outcomes := map[string]bool{}
		for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 2, 0}, {2, 1, 0}, {1, 0, 2}, {2, 0, 1}} {
			pair := Merge(base.text(t), devices[order[0]].text(t), devices[order[1]].text(t))
			if !pair.OK() {
				t.Fatalf("pair merge failed: %s", pair.Error)
			}
			whole := Merge(base.text(t), pair.Text, devices[order[2]].text(t))
			if !whole.OK() {
				t.Fatalf("whole merge failed: %s", whole.Error)
			}
			encoded, _ := json.Marshal(delegationOf(parseDoc(t, whole.Text), "10000002"))
			outcomes[string(encoded)] = true
		}
		if len(outcomes) != 1 {
			keys := make([]string, 0, len(outcomes))
			for key := range outcomes {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			t.Fatalf("sync order changed the marker in round %d: %v", round, keys)
		}
	}
}

// newLCG is a tiny deterministic generator so the property above explores the
// same 300 shapes on every machine and every run.
type lcg struct{ state uint64 }

func newLCG(seed uint64) *lcg { return &lcg{state: seed} }

func (r *lcg) intn(bound int) int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int((r.state >> 33) % uint64(bound))
}

// -- schema versions ----------------------------------------------------------

func TestEitherSideAtAnotherSchemaVersionRefusesTheMerge(t *testing.T) {
	for _, label := range []string{"ours", "theirs"} {
		sides := map[string]doc{"base": baseRecords(), "ours": baseRecords(), "theirs": baseRecords()}
		sides[label] = baseRecords()
		sides[label][0]["version"] = 1

		result := Merge(sides["base"].text(t), sides["ours"].text(t), sides["theirs"].text(t))

		if result.OK() {
			t.Fatalf("%s at v1 must refuse", label)
		}
		wanted := label + " is schema v1; this binary reads schema v2 only"
		if result.Error != wanted {
			t.Fatalf("error = %q, want %q", result.Error, wanted)
		}
		if strings.Contains(strings.ToLower(result.Error), "migrat") {
			t.Fatalf("there is no migration to point the operator at: %q", result.Error)
		}
	}
}

// The BASE is not a side. It is consulted to tell "changed" from "unchanged",
// never merged, so an ancestor older than both sides is safe — and it is the
// ordinary shape of a merge that reaches back past a schema upgrade.
func TestABaseOlderThanBothSidesStillMerges(t *testing.T) {
	oldBase := baseRecords()
	oldBase[0]["version"] = 1
	base := baseRecords()
	ours := base.change("10000002", map[string]any{"title": "Ours edited"})
	theirs := base.change("10000003", map[string]any{"title": "Theirs edited"})

	result := Merge(oldBase.text(t), ours.text(t), theirs.text(t))

	if !result.OK() {
		t.Fatalf("a v1 base under v2 sides must merge: %s", result.Error)
	}
	merged := parseDoc(t, result.Text)
	if got := merged.find("10000002")["title"]; got != "Ours edited" {
		t.Fatalf("title = %v", got)
	}
	if got := merged.find("10000003")["title"]; got != "Theirs edited" {
		t.Fatalf("title = %v", got)
	}
	// The output is written at the version this binary implements — never
	// downgraded to the ancestor's.
	if got := merged[0]["version"]; got != float64(Version) {
		t.Fatalf("version = %v", got)
	}
}

func TestABaseNewerThanThisBinaryRefusesTheMerge(t *testing.T) {
	futureBase := baseRecords()
	futureBase[0]["version"] = 3
	base := baseRecords()

	result := Merge(futureBase.text(t), base.text(t), base.text(t))

	if result.OK() {
		t.Fatal("a base ahead of both sides must refuse")
	}
	if result.Error != "base is schema v3; this binary reads schema v2 only" {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestAFutureSchemaVersionOnASideRefusesTheMerge(t *testing.T) {
	v3 := baseRecords()
	v3[0]["version"] = 3
	base := baseRecords()

	result := Merge(base.text(t), base.text(t), v3.text(t))

	if result.OK() {
		t.Fatal("a v3 side must refuse")
	}
	if result.Error != "theirs is schema v3; this binary reads schema v2 only" {
		t.Fatalf("error = %q", result.Error)
	}
}

// -- deletes, adds, ordering --------------------------------------------------

func TestDeleteVsUnchangedDeletesButDeleteVsEditKeepsEdit(t *testing.T) {
	base := baseRecords()
	oursDeleted := base.without("10000003")

	unchanged, _ := mergeDocs(t, base, oursDeleted, base)
	if unchanged.find("10000003") != nil {
		t.Fatal("a delete against an untouched side deletes")
	}

	edited := base.change("10000003", map[string]any{"title": "Edited concurrently", "updated": workStamp})
	editedRecords, result := mergeDocs(t, base, oursDeleted, edited)
	if got := editedRecords.find("10000003")["title"]; got != "Edited concurrently" {
		t.Fatalf("title = %v", got)
	}
	if result.Events[0].Decision != DecisionKeptTheirsEditOverOursDelete {
		t.Fatalf("decision = %v", result.Events[0].Decision)
	}
}

func TestSubtreeDeleteVsDescendantEditRestoresRequiredAncestorChain(t *testing.T) {
	nested := baseRecords().change("10000003", map[string]any{"parent": "10000002"})
	oursDeleted := nested.without("10000002", "10000003")
	theirs := nested.change("10000003", map[string]any{"title": "Edited nested task", "updated": workStamp})

	merged, result := mergeDocs(t, nested, oursDeleted, theirs)

	if got := merged.find("10000003")["parent"]; got != "10000002" {
		t.Fatalf("parent = %v", got)
	}
	if merged.find("10000002") == nil {
		t.Fatal("the deleted ancestor is restored to keep the edited child valid")
	}
	if !containsString(decisions(result), DecisionRestoredAncestor) {
		t.Fatalf("decisions = %v", decisions(result))
	}
	assertValid(t, result.Text)
}

func TestAddsFromBothSidesAreKeptInValidOursFirstOrder(t *testing.T) {
	base := baseRecords()
	ours := append(base.copy(), map[string]any{"type": "task", "id": "10000005", "parent": "10000001",
		"state": "TODO", "title": "Ours add", "updated": homeStamp})
	theirs := append(base.copy(), map[string]any{"type": "task", "id": "10000006", "parent": "10000001",
		"state": "TODO", "title": "Theirs add", "updated": workStamp})

	merged, result := mergeDocs(t, base, ours, theirs)

	if merged[0]["type"] != "meta" {
		t.Fatalf("first record = %v", merged[0])
	}
	if merged.indexOf("10000005") >= merged.indexOf("10000006") {
		t.Fatalf("ours' add must land first: %v", merged)
	}
	assertValid(t, result.Text)
}

func TestTheirsOnlyParentAndChildAreInsertedAsAContiguousSubtree(t *testing.T) {
	base := baseRecords()
	theirs := append(base.copy(),
		map[string]any{"type": "section", "id": "20000001", "title": "Home"},
		map[string]any{"type": "task", "id": "20000002", "parent": "20000001", "state": "TODO",
			"title": "New child", "updated": workStamp})

	merged, result := mergeDocs(t, base, base, theirs)

	if merged.indexOf("20000002") != merged.indexOf("20000001")+1 {
		t.Fatalf("subtree is not contiguous: %v", merged)
	}
	assertValid(t, result.Text)
}

func TestConcurrentReorderingUsesOursAndIsLogged(t *testing.T) {
	base := baseRecords()
	ours := doc{base[0], base[1], base[3], base[2], base[4]}
	theirs := doc{base[0], base[1], base[4], base[2], base[3]}

	merged, result := mergeDocs(t, base, ours, theirs)

	ids := make([]string, 0, 3)
	for _, candidate := range merged {
		if candidate["type"] == "task" {
			ids = append(ids, candidate["id"].(string))
		}
	}
	if !reflect.DeepEqual(ids, []string{"10000003", "10000002", "10000004"}) {
		t.Fatalf("task order = %v", ids)
	}
	if !containsString(decisions(result), DecisionOursOrderingConflict) {
		t.Fatalf("decisions = %v", decisions(result))
	}
}

// -- refusals -----------------------------------------------------------------

func TestMalformedOrDuplicateSideFailsWithoutText(t *testing.T) {
	base := baseRecords()
	malformed := Merge(base.text(t), "not-json\n", base.text(t))
	if malformed.OK() {
		t.Fatal("an unparseable side must refuse")
	}
	if malformed.Text != "" {
		t.Fatal("a failed merge carries no text")
	}

	duplicate := append(base.copy(), base.copy()[len(base)-1])
	invalid := Merge(base.text(t), duplicate.text(t), base.text(t))
	if invalid.OK() {
		t.Fatal("a duplicate id must refuse")
	}
	if !strings.Contains(invalid.Error, "duplicate id") {
		t.Fatalf("error = %q", invalid.Error)
	}
}

func TestEmptyBaseSupportsConcurrentFirstArchiveCreation(t *testing.T) {
	base := baseRecords()
	ours := doc{base[0], base[1], base[2]}
	theirs := doc{base[0], base[1], base[3]}

	result := Merge("", ours.text(t), theirs.text(t))

	if !result.OK() {
		t.Fatalf("merge failed: %s", result.Error)
	}
	merged := parseDoc(t, result.Text)
	if merged.find("10000002") == nil || merged.find("10000003") == nil {
		t.Fatalf("both first-creation records must survive: %v", merged)
	}
	assertValid(t, result.Text)
}

func TestAnInvalidSideRefusesRatherThanMerging(t *testing.T) {
	base := baseRecords()
	broken := base.change("10000002", map[string]any{"state": "NOT-A-STATE"})

	result := Merge(base.text(t), broken.text(t), base.text(t))

	if result.OK() {
		t.Fatal("a side Check rejects must refuse")
	}
	if !strings.HasPrefix(result.Error, "ours is invalid: ") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestASideThatIsNotValidUTF8Refuses(t *testing.T) {
	base := baseRecords()

	result := Merge(base.text(t), base.text(t), "{\"type\":\"meta\",\"version\":2,\"x\":\"\xff\"}\n")

	if result.OK() {
		t.Fatal("invalid UTF-8 must refuse")
	}
	if result.Error != "theirs is not valid UTF-8" {
		t.Fatalf("error = %q", result.Error)
	}
}

// Each side reparents one task under the other and is individually valid; only
// their merge is cyclic. The refusal happens after the merged records have been
// built, which is the case a write-then-validate ordering would already have
// written by the time it failed.
func TestACyclicMergeRefusesAfterTheRecordsAreBuilt(t *testing.T) {
	base := baseRecords()
	ours := base.change("10000003", map[string]any{"parent": "10000002", "updated": homeStamp})
	// theirs must MOVE the record, not just reparent it: a parent has to resolve
	// to an earlier line, so each side stays individually valid and only the
	// merge of the two is cyclic.
	moved := base.change("10000002", map[string]any{"parent": "10000003", "updated": workStamp})
	theirs := doc{moved[0], moved[1], moved[3], moved[2], moved[4]}

	assertValid(t, ours.text(t))
	assertValid(t, theirs.text(t))
	result := Merge(base.text(t), ours.text(t), theirs.text(t))

	if result.OK() {
		t.Fatal("a cyclic merge must refuse")
	}
	if !strings.Contains(result.Error, "cyclic parents") {
		t.Fatalf("error = %q", result.Error)
	}
	if result.Text != "" {
		t.Fatal("a refused merge carries no text to write")
	}
}

func TestRealWorldSixtPseStashDivergenceMatchesHandResolution(t *testing.T) {
	base := baseRecords()
	ours := base.copy()
	ours.find("10000002")["tags"] = []any{"@computer", "travel"}
	ours.find("10000002")["updated"] = homeStamp
	ours.find("10000003")["title"] = "Call PSE about final bill"
	ours.find("10000003")["updated"] = homeStamp

	theirs := base.copy()
	theirs.find("10000002")["scheduled"] = "2026-07-19"
	theirs.find("10000002")["updated"] = workStamp
	theirs.find("10000004")["body"] = "Stash migration notes."
	theirs.find("10000004")["updated"] = workStamp

	merged, result := mergeDocs(t, base, ours, theirs)

	if got := merged.find("10000002")["tags"]; !reflect.DeepEqual(got, []any{"@computer", "travel"}) {
		t.Fatalf("tags = %#v", got)
	}
	if got := merged.find("10000002")["scheduled"]; got != "2026-07-19" {
		t.Fatalf("scheduled = %v", got)
	}
	if got := merged.find("10000003")["title"]; got != "Call PSE about final bill" {
		t.Fatalf("title = %v", got)
	}
	if got := merged.find("10000004")["body"]; got != "Stash migration notes." {
		t.Fatalf("body = %v", got)
	}
	assertValid(t, result.Text)
}

// -- cross-file safety --------------------------------------------------------

// The merge cannot see the other file, so an archive on one device racing a live
// edit on another produces two individually valid files that together duplicate
// an id. That pair must be caught by the store-wide check rather than merged
// into silence.
func TestArchiveVsConcurrentEditPairIsRejectedByCrossFileCheck(t *testing.T) {
	liveBase := baseRecords()
	liveArchiver := liveBase.without("10000003")
	liveEditor := liveBase.change("10000003", map[string]any{"title": "Edited while archiving", "updated": workStamp})
	mergedLive := Merge(liveBase.text(t), liveArchiver.text(t), liveEditor.text(t))
	if !mergedLive.OK() {
		t.Fatalf("live merge failed: %s", mergedLive.Error)
	}

	archiveBase := doc{liveBase[0], {"type": "section", "id": "90000001", "title": "Archive"}}
	archiveArchiver := append(archiveBase.copy(), map[string]any{
		"type": "task", "id": "10000003", "parent": "90000001", "state": "DONE",
		"title": "Call PSE", "closed": "2026-07-16", "updated": homeStamp})
	mergedArchive := Merge(archiveBase.text(t), archiveArchiver.text(t), archiveBase.text(t))
	if !mergedArchive.OK() {
		t.Fatalf("archive merge failed: %s", mergedArchive.Error)
	}

	dir := t.TempDir()
	livePath := dir + "/tasks.jsonl"
	archivePath := dir + "/archive.jsonl"
	writeFile(t, livePath, mergedLive.Text)
	writeFile(t, archivePath, mergedArchive.Text)

	result := check.CheckStore(livePath, archivePath)
	if result.OK() {
		t.Fatal("the live/archive pair must be rejected")
	}
	joined := make([]string, 0, len(result.Errors))
	for _, entry := range result.Errors {
		joined = append(joined, entry.Message)
	}
	if !strings.Contains(strings.Join(joined, "\n"), `id "10000003" appears in both`) {
		t.Fatalf("errors = %v", joined)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
