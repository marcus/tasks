package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mutation verbs are exercised as BLACK BOXES, exactly like the read
// commands: argv in, stdout/stderr/exit out, and the resulting file bytes
// inspected afterwards. That is the contract test/test_cli_mutations.rb holds
// bin/tasks to, and it is the only level at which "--dry-run wrote nothing" can
// be asserted at all — a unit test against the store would happily pass while
// the adapter took the write path anyway.
//
// Test names keep their Ruby spelling where one exists, so the two suites can be
// read side by side.

// mutationFixture is a small store with the shapes every mutation verb needs:
// a dated open task, an undated one, a task carrying both stamps, a proposal, a
// closed task, a deferred task, and a parent with an open child.
const mutationFixture = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Inbox"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"INBOX","title":"Unfiled capture"}
{"type":"section","id":"dddd0003","title":"Work"}
{"type":"task","id":"dddd0004","parent":"dddd0003","state":"NEXT","priority":"A","title":"Ship the release","tags":["@computer","important"],"deadline":"2026-08-01","body":"Notes first."}
{"type":"task","id":"dddd0005","parent":"dddd0003","state":"TODO","title":"Both stamps","scheduled":"2026-07-25","deadline":"2026-08-10"}
{"type":"task","id":"dddd0006","parent":"dddd0003","state":"PROPOSED","title":"A proposal"}
{"type":"task","id":"dddd0007","parent":"dddd0003","state":"DONE","title":"Finished chore","closed":"2026-07-01"}
{"type":"task","id":"dddd0008","parent":"dddd0003","state":"TODO","title":"On hold already","tags":["defer"]}
{"type":"task","id":"dddd0009","parent":"dddd0003","state":"TODO","title":"Parent of work"}
{"type":"task","id":"dddd000a","parent":"dddd0009","state":"TODO","title":"Child of work"}
{"type":"task","id":"dddd000b","parent":"dddd0003","state":"TODO","title":"Weekly chore","deadline":"2026-07-24","recur":"+1w"}
`

func storeBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "tasks.jsonl"))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	return string(data)
}

// runUnchanged asserts the invariant every `--dry-run` test in
// test_cli_mutations.rb asserts: the preview succeeded and the file is byte for
// byte what it was.
func runUnchanged(t *testing.T, dir string, argv ...string) cliResult {
	t.Helper()
	before := storeBytes(t, dir)
	result := runCLI(t, dir, argv...)
	if result.status != 0 {
		t.Fatalf("%v: exit %d, stderr %q", argv, result.status, result.stderr)
	}
	if after := storeBytes(t, dir); after != before {
		t.Fatalf("%v wrote to the store:\n%s", argv, after)
	}
	return result
}

func recordFor(t *testing.T, dir, id string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(storeBytes(t, dir), "\n") {
		if line == "" {
			continue
		}
		row := map[string]any{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("no record with id %s", id)
	return nil
}

// -- due / schedule ----------------------------------------------------------

func TestCLIDueSetsDeadline(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "due", "Unfiled capture", "2026-09-09")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0002")["deadline"]; got != "2026-09-09" {
		t.Errorf("deadline = %v", got)
	}
	// The report is the resulting headline, which is how a caller confirms the
	// write without a second read.
	if !strings.Contains(result.stdout, "Unfiled capture") {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestSetDateReplacesExistingDeadline(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "due", "Ship the release", "2026-09-09"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["deadline"]; got != "2026-09-09" {
		t.Errorf("deadline = %v", got)
	}
}

func TestSetDateDeadlineIgnoresExistingScheduled(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "due", "Both stamps", "2026-09-09"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0005")
	if row["deadline"] != "2026-09-09" || row["scheduled"] != "2026-07-25" {
		t.Errorf("row = %v", row)
	}
}

func TestSetDateScheduledKindSetsScheduledNotDeadline(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "schedule", "Unfiled capture", "2026-09-09"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0002")
	if row["scheduled"] != "2026-09-09" {
		t.Errorf("scheduled = %v", row["scheduled"])
	}
	if _, present := row["deadline"]; present {
		t.Errorf("deadline was written: %v", row)
	}
}

func TestCLIDueSupportsFloatingAndFixedTimeValues(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "due", "Unfiled capture", "2026-09-09 09:30", "--floating"); result.status != 0 {
		t.Fatalf("floating: exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0002")
	timed, ok := row["deadline_time"].(map[string]any)
	if !ok || timed["local"] != "09:30" {
		t.Fatalf("deadline_time = %v", row["deadline_time"])
	}
	if _, zoned := timed["timezone"]; zoned {
		t.Errorf("a floating value must carry no zone: %v", timed)
	}

	dir = seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "due", "Unfiled capture", "2026-09-09 09:30",
		"--timezone", "Europe/London"); result.status != 0 {
		t.Fatalf("fixed: exit %d, stderr %q", result.status, result.stderr)
	}
	timed = recordFor(t, dir, "dddd0002")["deadline_time"].(map[string]any)
	if timed["timezone"] != "Europe/London" {
		t.Errorf("timezone = %v", timed)
	}
}

func TestCLITemporalFlagsAreMutuallyExclusiveAndFoldIsChecked(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "due", "Unfiled capture", "2026-09-09 09:30",
		"--timezone", "Europe/London", "--floating")
	if result.status != 1 || !strings.Contains(result.stderr, "mutually exclusive") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	result = runCLI(t, dir, "due", "Unfiled capture", "2026-09-09 09:30", "--fold", "sideways")
	if result.status != 1 || !strings.Contains(result.stderr, "--fold must be earlier or later") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	// A modifier value that never arrived must abort, not swallow the next word.
	result = runCLI(t, dir, "due", "Unfiled capture", "2026-09-09", "--timezone")
	if result.status != 1 || !strings.Contains(result.stderr, "missing value for --timezone") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if after := storeBytes(t, dir); !strings.Contains(after, `"title":"Unfiled capture"}`) {
		t.Errorf("a refused invocation wrote: %s", after)
	}
}

func TestCLIDueBadDateExitsOneWithoutWriting(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "due", "Unfiled capture", "not a date")
	if result.status != 1 || !strings.Contains(result.stderr, "unrecognized date: not a date") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused date wrote to the store")
	}
}

func TestCLIDueUsageAndDryRunWriteNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "due")
	if result.status != 1 || !strings.Contains(result.stderr, "usage: tasks due") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	preview := runUnchanged(t, dir, "due", "Unfiled capture", "2026-09-09", "--dry-run")
	if want := "would set DEADLINE <2026-09-09> on: INBOX Unfiled capture\n"; preview.stdout != want {
		t.Errorf("stdout = %q, want %q", preview.stdout, want)
	}
	preview = runUnchanged(t, dir, "schedule", "Unfiled capture", "2026-09-09", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would set SCHEDULED <2026-09-09> on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

func TestCLIScheduleAmbiguousExitsTwo(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "schedule", "a", "2026-09-09")
	if result.status != refExit {
		t.Errorf("exit %d, want %d (stderr %q)", result.status, refExit, result.stderr)
	}
}

// -- undate ------------------------------------------------------------------

func TestUndateRemovesBothWhenNoKind(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "undate", "Both stamps"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0005")
	if _, present := row["deadline"]; present {
		t.Errorf("deadline survived: %v", row)
	}
	if _, present := row["scheduled"]; present {
		t.Errorf("scheduled survived: %v", row)
	}
}

func TestCLIUndateKindFlagRemovesOnlyThatKind(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "undate", "Both stamps", "--kind", "deadline"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0005")
	if _, present := row["deadline"]; present {
		t.Errorf("deadline survived: %v", row)
	}
	if row["scheduled"] != "2026-07-25" {
		t.Errorf("scheduled = %v", row["scheduled"])
	}
}

func TestCLIUndateNothingToRemoveExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "undate", "Unfiled capture")
	if result.status != 1 || !strings.Contains(result.stderr, "nothing to remove") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused undate wrote to the store")
	}
}

func TestCLIUndateBadKindExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "undate", "Both stamps", "--kind", "someday")
	if result.status != 1 || !strings.Contains(result.stderr, "--kind must be deadline or scheduled") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIUndateDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "undate", "Both stamps", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would remove SCHEDULED/DEADLINE from:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "undate", "Both stamps", "--kind", "scheduled", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would remove SCHEDULED from:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- state / cancel ----------------------------------------------------------

func TestCLIStateReopensDoneItem(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "state", "Finished chore", "TODO"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0007")
	if row["state"] != "TODO" {
		t.Errorf("state = %v", row["state"])
	}
	// Leaving a closed state retires the stamp; a reopened task with a `closed`
	// date would be a lie the archive sweep would act on.
	if _, present := row["closed"]; present {
		t.Errorf("closed stamp survived a reopen: %v", row)
	}
}

func TestCLIBadStateExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "state", "Unfiled capture", "SOMEDAY")
	if result.status != 1 || !strings.Contains(result.stderr, "unknown state: SOMEDAY") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stderr, "want one of PROPOSED, INBOX") {
		t.Errorf("the refusal must name the vocabulary: %q", result.stderr)
	}
}

func TestCLIStateDryRunPreviewsTheCascade(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "state", "Parent of work", "DONE", "--dry-run")
	if !strings.Contains(preview.stdout, "would set state DONE on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	if !strings.Contains(preview.stdout, "would also close 1 open descendant\n") {
		t.Errorf("the cascade must be previewed: %q", preview.stdout)
	}
	// `done` names its own verb, and its preview counts the same descendants.
	preview = runUnchanged(t, dir, "done", "Parent of work", "--dry-run")
	if !strings.Contains(preview.stdout, "would mark DONE:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

func TestCLIStateDoneIsRecurrenceAware(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	// A repeating task PREVIEWS as a roll, not as a close: the two are different
	// outcomes and the preview must not promise the wrong one.
	preview := runUnchanged(t, dir, "done", "Weekly chore", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would recur → ") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	if strings.Contains(preview.stdout, "would mark DONE") {
		t.Errorf("a recurring completion is not a close: %q", preview.stdout)
	}
}

func TestCLICancelMarksCancelled(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "cancel", "Unfiled capture"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0002")["state"]; got != "CANCELLED" {
		t.Errorf("state = %v", got)
	}
}

func TestCLICancelAliasDrop(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "drop", "Unfiled capture"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0002")["state"]; got != "CANCELLED" {
		t.Errorf("state = %v", got)
	}
}

func TestCLICancelAcceptsRepeatableNoteVisibleInShow(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "cancel", "Ship the release", "--note", "supplier withdrew",
		"--note", "revisit in Q4")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0004")
	if row["state"] != "CANCELLED" {
		t.Errorf("state = %v", row["state"])
	}
	// The rationale appends to the EXISTING body rather than replacing it, and
	// lands in the same write as the transition.
	if got := row["body"]; got != "Notes first.\nsupplier withdrew\nrevisit in Q4" {
		t.Errorf("body = %q", got)
	}
}

func TestCLICancelNoteWithoutAValueExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "cancel", "Unfiled capture", "--note")
	if result.status != 1 || !strings.Contains(result.stderr, "missing value for --note") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused cancel wrote to the store")
	}
}

func TestCLICancelDryRunNamesTheNote(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "cancel", "Unfiled capture", "--note", "no longer needed", "--dry-run")
	if !strings.Contains(preview.stdout, "would set state CANCELLED on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	if !strings.Contains(preview.stdout, "would add note: no longer needed\n") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

func TestCLICancelNoMatchExitsTwo(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "cancel", "nothing named this")
	if result.status != refExit {
		t.Errorf("exit %d, want %d", result.status, refExit)
	}
}

// -- retitle -----------------------------------------------------------------

func TestCLIRetitleReplacesTitle(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "retitle", "Unfiled capture", "A", "better", "name"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0002")["title"]; got != "A better name" {
		t.Errorf("title = %v", got)
	}
}

func TestCLIRetitleAliasRename(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "rename", "Unfiled capture", "Renamed"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0002")["title"]; got != "Renamed" {
		t.Errorf("title = %v", got)
	}
}

func TestCLIRetitleMissingTitleExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "retitle", "Unfiled capture")
	if result.status != 1 || !strings.Contains(result.stderr, `usage: tasks retitle <ref> "new title"`) {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIPresentationUpdatesCorrectAProposal(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	// A proposal exists to be edited before it is accepted, so the three
	// presentation verbs resolve one where `done` would not.
	for _, argv := range [][]string{
		{"retitle", "A proposal", "A better proposal"},
		{"tag", "A better proposal", "+draft"},
		{"note", "A better proposal", "rationale"},
		{"priority", "A better proposal", "B"},
	} {
		if result := runCLI(t, dir, argv...); result.status != 0 {
			t.Fatalf("%v: exit %d, stderr %q", argv, result.status, result.stderr)
		}
	}
	row := recordFor(t, dir, "dddd0006")
	if row["title"] != "A better proposal" || row["priority"] != "B" || row["body"] != "rationale" {
		t.Errorf("row = %v", row)
	}
	if row["state"] != "PROPOSED" {
		t.Errorf("editing a proposal must not accept it: %v", row["state"])
	}
}

// -- tag ---------------------------------------------------------------------

func TestCLITagAddsAndRemoves(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "tag", "Ship the release", "+urgent", "-important"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	// Stored ORDER is part of the headline's bytes: survivors keep their places
	// and additions land at the end.
	tags := recordFor(t, dir, "dddd0004")["tags"].([]any)
	if len(tags) != 2 || tags[0] != "@computer" || tags[1] != "urgent" {
		t.Errorf("tags = %v", tags)
	}
}

func TestCLITagRemovesContext(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "tag", "Ship the release", "-@computer"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	tags := recordFor(t, dir, "dddd0004")["tags"].([]any)
	if len(tags) != 1 || tags[0] != "important" {
		t.Errorf("tags = %v", tags)
	}
}

func TestSetTagsAddIsIdempotent(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "tag", "Ship the release", "+important"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	tags := recordFor(t, dir, "dddd0004")["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("a duplicate add must not grow the list: %v", tags)
	}
}

func TestCLITagBadSpecExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "tag", "Ship the release", "urgent")
	if result.status != 1 || !strings.Contains(result.stderr, "tag spec must start with +, -, or @: urgent") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused tag spec wrote to the store")
	}
	result = runCLI(t, dir, "tag", "Ship the release")
	if result.status != 1 || !strings.Contains(result.stderr, "usage: tasks tag") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLITagDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "tag", "Ship the release", "+urgent", "-important", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would apply +urgent -important to:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- note --------------------------------------------------------------------

func TestCLINoteAppendsLine(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "note", "Ship the release", "second", "line"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["body"]; got != "Notes first.\nsecond line" {
		t.Errorf("body = %q", got)
	}
}

func TestAddNoteAppendsBodyLineToAnEmptyBody(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "note", "Unfiled capture", "first note"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	// The first note is the whole body — no leading newline.
	if got := recordFor(t, dir, "dddd0002")["body"]; got != "first note" {
		t.Errorf("body = %q", got)
	}
}

func TestCLINoteDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "note", "Unfiled capture", "hello", "--dry-run")
	if !strings.Contains(preview.stdout, "would add note to INBOX Unfiled capture: hello\n") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	result := runCLI(t, dir, "note", "Unfiled capture")
	if result.status != 1 || !strings.Contains(result.stderr, `usage: tasks note <ref> "text"`) {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

// -- defer / someday / activate ---------------------------------------------

func TestCLIDeferTagsAndHidesFromActiveViews(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "defer", "Unfiled capture")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, `put "Unfiled capture" on hold (Someday/Maybe) — on hold indefinitely`) {
		t.Errorf("stdout = %q", result.stdout)
	}
	tags := recordFor(t, dir, "dddd0002")["tags"].([]any)
	if len(tags) != 1 || tags[0] != "defer" {
		t.Errorf("tags = %v", tags)
	}
}

func TestCLIDeferSynonymSnooze(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "snooze", "Unfiled capture"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if tags, ok := recordFor(t, dir, "dddd0002")["tags"].([]any); !ok || tags[0] != "defer" {
		t.Errorf("tags = %v", recordFor(t, dir, "dddd0002")["tags"])
	}
}

func TestCLITimedDeferSetsAvailableFromAndClearsHold(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "defer", "On hold already", "2026-09-09")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0008")
	if row["scheduled"] != "2026-09-09" {
		t.Errorf("scheduled = %v", row["scheduled"])
	}
	// Both halves in ONE write: the date arrives and the indefinite hold goes,
	// so a reader never sees a task that is both scheduled and on hold.
	if tags, present := row["tags"]; present {
		for _, tag := range tags.([]any) {
			if tag == "defer" {
				t.Errorf("the hold survived: %v", tags)
			}
		}
	}
	if !strings.Contains(result.stdout, "unavailable until 2026-09-09") {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestCLISomedayIsCanonicalIndefiniteAliasAndRejectsADate(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "someday", "Unfiled capture", "2026-09-09")
	if result.status != 1 || !strings.Contains(result.stderr, "usage: tasks someday <ref>") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused someday wrote to the store")
	}
	if result := runCLI(t, dir, "someday", "Unfiled capture"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if tags := recordFor(t, dir, "dddd0002")["tags"].([]any); tags[0] != "defer" {
		t.Errorf("tags = %v", tags)
	}
}

func TestCLIDeferDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "defer", "Unfiled capture", "2026-09-09", "--dry-run")
	if !strings.HasPrefix(preview.stdout, `would defer "Unfiled capture" until 2026-09-09 — `) {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "someday", "Unfiled capture", "--dry-run")
	if !strings.Contains(preview.stdout, "on hold indefinitely") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

func TestCLIDeferRefusesModifiersWithoutADate(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "defer", "Unfiled capture", "--floating")
	if result.status != 1 || !strings.Contains(result.stderr, "temporal modifiers require a date") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIActivateClearsDeferTag(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "activate", "On hold already")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if !strings.Contains(result.stdout, `activate "On hold already" — available now`) {
		t.Errorf("stdout = %q", result.stdout)
	}
	if tags, present := recordFor(t, dir, "dddd0008")["tags"]; present {
		for _, tag := range tags.([]any) {
			if tag == "defer" {
				t.Errorf("the hold survived: %v", tags)
			}
		}
	}
}

func TestCLIActivateRefusesExtraPositionals(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "activate", "On hold already", "now")
	if result.status != 1 || !strings.Contains(result.stderr, "usage: tasks activate <ref>") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIActivateDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "activate", "On hold already", "--dry-run")
	if !strings.HasPrefix(preview.stdout, `would activate "On hold already" — `) {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- lead --------------------------------------------------------------------

func TestLeadSetsTheWindowAndReportsTheResultingAvailability(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "lead", "Ship the release", "3w")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["lead"]; got != "3w" {
		t.Errorf("lead = %v", got)
	}
	if !strings.Contains(result.stdout, `lead time 3w on "Ship the release" (3w before 2026-08-01)`) {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestLeadAcceptsPhrasesAndOff(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "lead", "Ship the release", "3", "weeks"); result.status != 0 {
		t.Fatalf("phrase: exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["lead"]; got != "3w" {
		t.Errorf("lead = %v", got)
	}
	result := runCLI(t, dir, "lead", "Ship the release", "off")
	if result.status != 0 {
		t.Fatalf("off: exit %d, stderr %q", result.status, result.stderr)
	}
	if _, present := recordFor(t, dir, "dddd0004")["lead"]; present {
		t.Error("off must clear the span")
	}
	if !strings.Contains(result.stdout, `clear the lead time on "Ship the release"`) {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestRuleOneRefusesALeadOnAnUndatedTask(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "lead", "Unfiled capture", "3w")
	if result.status != 1 || !strings.Contains(result.stderr, "has no date to hide before") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused lead wrote to the store")
	}
}

func TestRuleThreeRefusesASecondTimedGate(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "lead", "Both stamps", "3w")
	if result.status != 1 || !strings.Contains(result.stderr, "a second, ignored gate — pick one") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestRuleFourRefusesJunk(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "lead", "Ship the release", "banana")
	if result.status != 1 || !strings.Contains(result.stderr, leadHint) {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestLeadReadOnlyPreviewReportsTheSpanAndTheOpeningDate(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "lead", "Ship the release", "3w"); result.status != 0 {
		t.Fatalf("setup: exit %d, stderr %q", result.status, result.stderr)
	}
	preview := runUnchanged(t, dir, "lead", "Ship the release")
	if !strings.Contains(preview.stdout, "⏳ 3 weeks before (3w)") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	if !strings.Contains(preview.stdout, "opens 2026-07-11 (Sat) — 3 weeks before 2026-08-01") {
		t.Errorf("stdout = %q", preview.stdout)
	}

	structured := runUnchanged(t, dir, "lead", "Ship the release", "--json")
	row := map[string]any{}
	if err := json.Unmarshal([]byte(structured.stdout), &row); err != nil {
		t.Fatalf("parse: %v (%s)", err, structured.stdout)
	}
	if row["lead"] != "3w" || row["anchor"] != "2026-08-01" || row["opens"] != "2026-07-11" {
		t.Errorf("row = %v", row)
	}
	if row["lead_human"] != "3 weeks" {
		t.Errorf("lead_human = %v", row["lead_human"])
	}

	// A task with no span says so rather than printing an empty window.
	bare := runUnchanged(t, dir, "lead", "Unfiled capture")
	if !strings.Contains(bare.stdout, "no lead time — set one with `tasks lead \"Unfiled capture\" 3w`") {
		t.Errorf("stdout = %q", bare.stdout)
	}
}

func TestLeadDryRunPreviewsWithoutWriting(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "lead", "Ship the release", "3w", "--dry-run")
	if !strings.HasPrefix(preview.stdout, `would lead time 3w on "Ship the release" (3w before 2026-08-01) — `) {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- recur -------------------------------------------------------------------

func TestCLIRecurSetsCookieFromFriendlyWord(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "recur", "Ship the release", "weekly")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["recur"]; got != ".+1w" {
		t.Errorf("recur = %v", got)
	}
	if !strings.Contains(result.stdout, "↻ every week from completion (.+1w)") {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestCLIRecurFromScheduleUsesFixedPrefix(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "recur", "Ship the release", "weekly",
		"--from", "schedule"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if got := recordFor(t, dir, "dddd0004")["recur"]; got != "+1w" {
		t.Errorf("recur = %v", got)
	}
}

func TestCLIRecurOffClears(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "recur", "Weekly chore", "off"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if _, present := recordFor(t, dir, "dddd000b")["recur"]; present {
		t.Error("off must clear the cookie")
	}
}

func TestCLIRecurUndatedTaskWithoutOnExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "recur", "Unfiled capture", "weekly")
	if result.status != 1 || !strings.Contains(result.stderr, "has no date to repeat") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a refused recurrence wrote to the store")
	}
}

func TestCLIRecurUndatedTaskWithOnSeedsDate(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	if result := runCLI(t, dir, "recur", "Unfiled capture", "weekly",
		"--on", "2026-09-09"); result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	row := recordFor(t, dir, "dddd0002")
	// One transaction: the seeded stamp and the cookie land together, so a
	// refusal would have left neither.
	if row["deadline"] != "2026-09-09" || row["recur"] != ".+1w" {
		t.Errorf("row = %v", row)
	}
}

func TestCLIRecurOffOnUndatedIsNoopSuccess(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	before := storeBytes(t, dir)
	result := runCLI(t, dir, "recur", "Unfiled capture", "off")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	if storeBytes(t, dir) != before {
		t.Error("a no-op clear must not write, so it cannot consume an undo entry")
	}
	if !strings.Contains(result.stdout, "Unfiled capture") {
		t.Errorf("stdout = %q", result.stdout)
	}
}

func TestCLIRecurOnClosedTaskRejected(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "recur", "Finished chore", "weekly", "--include-done")
	if result.status != 1 || !strings.Contains(result.stderr, "can't set recurrence on a DONE task") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIRecurBadIntervalExitsOne(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "recur", "Ship the release", "fortnightish")
	if result.status != 1 || !strings.Contains(result.stderr, recurHint) {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIRecurNoMatchExitsTwo(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "recur", "nothing named this", "weekly")
	if result.status != refExit {
		t.Errorf("exit %d, want %d", result.status, refExit)
	}
}

func TestCLIRecurDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "recur", "Ship the release", "weekly", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would set recurrence .+1w (every week from completion) on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "recur", "Unfiled capture", "off", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would leave unchanged (no recurrence set) on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "recur", "Unfiled capture", "weekly",
		"--on", "2026-09-09", "--dry-run")
	if !strings.Contains(preview.stdout, ", seeding DEADLINE <2026-09-09> on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

func TestCLIRecurReadOnlyPreviewProjectsFromTheStamp(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "recur", "Weekly chore", "--count", "3")
	// The stamp IS the next occurrence, so the projection starts there.
	for _, want := range []string{"↻ every week from the scheduled date (+1w)", "2026-07-24", "2026-07-31", "2026-08-07"} {
		if !strings.Contains(preview.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, preview.stdout)
		}
	}
	structured := runUnchanged(t, dir, "recur", "Weekly chore", "--count", "2", "--json")
	row := map[string]any{}
	if err := json.Unmarshal([]byte(structured.stdout), &row); err != nil {
		t.Fatalf("parse: %v (%s)", err, structured.stdout)
	}
	if row["recur"] != "+1w" || row["anchor"] != "2026-07-24" {
		t.Errorf("row = %v", row)
	}
	if next := row["next"].([]any); len(next) != 2 || next[0] != "2026-07-24" {
		t.Errorf("next = %v", row["next"])
	}
}

func TestCLIRecurReadOnlyPreviewRefusesWriteFlags(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "recur", "Weekly chore", "--dry-run")
	if result.status != 1 || !strings.Contains(result.stderr, "is read-only — pass a schedule to set one") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	result = runCLI(t, dir, "recur", "Ship the release", "weekly", "--count", "3")
	if result.status != 1 || !strings.Contains(result.stderr, "--count previews occurrences") {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
	for _, bad := range []string{"0", "-2", "abc", "51"} {
		result = runCLI(t, dir, "recur", "Weekly chore", "--count", bad)
		if result.status != 1 {
			t.Errorf("--count %s: exit %d", bad, result.status)
		}
	}
}

func TestCLIRecurExplainNeedsNoTaskAndBranchesOnExitStatus(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runUnchanged(t, dir, "recur", "--explain", "every mon,wed", "--count", "2")
	if !strings.HasPrefix(result.stdout, "w:mon,wed — ") {
		t.Errorf("stdout = %q", result.stdout)
	}

	structured := runUnchanged(t, dir, "recur", "--explain", "weekly", "--count", "2", "--json")
	row := map[string]any{}
	if err := json.Unmarshal([]byte(structured.stdout), &row); err != nil {
		t.Fatalf("parse: %v (%s)", err, structured.stdout)
	}
	if row["input"] != "weekly" || row["canonical"] != ".+1w" {
		t.Errorf("row = %v", row)
	}
	if next := row["next"].([]any); len(next) != 2 {
		t.Errorf("next = %v", row["next"])
	}

	// An unreadable schedule exits 1 in BOTH renderings, so a script can branch
	// on the status alone rather than parsing prose.
	bad := runCLI(t, dir, "recur", "--explain", "fortnightish")
	if bad.status != 1 {
		t.Errorf("exit %d", bad.status)
	}
	bad = runCLI(t, dir, "recur", "--explain", "fortnightish", "--json")
	if bad.status != 1 || !strings.Contains(bad.stdout, `"error"`) {
		t.Errorf("exit %d, stdout %q", bad.status, bad.stdout)
	}

	// `off` parses cleanly and means "clear the schedule".
	off := runUnchanged(t, dir, "recur", "--explain", "off")
	if off.stdout != "off — clears any schedule on the task\n" {
		t.Errorf("stdout = %q", off.stdout)
	}

	refused := runCLI(t, dir, "recur", "--explain", "weekly", "--on", "2026-09-09")
	if refused.status != 1 || !strings.Contains(refused.stderr, "--explain previews a schedule, not a task") {
		t.Errorf("exit %d, stderr %q", refused.status, refused.stderr)
	}
}

// -- priority ----------------------------------------------------------------

func TestCLIPriorityDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "priority", "Unfiled capture", "B", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would set priority [#B] on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "priority", "Ship the release", "none", "--dry-run")
	if !strings.HasPrefix(preview.stdout, "would set priority (none) on:") {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- capture -----------------------------------------------------------------

func TestCLICaptureDryRunWritesNothing(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	preview := runUnchanged(t, dir, "capture", "A new thing", "--dry-run")
	if preview.stdout != "would capture under Inbox: INBOX A new thing\n" {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "capture", "A filed thing", "--priority", "B",
		"--context", "home", "--tag", "chore", "--project", "Work", "--dry-run")
	if preview.stdout != "would capture under Work: INBOX [#B] A filed thing :@home:chore:\n" {
		t.Errorf("stdout = %q", preview.stdout)
	}
	preview = runUnchanged(t, dir, "propose", "An idea", "--dry-run")
	if preview.stdout != "would propose under Inbox: PROPOSED An idea\n" {
		t.Errorf("stdout = %q", preview.stdout)
	}
}

// -- help --------------------------------------------------------------------

func TestCLIHelpPrintsReference(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	for _, spelling := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		result := runCLI(t, dir, spelling...)
		if result.status != 0 {
			t.Fatalf("%v: exit %d, stderr %q", spelling, result.status, result.stderr)
		}
		if !strings.HasPrefix(result.stdout, "tasks — a plain-text GTD CLI over tasks.jsonl.") {
			t.Errorf("%v: stdout = %q", spelling, result.stdout[:60])
		}
	}
	// Help must never be the command that rejects your guess.
	if result := runCLI(t, dir, "help", "list", "--anything"); result.status != 0 {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}

func TestCLIHelpJSONEmitsTheCommandRegistry(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "help", "--json")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	document := struct {
		Commands []struct {
			Name             string   `json:"name"`
			Aliases          []string `json:"aliases"`
			JSON             bool     `json:"json"`
			SchemaGate       bool     `json:"schema_gate"`
			JSONReason       string   `json:"json_reason"`
			SchemaGateReason string   `json:"schema_gate_reason"`
		} `json:"commands"`
	}{}
	if err := json.Unmarshal([]byte(result.stdout), &document); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(document.Commands) != len(helpCommands) {
		t.Fatalf("%d commands, want %d", len(document.Commands), len(helpCommands))
	}
	byName := map[string]int{}
	for index, command := range document.Commands {
		byName[command.Name] = index
		// The two opt-outs are the enforcement story: a command cannot decline
		// to answer in JSON, or to be schema-gated, without saying why.
		if !command.JSON && command.JSONReason == "" {
			t.Errorf("%s: json: false with no reason", command.Name)
		}
		if !command.SchemaGate && command.SchemaGateReason == "" {
			t.Errorf("%s: schema_gate: false with no reason", command.Name)
		}
	}
	for _, name := range []string{"due", "schedule", "undate", "state", "cancel", "retitle",
		"tag", "note", "defer", "someday", "activate", "lead", "recur", "help"} {
		if _, found := byName[name]; !found {
			t.Errorf("registry is missing %q", name)
		}
	}
	// The registry must agree with the dispatcher about which names exist,
	// because both `help --json` and alias resolution read from it.
	for _, command := range document.Commands {
		dispatch := strings.Split(command.Name, " ")[0]
		if !canonicalCommands[command.Name] && !canonicalCommands[dispatch] {
			t.Errorf("%q is published by help but is not a canonical command", command.Name)
		}
		for _, alias := range command.Aliases {
			token := strings.Split(alias, " ")[0]
			if aliasTokens[token] != dispatch {
				t.Errorf("alias %q resolves to %q, not %q", alias, aliasTokens[token], dispatch)
			}
		}
	}
}

// -- shared refusals ---------------------------------------------------------

func TestCLIUnknownFlagExitsOneForEveryMutationVerb(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	// An unrecognized --flag accepted as a positional would become title text
	// and perform the mutation it was meant to modify.
	for _, argv := range [][]string{
		{"due", "Unfiled capture", "2026-09-09", "--nope"},
		{"schedule", "Unfiled capture", "2026-09-09", "--nope"},
		{"undate", "Both stamps", "--nope"},
		{"state", "Unfiled capture", "TODO", "--nope"},
		{"cancel", "Unfiled capture", "--nope"},
		{"retitle", "Unfiled capture", "New", "--nope"},
		{"tag", "Unfiled capture", "+x", "--nope"},
		{"note", "Unfiled capture", "text", "--nope"},
		{"defer", "Unfiled capture", "--nope"},
		{"someday", "Unfiled capture", "--nope"},
		{"activate", "On hold already", "--nope"},
		{"lead", "Ship the release", "3w", "--nope"},
		{"recur", "Weekly chore", "weekly", "--nope"},
	} {
		before := storeBytes(t, dir)
		result := runCLI(t, dir, argv...)
		if result.status != 1 || !strings.Contains(result.stderr, "unknown flag: --nope") {
			t.Errorf("%v: exit %d, stderr %q", argv, result.status, result.stderr)
		}
		if storeBytes(t, dir) != before {
			t.Fatalf("%v wrote despite the refusal", argv)
		}
	}
}

func TestCLIMutationAgainstAnUnsupportedSchemaIsRefusedWithoutWriting(t *testing.T) {
	const future = `{"type":"meta","version":3}
{"type":"section","id":"eeee0001","title":"Inbox"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"INBOX","title":"Unreadable"}
`
	for _, argv := range [][]string{
		{"due", "Unreadable", "2026-09-09"},
		{"state", "Unreadable", "TODO"},
		{"tag", "Unreadable", "+x"},
		{"note", "Unreadable", "text"},
		{"someday", "Unreadable"},
		{"activate", "Unreadable"},
		{"lead", "Unreadable", "3w"},
		{"recur", "Unreadable", "weekly"},
		{"retitle", "Unreadable", "New"},
		{"undate", "Unreadable"},
	} {
		dir := seedStore(t, future)
		before := storeBytes(t, dir)
		result := runCLI(t, dir, argv...)
		if result.status != 1 {
			t.Errorf("%v: exit %d", argv, result.status)
		}
		if !strings.Contains(result.stderr, "unsupported meta version 3 (expected 2)") {
			t.Errorf("%v: stderr = %q", argv, result.stderr)
		}
		if storeBytes(t, dir) != before {
			t.Errorf("%v wrote to a store it cannot read", argv)
		}
	}

	// Under --json the refusal is a document on stdout too: a caller that got
	// nothing there cannot tell a refusal from an empty result.
	dir := seedStore(t, future)
	result := runCLI(t, dir, "tag", "Unreadable", "+x", "--json")
	row := map[string]any{}
	if err := json.Unmarshal([]byte(result.stdout), &row); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if row["error"] != "unsupported_schema_version" || row["action"] != "tag" {
		t.Errorf("row = %v", row)
	}
}

func TestCLIMutationJSONReportsTheTouchedTask(t *testing.T) {
	dir := seedStore(t, mutationFixture)
	result := runCLI(t, dir, "retitle", "Unfiled capture", "Renamed", "--json")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	document := struct {
		Touched []map[string]any `json:"touched"`
	}{}
	if err := json.Unmarshal([]byte(result.stdout), &document); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if len(document.Touched) != 1 || document.Touched[0]["title"] != "Renamed" {
		t.Errorf("touched = %v", document.Touched)
	}
}

// TestCLIMutationOnInvalidFileHintsAtCheck pins the distinction differential
// testing found the port collapsing.
//
// "The task moved under you" and "this file does not validate" leave identical
// bytes behind and exit the same way; only the second sentence tells them apart,
// and only the second one names a fix the user can perform. The adapter used to
// refuse before submitting whenever it could not read a baseline, which turned
// every invalid-file mutation into a phantom concurrent edit.
func TestCLIMutationOnInvalidFileHintsAtCheck(t *testing.T) {
	const invalid = `{"type":"meta","version":2}
{"type":"section","id":"ffff0001","title":"Inbox"}
{"type":"task","id":"ffff0002","parent":"ffff0001","state":"INBOX","title":"Readable"}
{"type":"task","id":"ffff0002","parent":"ffff0001","state":"INBOX","title":"Duplicate id"}
`
	for _, probe := range []struct {
		argv    []string
		summary string
	}{
		{[]string{"due", "Readable", "2026-09-09"}, "failed to set deadline"},
		{[]string{"tag", "Readable", "+x"}, "failed to update tags"},
		{[]string{"retitle", "Readable", "New"}, "failed to retitle"},
	} {
		dir := seedStore(t, invalid)
		before := storeBytes(t, dir)
		result := runCLI(t, dir, probe.argv...)
		if result.status != 1 {
			t.Errorf("%v: exit %d", probe.argv, result.status)
		}
		want := probe.summary + "\ntask file is already invalid — run `tasks check` (nothing was written)\n"
		if result.stderr != want {
			t.Errorf("%v: stderr = %q, want %q", probe.argv, result.stderr, want)
		}
		if storeBytes(t, dir) != before {
			t.Errorf("%v wrote to an invalid file", probe.argv)
		}
	}

	// The CHANGESET verbs answer differently, and deliberately: without a
	// readable editor snapshot there is no revision to guard the write with, so
	// nothing is attempted and no `check` hint is earned.
	dir := seedStore(t, invalid)
	result := runCLI(t, dir, "someday", "Readable")
	if result.status != 1 || result.stderr != "failed to defer\n" {
		t.Errorf("exit %d, stderr %q", result.status, result.stderr)
	}
}
