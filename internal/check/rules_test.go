package check

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The rules test/test_check.rb states that the fixture-oracle tables in
// check_test.go do not reach on their own: tree shape, the delegation
// invariants one by one, and the store-pair rules. Written against hand-built
// JSONL rather than the emitter, so a fault in the writer cannot make the
// linter look correct.

const metaLine = `{"type":"meta","version":2}`

// store assembles a file from lines, the way Ruby's check_records does from
// record hashes.
func store(lines ...string) []byte {
	return []byte(metaLine + "\n" + strings.Join(lines, "\n") + "\n")
}

func section(id, title string, extra ...string) string {
	return object(`"type":"section","id":`+quote(id)+`,"title":`+quote(title), extra...)
}

func task(id, parent, state, title string, extra ...string) string {
	fields := `"type":"task","id":` + quote(id)
	if parent != "" {
		fields += `,"parent":` + quote(parent)
	}
	return object(fields+`,"state":`+quote(state)+`,"title":`+quote(title), extra...)
}

func object(fields string, extra ...string) string {
	for _, field := range extra {
		fields += "," + field
	}
	return "{" + fields + "}"
}

func messages(result Result) string {
	parts := make([]string, 0, len(result.Errors))
	for _, entry := range result.Errors {
		parts = append(parts, entry.Message)
	}
	return strings.Join(parts, " | ")
}

func warningMessages(result Result) []string {
	parts := make([]string, 0, len(result.Warnings))
	for _, entry := range result.Warnings {
		parts = append(parts, entry.Message)
	}
	return parts
}

// test_example_store_is_clean — the sample a new user seeds from is itself a
// structurally valid store, and both implementations have to agree it is.
func TestExampleStoreIsClean(t *testing.T) {
	path := filepath.Join("..", "..", "..", "examples", "tasks.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no shipped example store: %v", err)
	}
	if result := Check(path); !result.OK() {
		t.Fatalf("errors = %#v, want a clean example store", result.Errors)
	}
}

// test_sections_are_not_flagged: a section carrying only its own fields, and a
// task beneath it, produce nothing.
func TestSectionsAndNestedTasksAreNotFlagged(t *testing.T) {
	text := store(
		section("aaaa0001", "Inbox"),
		section("aaaa0002", "Next Actions"),
		task("aaaa0003", "aaaa0002", "NEXT", "do a thing"),
	)
	result := CheckText(text)
	if !result.OK() || len(result.Warnings) != 0 {
		t.Fatalf("errors = %#v, warnings = %#v, want a clean file", result.Errors, result.Warnings)
	}
}

// -- tree shape --------------------------------------------------------------

// test_parent_must_resolve_to_an_earlier_record. Forward references are what
// make line order and tree shape the same fact; without the rule a reader would
// have to scan the whole file before it could nest anything.
func TestParentMustResolveToAnEarlierRecord(t *testing.T) {
	text := store(task("aaaa0002", "nope0000", "NEXT", "orphan"))
	want := []Entry{{2, `parent "nope0000" does not resolve to an earlier record`}}
	if got := CheckText(text).Errors; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
	// A parent named LATER in the file is still a forward reference.
	forward := store(
		task("aaaa0002", "aaaa0003", "NEXT", "child before parent"),
		section("aaaa0003", "Work"),
	)
	if got := messages(CheckText(forward)); !strings.Contains(got, "does not resolve to an earlier record") {
		t.Fatalf("errors = %q", got)
	}
}

// test_dfs_pre_order_violation: a grandchild appearing after its parent's
// subtree already closed.
func TestDFSPreOrderViolation(t *testing.T) {
	text := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "NEXT", "child1"),
		task("aaaa0003", "aaaa0001", "NEXT", "sibling"),
		task("aaaa0004", "aaaa0002", "NEXT", "grandchild out of order"),
	)
	want := []Entry{{5, `record "aaaa0004" breaks DFS pre-order (parent "aaaa0002" is not an open ancestor)`}}
	if got := CheckText(text).Errors; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}

	// The shapes that are legal: descending, and popping back to any open
	// ancestor. Neither may be reported.
	legal := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "NEXT", "child"),
		task("aaaa0003", "aaaa0002", "NEXT", "grandchild"),
		task("aaaa0004", "aaaa0003", "NEXT", "great-grandchild"),
		task("aaaa0005", "aaaa0002", "NEXT", "back up one level"),
		task("aaaa0006", "aaaa0001", "NEXT", "back to the root"),
	)
	if result := CheckText(legal); !result.OK() {
		t.Fatalf("errors = %#v, want a legal DFS walk to pass", result.Errors)
	}
}

// A record with no parent opens a new root, which is what lets a file hold
// several top-level sections without the second one looking out of order.
func TestParentlessRecordOpensANewRoot(t *testing.T) {
	text := store(
		section("aaaa0001", "First"),
		task("aaaa0002", "aaaa0001", "NEXT", "beneath the first"),
		section("aaaa0003", "Second"),
		task("aaaa0004", "aaaa0003", "NEXT", "beneath the second"),
	)
	if result := CheckText(text); !result.OK() {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

// -- section rules -----------------------------------------------------------

// test_section_must_not_carry_task_fields, one field at a time rather than the
// three the fixture happens to hold.
func TestSectionMustNotCarryAnyTaskField(t *testing.T) {
	values := map[string]string{
		"state": `"NEXT"`, "priority": `"A"`, "scheduled": `"2026-07-01"`,
		"scheduled_time": `{"local":"09:00"}`, "deadline": `"2026-07-01"`,
		"deadline_time": `{"local":"09:00"}`, "recur": `".+1w"`, "lead": `"3w"`,
		"lead_skip": `"2026-07-01"`, "closed": `"2026-07-01"`, "tags": `["@home"]`,
		"delegation": `{"kind":"agent","mode":"research","status":"ready","at":"2026-07-27T18:04:11Z"}`,
	}
	for key, value := range values {
		text := store(section("aaaa0001", "W", quote(key)+":"+value))
		want := "section must not carry " + quote(key)
		if got := messages(CheckText(text)); !strings.Contains(got, want) {
			t.Fatalf("section with %s: errors = %q, want %q", key, got, want)
		}
	}
	// `archived` is deliberately allowed: a swept subtree root can be a section.
	if result := CheckText(store(section("aaaa0001", "W", `"archived":"2026-07-01"`))); !result.OK() {
		t.Fatalf("errors = %#v, want archived to be legal on a section", result.Errors)
	}
}

func TestSectionRequiresATitle(t *testing.T) {
	for _, fields := range []string{
		`"type":"section","id":"aaaa0001"`,
		`"type":"section","id":"aaaa0001","title":""`,
		`"type":"section","id":"aaaa0001","title":"   "`,
		`"type":"section","id":"aaaa0001","title":null`,
	} {
		text := store("{" + fields + "}")
		if got := messages(CheckText(text)); !strings.Contains(got, "section has no title") {
			t.Fatalf("%s: errors = %q", fields, got)
		}
	}
}

// -- delegation --------------------------------------------------------------

const validDelegation = `{"kind":"agent","mode":"research","status":"ready","at":"2026-07-27T18:04:11Z"}`

func delegatedTask(delegation string, state string) []byte {
	return store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", state, "a task", `"delegation":`+delegation),
	)
}

// test_valid_delegations_pass_and_are_a_known_key
func TestValidDelegationsPassAndAreAKnownKey(t *testing.T) {
	if !knownKeys["delegation"] {
		t.Fatal("delegation must be a known key, or every marker would warn")
	}
	for _, delegation := range []string{
		validDelegation,
		`{"kind":"agent","mode":"implement","status":"claimed","assignee":"cc/fable5/aaaa1111","at":"2026-07-27T18:04:11Z","work_ref":"https://example.com/pr/42"}`,
		`{"kind":"human","status":"delegated","assignee":"pat@example.com","at":"2026-07-27T18:04:11Z"}`,
	} {
		result := CheckText(delegatedTask(delegation, "NEXT"))
		if !result.OK() || len(result.Warnings) != 0 {
			t.Fatalf("%s: errors = %#v, warnings = %#v", delegation, result.Errors, result.Warnings)
		}
	}
}

// test_every_delegation_invariant_violation_is_an_error, case for case.
func TestEveryDelegationInvariantViolationIsAnError(t *testing.T) {
	const at = `"at":"2026-07-27T18:04:11Z"`
	for _, tc := range []struct {
		want       string
		delegation string
	}{
		{"delegation must be an object", `"claimed"`},
		{`kind "team" must be human or agent`, `{"kind":"team","status":"ready",` + at + `}`},
		{"kind nil must be human or agent", `{"status":"ready",` + at + `}`},
		// A mode of the wrong SHAPE is an error for either kind; membership is a
		// separate question, and on disk it is a warning (see
		// TestAnUnconfiguredModeOnDiskIsAWarningNotAnError).
		{"mode \"Vibes!\" must be one of refine/research/implement", `{"kind":"human","mode":"Vibes!","status":"delegated","assignee":"pat@example.com",` + at + `}`},
		{"note must be a non-empty string", `{"kind":"agent","mode":"refine","status":"ready",` + at + `,"note":"   "}`},
		{"note must not contain control characters", `{"kind":"agent","mode":"refine","status":"ready",` + at + `,"note":"do it\u001b[2K"}`},
		{"note must be at most 2000 characters", `{"kind":"agent","mode":"refine","status":"ready",` + at + `,"note":"` + strings.Repeat("x", 2001) + `"}`},
		{`status "ready" must be "delegated" for a human`, `{"kind":"human","status":"ready","assignee":"pat@example.com",` + at + `}`},
		{`assignee "pat" must be an email address`, `{"kind":"human","status":"delegated","assignee":"pat",` + at + `}`},
		{`assignee "a b@c.d" must be an email address`, `{"kind":"human","status":"delegated","assignee":"a b@c.d",` + at + `}`},
		{"assignee nil must be an email address", `{"kind":"human","status":"delegated",` + at + `}`},
		{`mode 7 must be one of refine/research/implement`, `{"kind":"agent","mode":7,"status":"ready",` + at + `}`},
		{"mode nil must be one of", `{"kind":"agent","status":"ready",` + at + `}`},
		{"assignee is not allowed while ready", `{"kind":"agent","mode":"refine","status":"ready","assignee":"w1",` + at + `}`},
		{"assignee nil must be a worker id", `{"kind":"agent","mode":"refine","status":"claimed",` + at + `}`},
		{`assignee "w 1" must be a worker id`, `{"kind":"agent","mode":"refine","status":"claimed","assignee":"w 1",` + at + `}`},
		{"must be a worker id", `{"kind":"agent","mode":"refine","status":"claimed","assignee":"` + strings.Repeat("w", 201) + `",` + at + `}`},
		{`status "delegated" must be ready or claimed`, `{"kind":"agent","mode":"refine","status":"delegated",` + at + `}`},
		{"at nil is not a UTC timestamp", `{"kind":"agent","mode":"refine","status":"ready"}`},
		{`at "2026-07-27T18:04:11+02:00" is not a UTC timestamp`, `{"kind":"agent","mode":"refine","status":"ready","at":"2026-07-27T18:04:11+02:00"}`},
		{`assignee "@work" must be an email address`, `{"kind":"human","status":"delegated","assignee":"@work",` + at + `}`},
		{`assignee "@" must be an email address`, `{"kind":"human","status":"delegated","assignee":"@",` + at + `}`},
		{`assignee "pat@example" must be an email address`, `{"kind":"human","status":"delegated","assignee":"pat@example",` + at + `}`},
		{`\e[2K\e[1Aagent-ready" must be a worker id`, `{"kind":"agent","mode":"refine","status":"claimed","assignee":"\u001b[2K\u001b[1Aagent-ready",` + at + `}`},
		{"work_ref must be a non-empty string", `{"kind":"agent","mode":"research","status":"ready",` + at + `,"work_ref":"   "}`},
		{"work_ref must be a single line", `{"kind":"agent","mode":"research","status":"ready",` + at + `,"work_ref":"a\nb"}`},
		{"must be a single line", `{"kind":"agent","mode":"research","status":"ready",` + at + `,"work_ref":"a b"}`},
		{"work_ref must not contain control characters", `{"kind":"agent","mode":"research","status":"ready",` + at + `,"work_ref":"https://example.com/\u001b[2Kx"}`},
		{"work_ref must be at most 500 characters", `{"kind":"agent","mode":"research","status":"ready",` + at + `,"work_ref":"` + strings.Repeat("x", 501) + `"}`},
	} {
		t.Run(tc.want, func(t *testing.T) {
			result := CheckText(delegatedTask(tc.delegation, "NEXT"))
			if result.OK() {
				t.Fatalf("%s was accepted", tc.delegation)
			}
			if got := messages(result); !strings.Contains(got, tc.want) {
				t.Fatalf("errors = %q, want one containing %q", got, tc.want)
			}
		})
	}
}

// test_hand_edited_empty_delegation_is_an_error. The writer omits an empty
// object, so this only reaches a file by hand — and the schema has no meaning
// for it.
func TestHandEditedEmptyDelegationIsAnError(t *testing.T) {
	if got := messages(CheckText(delegatedTask("{}", "NEXT"))); !strings.Contains(got, "delegation must not be empty") {
		t.Fatalf("errors = %q", got)
	}
}

// test_null_delegation_is_treated_as_absent — the next write drops the key.
func TestNullDelegationIsTreatedAsAbsent(t *testing.T) {
	if result := CheckText(delegatedTask("null", "NEXT")); !result.OK() {
		t.Fatalf("errors = %#v, want a null marker treated as absence", result.Errors)
	}
}

// test_unknown_delegation_keys_do_not_fail_the_file. Forward compatibility one
// level down: treating a newer binary's field as an error failed `check`, made
// merge refuse, and — because the post-write check validates every line —
// blocked every write store-wide until a patch happened to land on that record.
func TestUnknownDelegationKeysWarnRatherThanFailTheFile(t *testing.T) {
	delegation := `{"kind":"agent","mode":"refine","status":"ready","at":"2026-07-27T18:04:11Z","hint":"x","lease":1}`
	result := CheckText(delegatedTask(delegation, "NEXT"))
	if !result.OK() {
		t.Fatalf("errors = %#v, want unknown nested keys tolerated", result.Errors)
	}
	got := warningMessages(result)
	want := []string{`unknown delegation key "hint"`, `unknown delegation key "lease"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v in the record's own member order", got, want)
	}
}

// test_proposed_task_cannot_carry_a_delegation: approval and delegation are
// independent owner decisions, so an undecided proposal never carries one.
func TestProposedTaskCannotCarryADelegation(t *testing.T) {
	got := messages(CheckText(delegatedTask(validDelegation, "PROPOSED")))
	if !strings.Contains(got, "delegation on a proposed task (PROPOSED)") {
		t.Fatalf("errors = %q", got)
	}
}

// Recurrence is a schedule for accepted work, and every write path already
// refuses to put a cookie on a proposal. The rule exists for the files no write
// produced — hand-edited, repaired, or written by another device — where the
// shape would otherwise pass `check` and then meet an operation that cannot act
// on it: completing a recurring task rolls its anchor forward instead of
// finishing it.
func TestProposedTaskCannotCarryRecurrence(t *testing.T) {
	text := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "PROPOSED", "a proposal", `"scheduled":"2026-06-01"`, `"recur":"+1w"`),
	)
	if got := messages(CheckText(text)); !strings.Contains(got, "recurrence on a proposed task (PROPOSED)") {
		t.Fatalf("errors = %q", got)
	}
	accepted := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "TODO", "accepted work", `"scheduled":"2026-06-01"`, `"recur":"+1w"`),
	)
	if result := CheckText(accepted); !result.OK() {
		t.Fatalf("recurrence on accepted work must pass: %#v", result.Errors)
	}
}

// test_closed_task_may_carry_a_delegation_as_provenance
func TestClosedTaskMayCarryADelegationAsProvenance(t *testing.T) {
	delegation := `{"kind":"agent","mode":"implement","status":"claimed","assignee":"cc/fable5/aaaa1111","at":"2026-07-27T18:04:11Z","work_ref":"https://example.com/pr/42"}`
	text := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "DONE", "a task", `"closed":"2026-07-27"`, `"delegation":`+delegation),
	)
	if result := CheckText(text); !result.OK() {
		t.Fatalf("errors = %#v, want a closed task to keep its provenance", result.Errors)
	}
}

// -- title and duplicate hazards ---------------------------------------------

// test_duplicate_done_titles_do_not_warn: the hazard is ambiguity for a fuzzy
// ref, and a closed task cannot be named by one.
func TestDuplicateClosedTitlesDoNotWarn(t *testing.T) {
	text := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "DONE", "pay the bill", `"closed":"2026-06-01"`),
		task("aaaa0003", "aaaa0001", "DONE", "pay the bill", `"closed":"2026-06-08"`),
	)
	result := CheckText(text)
	if !result.OK() || len(result.Warnings) != 0 {
		t.Fatalf("errors = %#v, warnings = %#v, want silence", result.Errors, result.Warnings)
	}
}

// The comparison is case-insensitive, so two spellings of one title are still
// the ambiguity a user would hit.
func TestDuplicateOpenTitlesFoldCase(t *testing.T) {
	text := store(
		section("aaaa0001", "W"),
		task("aaaa0002", "aaaa0001", "TODO", "Pay The Bill"),
		task("aaaa0003", "aaaa0001", "NEXT", "pay the bill"),
	)
	result := CheckText(text)
	if !result.OK() {
		t.Fatalf("errors = %#v, want a warning rather than an error", result.Errors)
	}
	want := []string{`duplicate open title "pay the bill" (lines 3, 4) — fuzzy refs will be ambiguous`}
	if got := warningMessages(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v", got, want)
	}
}

// -- the store pair ----------------------------------------------------------

// test_check_store_rejects_id_shared_by_live_and_archive: each file can be
// valid alone while a concurrent archive-vs-edit merge leaves one task in both.
func TestCheckStoreRejectsIDSharedByLiveAndArchive(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tasks.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")
	writeFile(t, live, store(section("aaaa0001", "Work"), task("aaaa0002", "aaaa0001", "NEXT", "Edited live")))
	writeFile(t, archive, store(
		section("bbbb0001", "Archive"),
		task("aaaa0002", "bbbb0001", "DONE", "Archived copy", `"closed":"2026-07-16"`),
	))
	want := []Entry{{3, `id "aaaa0002" appears in both tasks.jsonl line 3 and archive.jsonl line 3`}}
	if got := CheckStore(live, archive).Errors; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
}

// test_check_store_allows_missing_archive_and_disjoint_ids. A store with no
// archive yet is the ordinary case, not a broken one.
func TestCheckStoreAllowsMissingArchiveAndDisjointIDs(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tasks.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")
	writeFile(t, live, store(section("aaaa0001", "Work")))
	if result := CheckStore(live, archive); !result.OK() {
		t.Fatalf("errors = %#v, want a missing archive to be fine", result.Errors)
	}
	writeFile(t, archive, store(section("bbbb0001", "Archive")))
	if result := CheckStore(live, archive); !result.OK() {
		t.Fatalf("errors = %#v, want disjoint ids to be fine", result.Errors)
	}
}

// Both files' diagnostics are attributed and interleaved by line, errors and
// warnings alike — one store's report, not two files' concatenated.
func TestCheckStoreAnnotatesAndOrdersBothFiles(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tasks.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")
	writeFile(t, live, store(
		section("aaaa0001", "Work"),
		task("aaaa0002", "aaaa0001", "NEXT", "live", `"colour":"blue"`),
	))
	writeFile(t, archive, store(section("bbbb0001", "Archive", `"energy":"low"`)))

	result := CheckStore(live, archive)
	want := []string{`archive.jsonl: unknown key "energy"`, `tasks.jsonl: unknown key "colour"`}
	got := warningMessages(result)
	if len(got) != 2 {
		t.Fatalf("warnings = %#v, want two", got)
	}
	// Line 2 (the archive's section) sorts before line 3 (the live task).
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %#v, want %#v ordered by line", got, want)
	}
}

// A cross-file duplicate report is stable across runs. Go map iteration is
// randomized, so several shared ids would otherwise shuffle between runs and a
// diff of two check outputs would show noise.
func TestCrossFileDuplicateReportIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "tasks.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")
	writeFile(t, live, store(
		section("aaaa0001", "Work"),
		task("cccc0003", "aaaa0001", "NEXT", "three"),
		task("bbbb0002", "aaaa0001", "NEXT", "two"),
		task("aaaa0004", "aaaa0001", "NEXT", "four"),
	))
	writeFile(t, archive, store(
		section("dddd0001", "Archive"),
		task("cccc0003", "dddd0001", "DONE", "three", `"closed":"2026-07-16"`),
		task("bbbb0002", "dddd0001", "DONE", "two", `"closed":"2026-07-16"`),
		task("aaaa0004", "dddd0001", "DONE", "four", `"closed":"2026-07-16"`),
	))
	first := CheckStore(live, archive).Errors
	if len(first) != 3 {
		t.Fatalf("errors = %#v, want three duplicate ids", first)
	}
	for attempt := 0; attempt < 20; attempt++ {
		if got := CheckStore(live, archive).Errors; !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %#v, want %#v — the report must not depend on map order", attempt, got, first)
		}
	}
	// The report is ordered by line, as every check report is. Generation is
	// ordered by id, which is what makes the answer stable when two diagnostics
	// land on one line and the sort that follows is stable.
	for index := 1; index < len(first); index++ {
		if first[index-1].Line > first[index].Line {
			t.Fatalf("errors = %#v, want them ordered by line", first)
		}
	}
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
