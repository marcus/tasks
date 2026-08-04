package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

// The read commands are exercised as BLACK BOXES: argv in, stdout/stderr/exit
// out. That is the contract a user and an agent actually hold, and it is the
// only level at which "byte-identical to Ruby" can be asserted at all — a test
// against the read model would pass happily while the adapter printed the
// fields in the wrong order.

type cliResult struct {
	stdout string
	stderr string
	status int
}

// runCLI runs one invocation against a temporary store. The process environment
// is replaced wholesale rather than added to, so a developer's real TASKS_FILE
// or config can never reach a test.
func runCLI(t *testing.T, dir string, argv ...string) cliResult {
	t.Helper()
	previousEnv := env
	env = determinism.Env{
		"TASKS_FILE":      filepath.Join(dir, "tasks.jsonl"),
		"TASKS_ARCHIVE":   filepath.Join(dir, "archive.jsonl"),
		"XDG_STATE_HOME":  filepath.Join(dir, "state"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "cfg"),
		"TASKS_NOW":       "2026-07-20T12:00:00Z",
		"TZ":              "UTC",
	}
	defer func() { env = previousEnv }()

	stdout, stderr := captureOutput(t, func() int { return run(argv) })
	return cliResult{stdout: stdout.text, stderr: stderr.text, status: stdout.status}
}

type capture struct {
	text   string
	status int
}

func captureOutput(t *testing.T, body func() int) (capture, capture) {
	t.Helper()
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite

	outText := make(chan string, 1)
	errText := make(chan string, 1)
	go func() { data, _ := io.ReadAll(outRead); outText <- string(data) }()
	go func() { data, _ := io.ReadAll(errRead); errText <- string(data) }()

	status := body()

	os.Stdout, os.Stderr = previousOut, previousErr
	outWrite.Close()
	errWrite.Close()
	return capture{text: <-outText, status: status}, capture{text: <-errText}
}

// seedStore writes a fixture and returns its directory.
func seedStore(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dir
}

func seedConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "cfg", "tasks")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "config"), []byte(content), 0o644); err != nil {
		t.Fatalf("config: %v", err)
	}
}

// projectsFixture is test/test_helper.rb's PROJECTS_FIXTURE, record for record:
// an Inbox (excluded from areas), a "Projects" heading whose child sections are
// projects — "Site launch" (a body note, a recurring NEXT, a TODO with a
// deadline, a nested "Copywriting" sub-section proving depth rollup, and a
// deferred TODO proving deferral exclusion), "Stuck reno", and an empty project
// — plus a "Tasks" area and a "Done pile" whose only task is DONE.
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

// nestedFixture is test/test_tree.rb's NESTED: a section with a task carrying
// body links, a task with a subtask, and a second section.
const nestedFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Work"}
{"type":"task","id":"aaaa1111","parent":"aaaa0001","state":"NEXT","priority":"A","title":"Fix billing outage","tags":["@computer"],"deadline":"2026-07-10","body":"Context in [[https://acme.slack.com/archives/C042/p171][the incident thread]].\nTicket: https://acme.atlassian.net/browse/OPS-1234."}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"NEXT","title":"Review Q3 planning doc","body":"https://docs.google.com/document/d/abc/edit"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"TODO","title":"Leave comments for Dana","body":"Dana prefers suggestions mode."}
{"type":"section","id":"aaaa0004","title":"Home"}
{"type":"task","id":"aaaa0005","parent":"aaaa0004","state":"TODO","title":"Renew passport","body":"Photo specs: https://travel.state.gov/photos.html, then book."}
`

// -- projects ----------------------------------------------------------------

func TestProjectsTextListsProjectsThenAreasWithCountsAndStuck(t *testing.T) {
	dir := seedStore(t, projectsFixture)
	result := runCLI(t, dir, "projects")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	for _, want := range []string{
		"Projects", "Areas",
		"Site launch    3 open · 1 next · next 7/25",
		"Empty project  0 open · 0 next  (stuck)",
		"Stuck reno     1 open · 0 next  (stuck)",
		"Tasks          2 open · 1 next",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, result.stdout)
		}
	}
	// Projects sort ahead of areas; the dated project ahead of dateless ones.
	if strings.Index(result.stdout, "Site launch") > strings.Index(result.stdout, "Empty project") {
		t.Error("dated project must sort ahead of dateless ones")
	}
	if strings.Index(result.stdout, "Empty project") > strings.Index(result.stdout, "Stuck reno") {
		t.Error("dateless projects order by title")
	}
	if strings.Index(result.stdout, "Projects") > strings.Index(result.stdout, "Areas") {
		t.Error("projects group precedes areas")
	}
}

func TestProjectsJSONIsAnArrayOfProjectObjects(t *testing.T) {
	dir := seedStore(t, projectsFixture)
	result := runCLI(t, dir, "projects", "--json")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	wantIDs := []string{"cccc0004", "cccc000c", "cccc000a", "cccc000d"}
	wantKinds := []string{"project", "project", "project", "area"}
	if len(rows) != len(wantIDs) {
		t.Fatalf("%d rows, want %d", len(rows), len(wantIDs))
	}
	for index := range rows {
		if rows[index]["id"] != wantIDs[index] {
			t.Errorf("row %d id = %v, want %s", index, rows[index]["id"], wantIDs[index])
		}
		if rows[index]["kind"] != wantKinds[index] {
			t.Errorf("row %d kind = %v, want %s", index, rows[index]["kind"], wantKinds[index])
		}
	}
	site := rows[0]
	if site["open_count"] != float64(3) {
		t.Errorf("open_count = %v", site["open_count"])
	}
	if site["next_date"] != "2026-07-25" {
		t.Errorf("next_date = %v", site["next_date"])
	}
	if site["held_count"] != float64(1) {
		t.Errorf("held_count = %v, want 1 (the deferred TODO)", site["held_count"])
	}
	taskIDs, _ := json.Marshal(site["task_ids"])
	if string(taskIDs) != `["cccc0005","cccc0006","cccc0008"]` {
		t.Errorf("task_ids = %s", taskIDs)
	}
	// nil-valued keys are OMITTED: an area has no parent_id, next_date or body.
	area := rows[3]
	for _, absent := range []string{"parent_id", "next_date", "next_time", "next_at", "body"} {
		if _, present := area[absent]; present {
			t.Errorf("area row should omit %q, got %v", absent, area[absent])
		}
	}
	if area["stuck"] != false || area["held_count"] != float64(0) {
		t.Errorf("false and 0 are answers, not absences: %v", area)
	}
}

func TestProjectsRejectsUnknownFlag(t *testing.T) {
	dir := seedStore(t, projectsFixture)
	result := runCLI(t, dir, "projects", "--bogus")
	if result.status != 1 {
		t.Fatalf("exit %d, want 1", result.status)
	}
	if !strings.Contains(result.stderr, "unknown flag: --bogus") {
		t.Fatalf("stderr = %q", result.stderr)
	}
}

func TestProjectsRefusesAnInvalidStore(t *testing.T) {
	// A rollup computed from whatever survived parsing is a wrong answer a
	// weekly review would act on, so the refusal is the whole behaviour.
	dir := seedStore(t, "{\"type\":\"meta\",\"version\":2}\n{not json}\n")
	result := runCLI(t, dir, "projects")
	if result.status != 1 {
		t.Fatalf("exit %d, want 1", result.status)
	}
	if !strings.Contains(result.stderr, "task file is invalid") {
		t.Fatalf("stderr = %q", result.stderr)
	}
}

func TestProjectsEmptyState(t *testing.T) {
	dir := seedStore(t, "{\"type\":\"meta\",\"version\":2}\n")
	result := runCLI(t, dir, "projects")
	if result.stdout != "No projects or areas.\n" {
		t.Fatalf("stdout = %q", result.stdout)
	}
}

// -- next / inbox / quadrants -------------------------------------------------

func TestNextGroupsByContextWithNoContextFirst(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"bbbb0001","title":"Work"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"Untagged action"}
{"type":"task","id":"bbbb0003","parent":"bbbb0001","state":"NEXT","title":"Call the vendor","tags":["@phone","@office"]}
{"type":"task","id":"bbbb0004","parent":"bbbb0001","state":"TODO","title":"Not a next action"}
`
	dir := seedStore(t, fixture)
	result := runCLI(t, dir, "next")
	want := "(no context)\n  Untagged action\n\n@office\n  Call the vendor  @phone @office\n\n" +
		"@phone\n  Call the vendor  @phone @office\n\n"
	if result.stdout != want {
		t.Fatalf("stdout =\n%q\nwant\n%q", result.stdout, want)
	}
}

func TestNextKeepsFileOrderForEqualPriorities(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"bbbb0001","title":"Work"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"first"}
{"type":"task","id":"bbbb0003","parent":"bbbb0001","state":"NEXT","priority":"A","title":"prioritized"}
{"type":"task","id":"bbbb0004","parent":"bbbb0001","state":"NEXT","title":"second"}
`
	dir := seedStore(t, fixture)
	result := runCLI(t, dir, "next", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	got := []string{}
	for _, row := range rows {
		got = append(got, row["title"].(string))
	}
	want := []string{"prioritized", "first", "second"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order = %v, want %v (priority first, then file order)", got, want)
		}
	}
}

func TestNextHidesUnavailableActions(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"bbbb0001","title":"Work"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"NEXT","title":"held","scheduled":"2027-01-01"}
`
	dir := seedStore(t, fixture)
	if result := runCLI(t, dir, "next"); result.stdout != "" {
		t.Fatalf("a task held behind a future start date is not a next action: %q", result.stdout)
	}
}

func TestInboxListsAndReportsEmpty(t *testing.T) {
	dir := seedStore(t, projectsFixture)
	result := runCLI(t, dir, "inbox")
	if result.stdout != "  unfiled capture\n" {
		t.Fatalf("stdout = %q", result.stdout)
	}
	empty := seedStore(t, "{\"type\":\"meta\",\"version\":2}\n")
	if got := runCLI(t, empty, "inbox").stdout; got != "Inbox empty. ✨\n" {
		t.Fatalf("empty state = %q", got)
	}
}

func TestQuadrantsPrintsEveryQuadrantIncludingEmptyOnes(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","priority":"A","title":"q1","deadline":"2026-07-21"}
{"type":"task","id":"dddd0003","parent":"dddd0001","state":"NEXT","priority":"A","title":"q2"}
{"type":"task","id":"dddd0004","parent":"dddd0001","state":"TODO","title":"q3","deadline":"2026-07-21"}
{"type":"task","id":"dddd0005","parent":"dddd0001","state":"TODO","title":"q4"}
`
	dir := seedStore(t, fixture)
	result := runCLI(t, dir, "quadrants")
	want := "Q1 · Important + Urgent  (do now)\n  [A] q1\n\n" +
		"Q2 · Important, Not Urgent  (schedule)\n  [A] q2\n\n" +
		"Q3 · Urgent, Not Important  (delegate)\n  q3\n\n" +
		"Q4 · Neither  (eliminate)\n  q4\n\n"
	if result.stdout != want {
		t.Fatalf("stdout =\n%q\nwant\n%q", result.stdout, want)
	}

	empty := seedStore(t, "{\"type\":\"meta\",\"version\":2}\n")
	if got := runCLI(t, empty, "quadrants").stdout; !strings.Contains(got, "  —\n") {
		t.Fatalf("an empty quadrant prints a dash, got %q", got)
	}
}

func TestQuadrantsJSONCarriesTheQuadrantOnTheStandardRow(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Work"}
{"type":"task","id":"dddd0002","parent":"dddd0001","state":"NEXT","priority":"A","title":"q1","deadline":"2026-07-21"}
`
	dir := seedStore(t, fixture)
	result := runCLI(t, dir, "quadrants", "--json")
	if !strings.Contains(result.stdout, `"delegation":null,"quadrant":"Q1"}`) {
		t.Fatalf("quadrant must be the LAST member of the standard row: %s", result.stdout)
	}
}

// -- show --------------------------------------------------------------------

func TestShowReportsProjectAndLinks(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "show", "billing outage")
	if result.status != 0 {
		t.Fatalf("exit %d, stderr %q", result.status, result.stderr)
	}
	for _, want := range []string{
		"NEXT [#A] Fix billing outage :@computer:",
		"  id:        aaaa1111",
		"  project:   Work",
		"  deadline:  2026-07-10",
		"  availability: available now",
		"  slack https://acme.slack.com/archives/C042/p171  (the incident thread)",
		"  jira  https://acme.atlassian.net/browse/OPS-1234",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, result.stdout)
		}
	}
}

func TestShowJSONIsTheTaskResource(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "show", "billing outage", "--json")
	var row map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &row); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if row["project"] != "Work" {
		t.Errorf("project = %v", row["project"])
	}
	links, _ := json.Marshal(row["links"])
	want := `[{"label":"the incident thread","system":"slack","url":"https://acme.slack.com/archives/C042/p171"},` +
		`{"label":null,"system":"jira","url":"https://acme.atlassian.net/browse/OPS-1234"}]`
	if string(links) != want {
		t.Errorf("links = %s", links)
	}
	// `project` appears exactly once: Ruby merges it onto the standard row in
	// place, so a second member would be a shape no consumer expects.
	if count := strings.Count(result.stdout, `"project":`); count != 1 {
		t.Errorf("project appears %d times", count)
	}
	notes, _ := json.Marshal(row["notes"])
	if !strings.Contains(string(notes), "incident thread") {
		t.Errorf("notes = %s", notes)
	}
}

func TestShowRefusesAMissingRefWithExitTwo(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "show", "nothing here")
	if result.status != refExit {
		t.Fatalf("exit %d, want %d", result.status, refExit)
	}
	if !strings.Contains(result.stderr, "no match: nothing here") {
		t.Fatalf("stderr = %q", result.stderr)
	}
}

func TestShowWithoutARefPrintsUsage(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "show")
	if result.status != 1 || !strings.Contains(result.stderr, "usage: tasks show <ref>") {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
}

func TestShowRendersLeadRecurAndAvailability(t *testing.T) {
	fixture := `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Work"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"TODO","title":"Renew the domain","deadline":"2026-11-01","lead":"3w","recur":"+1y"}
`
	dir := seedStore(t, fixture)
	result := runCLI(t, dir, "show", "Renew")
	for _, want := range []string{
		"  deadline:  2026-11-01",
		"  availability: unavailable until 2026-10-11",
		"  recur:     +1y (every year from the scheduled date)",
		"  lead:      3w (3 weeks)",
		"  opens:     2026-10-11 (Sun) — 3 weeks before 2026-11-01",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, result.stdout)
		}
	}
}

// -- links -------------------------------------------------------------------

func TestLinksListsAndFiltersBySystem(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "links")
	// The system column is padded to the widest system in the WHOLE listing, so
	// the URLs line up across tasks rather than per task.
	if !strings.Contains(result.stdout, "slack            https://acme.slack.com") ||
		!strings.Contains(result.stdout, "jira             https://acme.atlassian.net") ||
		!strings.Contains(result.stdout, "travel.state.gov https://travel.state.gov") {
		t.Fatalf("stdout =\n%s", result.stdout)
	}
	filtered := runCLI(t, dir, "links", "--system", "jira")
	if strings.Contains(filtered.stdout, "slack.com") {
		t.Fatalf("--system jira must exclude slack:\n%s", filtered.stdout)
	}
	// The filter is case-insensitive because the names it matches are lowercase.
	upper := runCLI(t, dir, "links", "--system", "JIRA")
	if !strings.Contains(upper.stdout, "atlassian") {
		t.Fatalf("--system JIRA found nothing:\n%s", upper.stdout)
	}
}

func TestLinksRejectsBadFlags(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	if result := runCLI(t, dir, "links", "-j"); result.status == 0 ||
		!strings.Contains(result.stderr, "unknown flag: -j") {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
	if result := runCLI(t, dir, "links", "--system", "--json"); result.status == 0 ||
		!strings.Contains(result.stderr, "--system needs a value") {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
}

func TestLinksJSONAndSingleRef(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "links", "billing outage", "--json")
	var document struct {
		Links []struct {
			System string `json:"system"`
			Task   string `json:"task"`
			ID     string `json:"id"`
			Line   int    `json:"line"`
			Source string `json:"source"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &document); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if len(document.Links) != 2 {
		t.Fatalf("%d links, want 2", len(document.Links))
	}
	for _, link := range document.Links {
		if !strings.Contains(link.Task, "billing") {
			t.Errorf("a ref scopes to one task, got %q", link.Task)
		}
		if link.Source != "live" || link.ID != "aaaa1111" {
			t.Errorf("row = %+v", link)
		}
	}
}

func TestLinksEmptyMessage(t *testing.T) {
	dir := seedStore(t, "{\"type\":\"meta\",\"version\":2}\n"+
		"{\"type\":\"task\",\"id\":\"aaaa0002\",\"state\":\"TODO\",\"title\":\"nothing linked\"}\n")
	if got := runCLI(t, dir, "links").stdout; !strings.Contains(got, "No links found.") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestLinksExpandsConfiguredShorthands(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Work"}
{"type":"task","id":"cccc0002","parent":"cccc0001","state":"NEXT","title":"One-link task","body":"Ticket jira:OPS-7"}
`)
	seedConfig(t, dir, "link.jira = https://acme.atlassian.net/browse/%s\n")
	result := runCLI(t, dir, "links", "--json")
	if !strings.Contains(result.stdout, `"url":"https://acme.atlassian.net/browse/OPS-7"`) ||
		!strings.Contains(result.stdout, `"label":"jira:OPS-7"`) {
		t.Fatalf("stdout = %s", result.stdout)
	}
}

// -- open --------------------------------------------------------------------

func TestOpenPrintsASingleLink(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "Review", "--print")
	if result.status != 0 {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "https://docs.google.com/document/d/abc/edit" {
		t.Fatalf("stdout = %q", result.stdout)
	}
}

func TestOpenWithSeveralLinksAsksInsteadOfGuessing(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "billing", "--print")
	if result.status == 0 {
		t.Fatal("several links must not launch one of them")
	}
	if !strings.Contains(result.stderr, "2 links — pick one") {
		t.Fatalf("stderr = %q", result.stderr)
	}
	if !strings.Contains(result.stderr, "1. slack") || !strings.Contains(result.stderr, "2. jira") {
		t.Fatalf("the numbered candidates are the contract: %q", result.stderr)
	}
	if result.stdout != "" {
		t.Fatalf("nothing goes to stdout on a refusal, got %q", result.stdout)
	}
}

func TestOpenPickIsOneBasedAndRefusesOutOfRange(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	if got := runCLI(t, dir, "open", "billing", "2", "--print").stdout; !strings.Contains(got, "atlassian") {
		t.Fatalf("pick 2 = %q", got)
	}
	// 0 would negative-index into the tail in a language that allows it.
	for _, pick := range []string{"0", "9"} {
		result := runCLI(t, dir, "open", "billing", pick, "--print")
		if result.status == 0 || !strings.Contains(result.stderr, "no link #"+pick) {
			t.Errorf("pick %s: exit %d stderr %q", pick, result.status, result.stderr)
		}
	}
}

func TestOpenSystemFilterSelectsWithoutAPick(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "billing", "--system", "jira", "--print")
	if result.status != 0 || !strings.Contains(result.stdout, "OPS-1234") {
		t.Fatalf("exit %d stdout %q", result.status, result.stdout)
	}
}

func TestOpenNoLinksFailsCleanly(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "Dana", "--print")
	if result.status == 0 || !strings.Contains(result.stderr, "no links on: Leave comments for Dana") {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
}

func TestOpenJSONReportsTheLinkItActedOn(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "Review", "--print", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if payload["url"] != "https://docs.google.com/document/d/abc/edit" {
		t.Errorf("url = %v", payload["url"])
	}
	if payload["id"] != "aaaa0002" {
		t.Errorf("id = %v", payload["id"])
	}
	// --print is the no-launch form, and says so rather than lying about it.
	if payload["opened"] != false {
		t.Errorf("opened = %v, want false under --print", payload["opened"])
	}
}

func TestOpenJSONRefusalsAreErrorObjects(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	for _, testCase := range []struct {
		name string
		argv []string
		want string
	}{
		{"no links", []string{"open", "Dana", "--json", "--print"}, "not_found"},
		{"out of range", []string{"open", "billing", "9", "--json", "--print"}, "not_found"},
		{"ambiguous", []string{"open", "billing", "--json", "--print"}, "ambiguous"},
	} {
		result := runCLI(t, dir, testCase.argv...)
		var payload map[string]any
		if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
			t.Fatalf("%s: parse: %v (%s)", testCase.name, err, result.stdout)
		}
		if payload["error"] != testCase.want || payload["action"] != "open" {
			t.Errorf("%s: envelope = %v", testCase.name, payload)
		}
		if result.status == 0 {
			t.Errorf("%s: a refusal must not exit 0", testCase.name)
		}
	}
}

func TestOpenExpectsALinkNumber(t *testing.T) {
	dir := seedStore(t, nestedFixture)
	result := runCLI(t, dir, "open", "billing", "second", "--print")
	if result.status == 0 || !strings.Contains(result.stderr, "expected a link number, got: second") {
		t.Fatalf("exit %d stderr %q", result.status, result.stderr)
	}
}

// -- the schema gate ---------------------------------------------------------

// A read this build cannot interpret must REFUSE, not answer. An empty list and
// a list from a file the binary cannot read are indistinguishable to a caller,
// and the second one is a lie.
func TestReadCommandsRefuseAnUnsupportedSchema(t *testing.T) {
	dir := seedStore(t, "{\"type\":\"meta\",\"version\":1}\n"+
		"{\"type\":\"task\",\"id\":\"aaaa0001\",\"state\":\"TODO\",\"title\":\"x\"}\n")
	for _, command := range []string{"next", "inbox", "quadrants", "projects", "links"} {
		result := runCLI(t, dir, command)
		if result.status != 1 {
			t.Errorf("%s: exit %d, want 1", command, result.status)
		}
		if !strings.Contains(result.stderr, "unsupported meta version 1") {
			t.Errorf("%s: stderr = %q", command, result.stderr)
		}
		if result.stdout != "" {
			t.Errorf("%s: answered anyway with %q", command, result.stdout)
		}
	}
	// Under --json the refusal is a document, and it carries NO rolled_back:
	// the gate fires before anything is attempted, so there is no write to have
	// been reverted.
	result := runCLI(t, dir, "next", "--json")
	if strings.Contains(result.stdout, "rolled_back") {
		t.Errorf("schema refusal must not claim a rollback: %s", result.stdout)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &payload); err != nil {
		t.Fatalf("parse: %v (%s)", err, result.stdout)
	}
	if payload["error"] != "unsupported_schema_version" || payload["action"] != "next" {
		t.Errorf("envelope = %v", payload)
	}
}
