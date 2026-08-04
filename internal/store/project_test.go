package store

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/check"
)

// PROJECTS_FIXTURE from test/test_helper.rb: an Inbox (never an area), a
// top-level "Projects" heading whose child sections are projects — one with a
// body, a recurring NEXT, a dated TODO, a nested sub-section proving depth, and
// a deferred TODO — a stuck project, an empty one, an area, and a done-only
// section. DFS pre-order throughout.
const projectsFixture = `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Inbox"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"INBOX","title":"unfiled capture"}
{"type":"section","id":"cccc0003","title":"Projects"}
{"type":"section","id":"cccc0004","parent":"cccc0003","title":"Site launch","body":"Goal: ship the personal site."}
{"type":"task","id":"cccc0005","parent":"cccc0004","state":"NEXT","title":"Pick a static-site generator","recur":"+1w"}
{"type":"task","id":"cccc0006","parent":"cccc0004","state":"TODO","title":"Write the landing copy","deadline":"2026-07-25"}
{"type":"section","id":"cccc0007","parent":"cccc0004","title":"Copywriting"}
{"type":"task","id":"cccc0008","parent":"cccc0007","state":"TODO","title":"Draft the about page"}
{"type":"task","id":"cccc0009","parent":"cccc0004","state":"TODO","title":"Someday: custom domain","tags":["defer"]}
{"type":"section","id":"cccc000a","parent":"cccc0003","title":"Stuck reno"}
{"type":"task","id":"cccc000b","parent":"cccc000a","state":"TODO","title":"Measure the kitchen"}
{"type":"section","id":"cccc000c","parent":"cccc0003","title":"Empty project"}
{"type":"section","id":"cccc000d","title":"Tasks"}
{"type":"task","id":"cccc000e","parent":"cccc000d","state":"NEXT","title":"Reply to the vendor"}
{"type":"task","id":"cccc000f","parent":"cccc000d","state":"TODO","title":"File expenses"}
{"type":"section","id":"cccc0010","title":"Done pile"}
{"type":"task","id":"cccc0011","parent":"cccc0010","state":"DONE","title":"Old finished chore","closed":"2026-07-01"}
`

// recordTitled is the parsed record with a given title, or a zero record.
func recordTitled(t *testing.T, path, title string) (map[string]string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, parsed := range recordsOf(raw) {
		if parsed["title"] == title {
			return parsed, true
		}
	}
	return nil, false
}

func recordsOf(raw []byte) []map[string]string {
	out := []map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		flat := map[string]string{"__raw": line}
		for _, key := range []string{"id", "type", "title", "parent", "state", "closed", "archived", "recur"} {
			marker := `"` + key + `":"`
			index := strings.Index(line, marker)
			if index < 0 {
				continue
			}
			rest := line[index+len(marker):]
			end := strings.Index(rest, `"`)
			if end < 0 {
				continue
			}
			flat[key] = rest[:end]
		}
		out = append(out, flat)
	}
	return out
}

// -- rename_section! -------------------------------------------------------

// test_rename_section_retitles_and_round_trips_through_undo.
func TestRenameSectionRetitlesAndRoundTripsThroughUndo(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	id, ok := target.RenameSection("cccc0004", "  Site launch v2  ")
	if !ok || id != "cccc0004" {
		t.Fatalf("rename = %q, %v", id, ok)
	}
	if _, found := recordTitled(t, target.org, "Site launch v2"); !found {
		t.Error("the title was not trimmed and written")
	}
	if _, found := recordTitled(t, target.org, "Site launch"); found {
		t.Error("the old title survived")
	}
	assertChecked(t, target)

	status, label := target.HistoryStep(-1)
	if status != HistoryOK || label != "rename section: Site launch v2" {
		t.Fatalf("undo = %v %q", status, label)
	}
	if _, found := recordTitled(t, target.org, "Site launch"); !found {
		t.Error("undo did not restore the title")
	}
	assertChecked(t, target)
}

// test_rename_section_rejects_blank_titles_and_missing_ids: a refusal writes
// nothing and burns no history.
func TestRenameSectionRejectsBlankTitlesAndMissingIDs(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	if _, ok := target.RenameSection("cccc0004", "   "); ok {
		t.Error("a blank title was accepted")
	}
	if _, ok := target.RenameSection("ffffffff", "Ghost"); ok {
		t.Error("a missing section was accepted")
	}
	// A TASK id must miss too: renaming addresses sections only.
	if _, ok := target.RenameSection("cccc0005", "Not a section"); ok {
		t.Error("a task id was accepted as a section")
	}
	if _, found := recordTitled(t, target.org, "Site launch"); !found {
		t.Error("a refusal changed the file")
	}
	if status, _ := target.HistoryStep(-1); status != HistoryEmpty {
		t.Error("a refusal burned a history slot")
	}
}

// -- complete_project! -----------------------------------------------------

// test_complete_project_closes_open_descendants_dropping_defer_and_recur.
func TestCompleteProjectClosesOpenDescendantsDroppingDeferAndRecur(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	closed, found := target.CompleteProject("cccc0004", "2026-07-20")
	if !found {
		t.Fatal("the project was not found")
	}
	if closed != 4 {
		t.Fatalf("closed = %d, want 4 — NEXT, TODO, the nested task, and the deferred one", closed)
	}

	recurring, _ := recordTitled(t, target.org, "Pick a static-site generator")
	if recurring["state"] != "DONE" || recurring["closed"] != "2026-07-20" {
		t.Errorf("recurring task = %v", recurring)
	}
	if strings.Contains(recurring["__raw"], `"recur"`) {
		t.Error("a cascaded recurring task was advanced rather than retired")
	}
	deferred, _ := recordTitled(t, target.org, "Someday: custom domain")
	if deferred["state"] != "DONE" || deferred["closed"] != "2026-07-20" {
		t.Errorf("deferred task = %v", deferred)
	}
	if strings.Contains(deferred["__raw"], `"defer"`) {
		t.Error("the defer tag survived a close")
	}
	assertChecked(t, target)

	status, label := target.HistoryStep(-1)
	if status != HistoryOK || label != "complete project: cccc0004" {
		t.Fatalf("undo = %v %q", status, label)
	}
	restored, _ := recordTitled(t, target.org, "Pick a static-site generator")
	if restored["state"] != "NEXT" {
		t.Errorf("undo left state %q", restored["state"])
	}
	assertChecked(t, target)
}

// test_complete_project_is_a_clean_zero_when_nothing_is_open: closing nothing
// records no history, which is what lets a caller distinguish it from a
// rollback that also reported zero.
func TestCompleteProjectIsACleanZeroWhenNothingIsOpen(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	closed, found := target.CompleteProject("cccc000c", "2026-07-20")
	if !found || closed != 0 {
		t.Fatalf("closed = %d, found = %v", closed, found)
	}
	if status, _ := target.HistoryStep(-1); status != HistoryEmpty {
		t.Error("closing nothing recorded history")
	}
	if reason, _ := target.LastRollback(); reason != "" {
		t.Errorf("a clean zero recorded a rollback: %q", reason)
	}
	assertChecked(t, target)
}

// test_complete_project_reports_a_missing_section.
func TestCompleteProjectReportsAMissingSection(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)
	if _, found := target.CompleteProject("ffffffff", "2026-07-20"); found {
		t.Error("a missing section reported found")
	}
}

// -- archive_project! ------------------------------------------------------

// test_archive_project_moves_the_subtree_and_undo_deletes_a_fresh_archive.
func TestArchiveProjectMovesTheSubtreeAndUndoDeletesAFreshArchive(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)
	if _, err := os.Stat(target.archive); err == nil {
		t.Fatal("the archive existed before the sweep")
	}

	moved, proposed, found := target.ArchiveProject("cccc0004", "2026-07-20")
	if !found || proposed {
		t.Fatalf("archive = %v %v", found, proposed)
	}
	assertIDs(t, moved,
		[]string{"cccc0004", "cccc0005", "cccc0006", "cccc0007", "cccc0008", "cccc0009"}, "moved")

	if _, found := recordTitled(t, target.org, "Site launch"); found {
		t.Error("the subtree was not swept out of the live file")
	}
	root, found := recordTitled(t, target.archive, "Site launch")
	if !found {
		t.Fatal("the subtree did not reach the archive")
	}
	if strings.Contains(root["__raw"], `"parent"`) {
		t.Error("a swept section root kept its parent")
	}
	if root["archived"] == "" {
		t.Error("a swept section root has no archived stamp")
	}
	// An open task moves too: blocking is caller policy, the store is mechanical.
	open, _ := recordTitled(t, target.archive, "Pick a static-site generator")
	if open["state"] != "NEXT" {
		t.Errorf("archived task state = %q, want NEXT", open["state"])
	}
	assertChecked(t, target)
	if result := check.Check(target.archive); !result.OK() {
		t.Errorf("the archive failed validation: %v", result.Errors)
	}

	status, label := target.HistoryStep(-1)
	if status != HistoryOK || label != "archive project: cccc0004" {
		t.Fatalf("undo = %v %q", status, label)
	}
	if _, err := os.Stat(target.archive); err == nil {
		t.Error("undo did not remove the archive file it created")
	}
	if _, found := recordTitled(t, target.org, "Site launch"); !found {
		t.Error("undo did not restore the subtree")
	}
	assertChecked(t, target)
}

// test_archive_project_reports_a_missing_section: and it must not create an
// archive file on the way to saying no.
func TestArchiveProjectReportsAMissingSection(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)
	if _, _, found := target.ArchiveProject("ffffffff", "2026-07-20"); found {
		t.Error("a missing section reported found")
	}
	if _, err := os.Stat(target.archive); err == nil {
		t.Error("a refusal created an archive file")
	}
}

// An undecided proposal is never archival material: a proposal archived without
// a decision is a decision nobody made.
func TestArchiveProjectRefusesUndecidedProposals(t *testing.T) {
	const tree = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Projects"}
{"type":"section","id":"dddd0002","parent":"dddd0001","title":"Held"}
{"type":"task","id":"dddd0003","parent":"dddd0002","state":"PROPOSED","title":"Undecided"}
`
	target, _ := writerFixture(t, tree)
	before := readStore(t, target)

	moved, proposed, found := target.ArchiveProject("dddd0002", "2026-07-20")
	if !proposed || found || moved != nil {
		t.Fatalf("archive = %v %v %v", moved, proposed, found)
	}
	if readStore(t, target) != before {
		t.Error("a refused sweep wrote bytes")
	}
	if _, err := os.Stat(target.archive); err == nil {
		t.Error("a refused sweep created an archive file")
	}
}

// -- create_section! -------------------------------------------------------

// test_create_section_appends_a_top_level_list_at_end_of_file.
func TestCreateSectionAppendsATopLevelListAtEndOfFile(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	id := target.CreateSection("  Reading  ", "")
	if id == "" {
		t.Fatal("no section was created")
	}
	records := recordsOf([]byte(readStore(t, target)))
	last := records[len(records)-1]
	if last["id"] != id || last["type"] != "section" || last["title"] != "Reading" {
		t.Errorf("last record = %v", last)
	}
	if strings.Contains(last["__raw"], `"parent"`) {
		t.Error("a top-level list carries no parent")
	}
	assertChecked(t, target)

	status, label := target.HistoryStep(-1)
	if status != HistoryOK || label != "create section: Reading" {
		t.Fatalf("undo = %v %q", status, label)
	}
	if _, found := recordTitled(t, target.org, "Reading"); found {
		t.Error("undo did not remove the section")
	}
	assertChecked(t, target)
}

// test_create_section_inserts_as_last_child_past_a_nested_subtree: the whole
// destination subtree — including a nested sub-section and its task — precedes
// the new last child, which is what keeps DFS pre-order valid.
func TestCreateSectionInsertsAsLastChildPastANestedSubtree(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	id := target.CreateSection("Launch assets", "cccc0004")
	if id == "" {
		t.Fatal("no section was created")
	}
	records := recordsOf([]byte(readStore(t, target)))
	position := map[string]int{}
	for index, parsed := range records {
		position[parsed["id"]] = index
	}
	if records[position[id]]["parent"] != "cccc0004" {
		t.Errorf("parent = %q", records[position[id]]["parent"])
	}
	for _, existing := range []string{"cccc0004", "cccc0005", "cccc0006", "cccc0007",
		"cccc0008", "cccc0009"} {
		if position[id] <= position[existing] {
			t.Errorf("the new child precedes %s", existing)
		}
	}
	assertChecked(t, target)
}

// test_create_section_rejects_a_blank_title_and_a_missing_parent.
func TestCreateSectionRejectsABlankTitleAndAMissingParent(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	if id := target.CreateSection("   ", ""); id != "" {
		t.Error("a blank title was accepted")
	}
	if id := target.CreateSection("Orphan", "ffffffff"); id != "" {
		t.Error("a missing parent was accepted")
	}
	if _, found := recordTitled(t, target.org, "Orphan"); found {
		t.Error("a refusal wrote a section")
	}
	if status, _ := target.HistoryStep(-1); status != HistoryEmpty {
		t.Error("a refusal burned a history slot")
	}
	assertChecked(t, target)
}

// -- create_project! -------------------------------------------------------

// test_application_create_project_files_under_the_existing_root.
func TestCreateProjectFilesUnderTheExistingRoot(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)

	result := target.CreateProject("Reviews")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if result.Summary.CreatedRoot {
		t.Error("a root already existed and was reported as created")
	}
	if result.Summary.RootID != "cccc0003" {
		t.Errorf("root_id = %q, want cccc0003", result.Summary.RootID)
	}
	assertIDs(t, result.TouchedIDs, []string{result.Summary.CreatedID}, "touched")

	created, found := recordTitled(t, target.org, "Reviews")
	if !found || created["parent"] != "cccc0003" {
		t.Errorf("created = %v", created)
	}
	assertChecked(t, target)
}

// test_application_create_project_bootstraps_a_missing_root: one write, so one
// undo removes BOTH the project and the root it bootstrapped.
func TestCreateProjectBootstrapsAMissingRootInOneWrite(t *testing.T) {
	const tree = `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Inbox"}
`
	target, _ := writerFixture(t, tree)
	original := readStore(t, target)

	result := target.CreateProject("Reviews")
	if result.Status != MutationOK {
		t.Fatalf("status = %q, errors = %v", result.Status, result.Errors)
	}
	if !result.Summary.CreatedRoot {
		t.Error("no root existed, so one should have been created")
	}
	root, found := recordTitled(t, target.org, "Projects")
	if !found || strings.Contains(root["__raw"], `"parent"`) {
		t.Errorf("the auto-created root = %v", root)
	}
	project, _ := recordTitled(t, target.org, "Reviews")
	if project["parent"] != root["id"] {
		t.Errorf("project parent = %q, want %q", project["parent"], root["id"])
	}
	assertIDs(t, result.TouchedIDs, []string{result.Summary.CreatedID, root["id"]},
		"touched carries the new project and the auto-created root")
	assertChecked(t, target)

	if status, _ := target.HistoryStep(-1); status != HistoryOK {
		t.Fatal("undo refused")
	}
	if readStore(t, target) != original {
		t.Error("one undo did not remove both the project and the bootstrapped root")
	}
}

// test_application_create_project_rejects_blank_and_duplicate_titles: the
// duplicate check spans the root's whole child list, projects and areas alike,
// because those titles are the project-ref candidate set.
func TestCreateProjectRejectsBlankAndDuplicateTitles(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)
	original := readStore(t, target)

	for _, title := range []string{"   ", "site launch", "Empty project", "STUCK RENO"} {
		result := target.CreateProject(title)
		if result.Status != MutationInvalid {
			t.Errorf("%q: status = %q, want invalid", title, result.Status)
		}
		if len(result.FieldErrors["title"]) != 1 {
			t.Errorf("%q: field errors = %v", title, result.FieldErrors)
		}
	}
	if readStore(t, target) != original {
		t.Error("a rejected create wrote bytes")
	}
}

// test_project_mutations_map_post_write_rollbacks_to_store_invalid.
//
// These four report failure through a bare boolean or count, so the recorded
// rollback is the ONLY evidence that anything was written at all. The archive
// is broken deliberately: post_write_failure validates BOTH files, so a live
// write that is itself fine still rolls back — which is exactly the case where
// "false" and "nothing to do" would otherwise be indistinguishable.
func TestProjectMutationsRecordAPostWriteRollback(t *testing.T) {
	for _, testCase := range []struct {
		name string
		run  func(*Store) bool
	}{
		{"create_section", func(s *Store) bool { return s.CreateSection("Reading", "") != "" }},
		{"rename_section", func(s *Store) bool {
			_, ok := s.RenameSection("cccc0004", "Renamed")
			return ok
		}},
		{"complete_project", func(s *Store) bool {
			_, ok := s.CompleteProject("cccc0004", "2026-07-20")
			return ok
		}},
		// archive_project is deliberately absent: it REWRITES the archive from
		// its own parse, so a broken archive line is dropped rather than tripping
		// the post-write check. Ruby's sweep behaves identically — Format.parse
		// returns the valid records and collects the errors separately — so this
		// is the sweep's shape, not a rollback path.
	} {
		target, _ := writerFixture(t, projectsFixture)
		original := readStore(t, target)
		if err := os.WriteFile(target.archive, []byte("{\"type\":\"meta\",\"version\":2}\nnot json\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if testCase.run(target) {
			t.Errorf("%s: reported success over an invalid store", testCase.name)
		}
		if readStore(t, target) != original {
			t.Errorf("%s: the rollback did not restore the live bytes", testCase.name)
		}
		reason, stage := target.LastRollback()
		if reason == "" || stage != RollbackValidation {
			t.Errorf("%s: rollback = %q / %q, want a validation rollback",
				testCase.name, reason, stage)
		}
		if status, _ := target.HistoryStep(-1); status != HistoryEmpty {
			t.Errorf("%s: a rolled-back write recorded history", testCase.name)
		}
	}
}

// -- section_named ---------------------------------------------------------

// Store#section_named resolves in capture's widening tiers, which is what lets
// `move <ref> "Copywriting"` reach a nested project sub-section by name.
func TestSectionNamedResolvesInWideningTiers(t *testing.T) {
	target, _ := writerFixture(t, projectsFixture)
	for _, testCase := range []struct{ name, want string }{
		{"Inbox", "cccc0001"},       // exact, top level
		{"Copywriting", "cccc0007"}, // exact, any level
		{"Done", "cccc0010"},        // substring, top level
		{"Stuck", "cccc000a"},       // substring, any level
	} {
		found, ok := target.SectionNamed(testCase.name)
		if !ok || found.String("id") != testCase.want {
			t.Errorf("%q = %q (%v), want %q", testCase.name, found.String("id"), ok, testCase.want)
		}
	}
	if _, ok := target.SectionNamed("nothing like this"); ok {
		t.Error("a name that matches nothing resolved")
	}
}

// -- ensure_id -------------------------------------------------------------

// EnsureID mints an id for a legacy record that has none, and is idempotent for
// one that does — no write, no undo slot.
func TestEnsureIDMintsOnlyForARecordWithoutOne(t *testing.T) {
	const tree = `{"type":"meta","version":2}
{"type":"section","id":"1e000001","title":"Next Actions"}
{"type":"task","parent":"1e000001","state":"NEXT","title":"Hand-appended"}
`
	target, _ := writerFixture(t, tree)

	assigned, ok := target.EnsureID(3, "", "Hand-appended")
	if !ok || assigned == "" {
		t.Fatalf("mint = %q %v", assigned, ok)
	}
	if !strings.Contains(readStore(t, target), `"id":"`+assigned+`"`) {
		t.Error("the minted id did not reach the file")
	}
	assertChecked(t, target)

	before := readStore(t, target)
	repeat, ok := target.EnsureID(3, assigned, "Hand-appended")
	if !ok || repeat != assigned {
		t.Errorf("repeat = %q %v", repeat, ok)
	}
	if readStore(t, target) != before {
		t.Error("an idempotent repeat wrote bytes")
	}
}
