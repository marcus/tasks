package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The four project-write routes, mirroring test/api/test_projects.rb name for
// name, plus the coverage that suite does not carry: the bootstrapped root, the
// checked-store gate on each route, the archive's moved ids, and the live and
// archive BYTES a sweep leaves behind.
//
// Every case asserts the store as well as the response. A project write that
// answered correctly and wrote the wrong records would pass a response-only
// suite, and the store is the half the user cannot recover.

// The project fixture is test_helper.rb's PROJECTS_FIXTURE_RECORDS: an Inbox
// (excluded from areas), a "Projects" heading whose section children are
// projects — "Site launch" with a body note, a recurring NEXT, a deadlined TODO,
// a nested "Copywriting" sub-section and a deferred TODO; "Stuck reno" with no
// NEXT; and an empty project — plus a "Tasks" area and a "Done pile" whose only
// task is DONE, so it never surfaces as an area.
const (
	pfxInbox     = "cccc0001"
	pfxInboxTask = "cccc0002"
	pfxProjects  = "cccc0003"
	pfxSite      = "cccc0004"
	pfxSiteNext  = "cccc0005"
	pfxSiteTodo  = "cccc0006"
	pfxSiteSub   = "cccc0007"
	pfxSiteSubTk = "cccc0008"
	pfxSiteDefer = "cccc0009"
	pfxReno      = "cccc000a"
	pfxRenoTodo  = "cccc000b"
	pfxEmpty     = "cccc000c"
	pfxTasks     = "cccc000d"
	pfxTasksNext = "cccc000e"
	pfxTasksTodo = "cccc000f"
	pfxDonepile  = "cccc0010"
	pfxDoneTask  = "cccc0011"
)

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

// A project whose only open task is deferred: open_count 0, held_count 1.
const deferredOnlyFixture = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Projects"}
{"type":"section","id":"dddd0002","parent":"dddd0001","title":"Parked"}
{"type":"task","id":"dddd0003","parent":"dddd0002","state":"TODO","title":"Someday: revisit","tags":["defer"]}
`

const proposedProjectFixture = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Projects"}
{"type":"section","id":"dddd0002","parent":"dddd0001","title":"Candidate"}
{"type":"task","id":"dddd0003","parent":"dddd0002","state":"PROPOSED","title":"Investigate the candidate"}
`

// An area whose soonest open task carries a TIMED deadline — the fixture the
// Ruby regression test added alongside this one uses.
const timedAreaFixture = `{"type":"meta","version":2}
{"type":"section","id":"dddd0001","title":"Projects"}
{"type":"section","id":"dddd0002","title":"Vendors"}
{"type":"task","id":"dddd0003","parent":"dddd0002","state":"NEXT","title":"Reply to the vendor","deadline":"2026-07-28","deadline_time":{"local":"17:00","timezone":"Europe/London"}}
`

func newProjectHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, projectsFixture, "", "")
}

// recordFor is test_helper.rb's record_for: the parsed live record with that
// title, or nil. Asserting on a FIELD rather than on a regex over the file is
// what makes "the write landed" a claim about the record.
func (h *harness) recordFor(title string) map[string]any {
	h.t.Helper()
	for _, line := range strings.Split(string(h.storeBytes()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(line), &parsed) != nil {
			continue
		}
		if parsed["title"] == title {
			return parsed
		}
	}
	return nil
}

// archiveBytes is the archive file, or "" when no sweep has created it.
func (h *harness) archiveBytes() string {
	h.t.Helper()
	raw, err := os.ReadFile(h.archive)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (h *harness) postProject(path string) answer {
	return h.do(request{method: "POST", path: path})
}

// -- create -------------------------------------------------------------------

func TestCreateProjectReturns201WithTheNewProject(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects", `{"title":"Mid-year Reviews"}`, nil)
	assertStatus(t, answered, 201)
	data := answered.data()
	if data["title"] != "Mid-year Reviews" || data["kind"] != "project" {
		t.Errorf("created = %v", data)
	}
	if data["open_count"] != float64(0) || data["stuck"] != true {
		t.Errorf("a fresh project is empty and stuck: %v", data)
	}
	id, _ := data["id"].(string)
	if got := answered.Header.Get("location"); got != "/api/v1/projects/"+id {
		t.Errorf("location = %q, want /api/v1/projects/%s", got, id)
	}
	// The ETag is the GLOBAL store revision, which is also what the envelope
	// reports: a project carries no per-resource revision.
	revision, _ := answered.dig("meta", "store_revision").(string)
	if answered.etag() != `"`+revision+`"` {
		t.Errorf("etag %q does not match meta.store_revision %q", answered.etag(), revision)
	}
	created := h.recordFor("Mid-year Reviews")
	if created == nil || created["parent"] != pfxProjects {
		t.Errorf("the project was not filed under the Projects root: %v", created)
	}
	if created["type"] != "section" {
		t.Errorf("a project is a section, got %v", created["type"])
	}
	// The new project is immediately addressable, which is what the Location
	// header promises.
	assertStatus(t, h.get("/api/v1/projects/"+id), 200)
}

// Ruby's API suite never creates a project in a store with no "Projects" root.
// The domain bootstraps one, and a client that got a 201 naming a project filed
// under nothing would have no way to find it again.
func TestCreateProjectBootstrapsTheProjectsRoot(t *testing.T) {
	h := newHarnessWith(t, `{"type":"meta","version":2}`+"\n", "", "")
	answered := h.json("POST", "/api/v1/projects", `{"title":"First"}`, nil)
	assertStatus(t, answered, 201)
	root := h.recordFor("Projects")
	created := h.recordFor("First")
	if root == nil || created == nil {
		t.Fatalf("root or project missing: %s", h.storeBytes())
	}
	if created["parent"] != root["id"] {
		t.Errorf("project parent = %v, want the bootstrapped root %v", created["parent"], root["id"])
	}
	if answered.data()["parent_id"] != root["id"] {
		t.Errorf("resource parent_id = %v, want %v", answered.data()["parent_id"], root["id"])
	}
}

func TestCreateProjectBlankTitleIs422(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	assertError(t, h.json("POST", "/api/v1/projects", `{"title":"   "}`, nil), 422, "validation_failed")
	if string(h.storeBytes()) != before {
		t.Error("a refused create wrote to the store")
	}
}

func TestCreateProjectMissingTitleIs422(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects", `{}`, nil)
	assertError(t, answered, 422, "validation_failed")
	if !strings.Contains(answered.Body, "is required") {
		t.Errorf("details do not say the title is required: %s", answered.Body)
	}
}

func TestCreateProjectDuplicateTitleIs422(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	// Case-insensitively equal to the existing "Site launch" project: those
	// titles are the project-ref candidate set, so a duplicate would make every
	// later ref to it ambiguous. An AREA title collides too.
	for _, testCase := range []struct{ sent, quoted string }{
		{"site launch", `"site launch"`},
		{"TASKS", `"TASKS"`},
	} {
		answered := h.json("POST", "/api/v1/projects", `{"title":"`+testCase.sent+`"}`, nil)
		assertError(t, answered, 422, "validation_failed")
		// The reason arrives under `title`, not as an empty details object: it is
		// the one refusal on this route a client can actually act on, and a bare
		// "one or more fields are invalid" would hide which field and why.
		fields, _ := answered.dig("error", "details", "fields").(map[string]any)
		reasons := stringsOf(fields["title"])
		if len(reasons) != 1 {
			t.Fatalf("%q: details.fields.title = %v", testCase.sent, fields["title"])
		}
		want := "a project or area named " + testCase.quoted + " already exists"
		if reasons[0] != want {
			t.Errorf("%q: reason = %q, want %q", testCase.sent, reasons[0], want)
		}
	}
	if string(h.storeBytes()) != before {
		t.Error("a refused duplicate wrote to the store")
	}
}

func TestCreateProjectUnknownFieldIs422(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects", `{"title":"X","colour":"red"}`, nil)
	assertError(t, answered, 422, "validation_failed")
	if !strings.Contains(answered.Body, "unknown request field colour") {
		t.Errorf("the refusal does not name the field: %s", answered.Body)
	}
}

func TestCreateProjectRejectsAForeignOrigin(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects", `{"title":"X"}`,
		map[string]string{"Origin": "http://evil.example"})
	assertError(t, answered, 403, "forbidden_origin")
	if h.recordFor("X") != nil {
		t.Error("a foreign origin wrote a project")
	}
}

// -- rename -------------------------------------------------------------------

func TestRenameProjectUpdatesTitle(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("PATCH", "/api/v1/projects/"+pfxReno, `{"title":"  Kitchen reno  "}`, nil)
	assertStatus(t, answered, 200)
	// The domain trims, and the response reports what was stored rather than
	// what was sent.
	if answered.data()["title"] != "Kitchen reno" {
		t.Errorf("title = %v", answered.data()["title"])
	}
	if h.recordFor("Kitchen reno") == nil {
		t.Errorf("the section was not retitled: %s", h.storeBytes())
	}
	if h.recordFor("Stuck reno") != nil {
		t.Error("the old title survived the rename")
	}
	// A retitle moves no task, so the rollups are the ones the read model had.
	if answered.data()["open_count"] != float64(1) {
		t.Errorf("open_count = %v, want 1", answered.data()["open_count"])
	}
}

func TestRenameProjectBlankTitleIs422(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	assertError(t, h.json("PATCH", "/api/v1/projects/"+pfxReno, `{"title":"   "}`, nil), 422, "validation_failed")
	if string(h.storeBytes()) != before {
		t.Error("a refused rename wrote to the store")
	}
}

func TestRenameProjectUnknownFieldIs422(t *testing.T) {
	h := newProjectHarness(t)
	assertError(t, h.json("PATCH", "/api/v1/projects/"+pfxReno, `{"title":"X","colour":"red"}`, nil),
		422, "validation_failed")
}

func TestRenameMissingProjectIs404(t *testing.T) {
	h := newProjectHarness(t)
	assertError(t, h.json("PATCH", "/api/v1/projects/ffffffff", `{"title":"Ghost"}`, nil), 404, "not_found")
	// A malformed id is a transport refusal, before the read model is consulted.
	assertError(t, h.json("PATCH", "/api/v1/projects/not-hex", `{"title":"Ghost"}`, nil), 400, "malformed_request")
}

// Retitling the "Tasks" area to "Inbox" moves it out of the read model; the
// write committed, so the response is a synthesized 200, not a 404.
func TestRenameAreaOutOfScopeReturns200WithNewTitle(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("PATCH", "/api/v1/projects/"+pfxTasks, `{"title":"Inbox"}`, nil)
	assertStatus(t, answered, 200)
	if answered.data()["title"] != "Inbox" {
		t.Errorf("title = %v", answered.data()["title"])
	}
	if h.recordFor("Tasks") != nil {
		t.Error("the section was not retitled out of scope")
	}
	// It really is out of the read model now, which is what makes the 200 a
	// synthesis rather than a re-read.
	assertError(t, h.get("/api/v1/projects/"+pfxTasks), 404, "not_found")
}

// The synthesized response must carry the SAME rollups the read model reported,
// including the timed pair. Ruby dropped `next_time` and `next_at` here — they
// default to nil in ProjectView and its synthesis did not pass them — which this
// port refused to reproduce; lib/tasks/api/app.rb was fixed instead.
func TestRenameAreaOutOfScopeKeepsTheTimedRollups(t *testing.T) {
	h := newHarnessWith(t, timedAreaFixture, "", "")
	before := h.get("/api/v1/projects/dddd0002")
	assertStatus(t, before, 200)
	timed, _ := before.data()["next_time"].(map[string]any)
	if timed == nil || timed["local"] != "17:00" {
		t.Fatalf("fixture precondition: next_time = %v", before.data()["next_time"])
	}

	answered := h.json("PATCH", "/api/v1/projects/dddd0002", `{"title":"Inbox"}`, nil)
	assertStatus(t, answered, 200)
	after, _ := answered.data()["next_time"].(map[string]any)
	if after == nil {
		t.Fatalf("the retitle dropped next_time: %s", answered.Body)
	}
	for _, key := range []string{"local", "timezone", "fold", "effective_timezone", "instant"} {
		if after[key] != timed[key] {
			t.Errorf("next_time.%s = %v, want %v", key, after[key], timed[key])
		}
	}
	if answered.data()["next_at"] != before.data()["next_at"] {
		t.Errorf("next_at = %v, want %v", answered.data()["next_at"], before.data()["next_at"])
	}
	if answered.data()["next_date"] != before.data()["next_date"] {
		t.Errorf("next_date = %v, want %v", answered.data()["next_date"], before.data()["next_date"])
	}
}

func TestRenameOnInboxOrProjectsRootIs404AndWritesNothing(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	// Four ids that are sections but not projects or areas, plus a task id. Each
	// must refuse BEFORE the store's mechanical section retitle runs.
	for _, id := range []string{pfxInbox, pfxProjects, pfxDonepile, pfxSiteSub, pfxRenoTodo} {
		assertError(t, h.json("PATCH", "/api/v1/projects/"+id, `{"title":"Renamed"}`, nil), 404, "not_found")
		if string(h.storeBytes()) != before {
			t.Fatalf("a non-project rename (%s) wrote to the store", id)
		}
	}
}

// Project mutations carry no per-resource revision, so — unlike task PATCH — a
// missing If-Match is not 428.
func TestRenameRequiresNoIfMatch(t *testing.T) {
	h := newProjectHarness(t)
	assertStatus(t, h.json("PATCH", "/api/v1/projects/"+pfxReno, `{"title":"Kitchen reno"}`, nil), 200)
}

// -- complete -----------------------------------------------------------------

func TestCompleteProjectClosesOpenTasks(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects/"+pfxSite+"/complete", "", nil)
	assertStatus(t, answered, 200)
	data := answered.data()
	if data["open_count"] != float64(0) || data["next_count"] != float64(0) {
		t.Errorf("rollups after completion = %v", data)
	}
	if ids := stringsOf(data["task_ids"]); len(ids) != 0 {
		t.Errorf("task_ids = %v, want empty", ids)
	}
	// The cascade reaches a nested sub-section's task and the DEFERRED one, and
	// retires the recurrence cookie rather than rolling it forward.
	for _, title := range []string{
		"Pick a static-site generator", "Write the landing copy",
		"Draft the about page", "Someday: custom domain",
	} {
		record := h.recordFor(title)
		if record == nil || record["state"] != "DONE" {
			t.Errorf("%q was not closed: %v", title, record)
		}
	}
	if recurring := h.recordFor("Pick a static-site generator"); recurring["recur"] != nil {
		t.Errorf("the recurrence cookie survived completion: %v", recurring["recur"])
	}
	if deferred := h.recordFor("Someday: custom domain"); deferred["tags"] != nil {
		if tags := stringsOf(deferred["tags"]); containsString(tags, "defer") {
			t.Errorf("the defer tag survived completion: %v", tags)
		}
	}
	// The section itself remains, and stays addressable.
	if h.recordFor("Site launch") == nil {
		t.Error("completion removed the project section")
	}
	assertStatus(t, h.get("/api/v1/projects/"+pfxSite), 200)
}

// A second completion writes nothing and is still a clean 200. Zero closed is
// also what a rolled-back write reports, so a build that mapped it to a failure
// would refuse an ordinary repeat.
func TestCompleteProjectAgainIsACleanRepeat(t *testing.T) {
	h := newProjectHarness(t)
	assertStatus(t, h.json("POST", "/api/v1/projects/"+pfxSite+"/complete", "", nil), 200)
	after := string(h.storeBytes())
	again := h.json("POST", "/api/v1/projects/"+pfxSite+"/complete", "", nil)
	assertStatus(t, again, 200)
	if string(h.storeBytes()) != after {
		t.Error("an idempotent repeat rewrote the store")
	}
	// An already-empty project is the same clean 200.
	assertStatus(t, h.json("POST", "/api/v1/projects/"+pfxEmpty+"/complete", "", nil), 200)
}

func TestCompleteMissingProjectIs404(t *testing.T) {
	h := newProjectHarness(t)
	assertError(t, h.json("POST", "/api/v1/projects/ffffffff/complete", "", nil), 404, "not_found")
}

// An area drops out of the read model once its open work is closed; the completed
// 200 is synthesized from the pre-read, never a post-write 404.
func TestCompleteAreaClosesItsTasksAndReturnsZeroOpen(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.json("POST", "/api/v1/projects/"+pfxTasks+"/complete", "", nil)
	assertStatus(t, answered, 200)
	data := answered.data()
	if data["open_count"] != float64(0) || data["held_count"] != float64(0) {
		t.Errorf("rollups = %v", data)
	}
	if data["stuck"] != true {
		t.Errorf("stuck = %v, want true", data["stuck"])
	}
	if ids := stringsOf(data["task_ids"]); len(ids) != 0 {
		t.Errorf("task_ids = %v, want empty", ids)
	}
	for _, title := range []string{"Reply to the vendor", "File expenses"} {
		if record := h.recordFor(title); record == nil || record["state"] != "DONE" {
			t.Errorf("%q was not closed: %v", title, record)
		}
	}
	// The synthesis was necessary: the area is gone from the read model.
	assertError(t, h.get("/api/v1/projects/"+pfxTasks), 404, "not_found")
}

func TestCompleteOnInboxOrProjectsRootIs404AndWritesNothing(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	// Neither is a project or area; each must 404 BEFORE any cascade runs — the
	// store's CompleteProject would happily close Inbox's tasks.
	for _, id := range []string{pfxInbox, pfxProjects, pfxDonepile, pfxRenoTodo} {
		assertError(t, h.json("POST", "/api/v1/projects/"+id+"/complete", "", nil), 404, "not_found")
		if string(h.storeBytes()) != before {
			t.Fatalf("a non-project complete (%s) closed tasks", id)
		}
	}
	if record := h.recordFor("unfiled capture"); record["state"] != "INBOX" {
		t.Errorf("the Inbox task was closed: %v", record)
	}
}

func TestCompleteRejectsABody(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	assertError(t, h.json("POST", "/api/v1/projects/"+pfxSite+"/complete", `{"unexpected":true}`, nil),
		400, "malformed_request")
	// A non-JSON body is refused on the media type first, exactly as DELETE is.
	assertError(t, h.do(request{
		method: "POST", path: "/api/v1/projects/" + pfxSite + "/complete",
		body: "nope", contentType: "text/plain",
	}), 415, "unsupported_media_type")
	if string(h.storeBytes()) != before {
		t.Error("a refused complete wrote to the store")
	}
}

// -- archive ------------------------------------------------------------------

func TestArchiveRefusesWhileOpenTasksRemain(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	answered := h.postProject("/api/v1/projects/" + pfxSite + "/archive")
	assertError(t, answered, 409, "conflict")
	details, _ := answered.dig("error", "details").(map[string]any)
	if details["open_count"] != float64(3) {
		t.Errorf("open_count = %v, want 3", details["open_count"])
	}
	if details["held_count"] != float64(1) {
		t.Errorf("held_count = %v, want 1", details["held_count"])
	}
	if !strings.Contains(answered.message(), "force=true") {
		t.Errorf("the refusal does not name the remedy: %q", answered.message())
	}
	if string(h.storeBytes()) != before {
		t.Error("a refused archive wrote to the store")
	}
	if h.archiveBytes() != "" {
		t.Error("a refused archive created the archive file")
	}
}

func TestArchiveForceSweepsTheSubtree(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.postProject("/api/v1/projects/" + pfxSite + "/archive?force=true")
	assertStatus(t, answered, 200)
	data := answered.data()
	if data["id"] != pfxSite {
		t.Errorf("id = %v", data["id"])
	}
	// The whole contiguous subtree: the project, four tasks, and the nested
	// sub-section — root first.
	moved := stringsOf(data["moved_ids"])
	want := []string{pfxSite, pfxSiteNext, pfxSiteTodo, pfxSiteSub, pfxSiteSubTk, pfxSiteDefer}
	assertStrings(t, moved, want, "moved_ids")
	if data["archived"] != float64(len(want)) {
		t.Errorf("archived = %v, want %d", data["archived"], len(want))
	}
	if h.recordFor("Site launch") != nil {
		t.Error("the project survived the sweep in the live file")
	}
	// The swept root drops its parent and gains today's archived stamp, and the
	// archive file is where the subtree now lives.
	archive := h.archiveBytes()
	for _, title := range []string{"Site launch", "Draft the about page", "Someday: custom domain"} {
		if !strings.Contains(archive, `"title":"`+title+`"`) {
			t.Errorf("%q is not in the archive: %s", title, archive)
		}
	}
	if !strings.Contains(archive, `"archived":"2026-07-15"`) {
		t.Errorf("the swept root carries no archived stamp from the pinned clock: %s", archive)
	}
	// The project resource is gone, so a second sweep has nothing to find.
	assertError(t, h.get("/api/v1/projects/"+pfxSite), 404, "not_found")
	assertError(t, h.postProject("/api/v1/projects/"+pfxSite+"/archive"), 404, "not_found")
}

// A project whose only open work is deferred has open_count 0 but held_count 1;
// that still blocks archive without force (parity with the CLI and with
// complete's cascade, which closes held tasks).
func TestArchiveRefusesADeferredOnlyProjectThenForceSweeps(t *testing.T) {
	h := newHarnessWith(t, deferredOnlyFixture, "", "")
	before := string(h.storeBytes())
	refused := h.postProject("/api/v1/projects/dddd0002/archive")
	assertError(t, refused, 409, "conflict")
	details, _ := refused.dig("error", "details").(map[string]any)
	if details["open_count"] != float64(0) || details["held_count"] != float64(1) {
		t.Errorf("details = %v, want open_count 0 and held_count 1", details)
	}
	if string(h.storeBytes()) != before {
		t.Error("a refused archive writes nothing")
	}

	forced := h.postProject("/api/v1/projects/dddd0002/archive?force=true")
	assertStatus(t, forced, 200)
	if h.recordFor("Parked") != nil {
		t.Error("the forced sweep left the project behind")
	}
	if !strings.Contains(h.archiveBytes(), `"title":"Someday: revisit"`) {
		t.Errorf("the deferred task did not reach the archive: %s", h.archiveBytes())
	}
}

// A proposal archived without a decision is a decision nobody made, so it blocks
// the sweep even under force — the one refusal force cannot override.
func TestArchiveForceNeverSweepsAnUndecidedProposal(t *testing.T) {
	h := newHarnessWith(t, proposedProjectFixture, "", "")
	before := string(h.storeBytes())
	answered := h.postProject("/api/v1/projects/dddd0002/archive?force=true")
	assertError(t, answered, 409, "conflict")
	if !strings.Contains(answered.message(), "decide proposed tasks") {
		t.Errorf("message = %q", answered.message())
	}
	if string(h.storeBytes()) != before {
		t.Error("the refused sweep wrote to the live store")
	}
	if h.archiveBytes() != "" {
		t.Error("the refused sweep created an archive file")
	}
}

func TestArchiveEmptyProjectNeedsNoForce(t *testing.T) {
	h := newProjectHarness(t)
	answered := h.postProject("/api/v1/projects/" + pfxEmpty + "/archive")
	assertStatus(t, answered, 200)
	if answered.data()["archived"] != float64(1) {
		t.Errorf("archived = %v, want 1", answered.data()["archived"])
	}
	assertStrings(t, stringsOf(answered.data()["moved_ids"]), []string{pfxEmpty}, "moved_ids")
	if h.recordFor("Empty project") != nil {
		t.Error("the empty project survived the sweep")
	}
}

func TestArchiveRejectsUnknownQuery(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	assertError(t, h.postProject("/api/v1/projects/"+pfxEmpty+"/archive?bogus=1"), 422, "validation_failed")
	// A force value that is not a boolean is refused rather than guessed at.
	assertError(t, h.postProject("/api/v1/projects/"+pfxEmpty+"/archive?force=yes"), 422, "validation_failed")
	// And the query is validated BEFORE the body refusal, exactly as in Ruby.
	assertError(t, h.json("POST", "/api/v1/projects/"+pfxEmpty+"/archive?force=yes", `{"a":1}`, nil),
		422, "validation_failed")
	assertError(t, h.json("POST", "/api/v1/projects/"+pfxEmpty+"/archive", `{"a":1}`, nil),
		400, "malformed_request")
	if string(h.storeBytes()) != before {
		t.Error("a refused archive wrote to the store")
	}
}

func TestArchiveOnANonProjectIs404AndWritesNothing(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	for _, id := range []string{pfxInbox, pfxProjects, pfxDonepile, pfxRenoTodo, "ffffffff"} {
		assertError(t, h.postProject("/api/v1/projects/"+id+"/archive?force=true"), 404, "not_found")
		if string(h.storeBytes()) != before {
			t.Fatalf("a non-project archive (%s) wrote to the store", id)
		}
		if h.archiveBytes() != "" {
			t.Fatalf("a non-project archive (%s) created an archive file", id)
		}
	}
}

// -- shared policy ------------------------------------------------------------

func TestProjectMutationRejectsAForeignOrigin(t *testing.T) {
	h := newProjectHarness(t)
	before := string(h.storeBytes())
	foreign := map[string]string{"Origin": "http://evil.example"}
	assertError(t, h.json("PATCH", "/api/v1/projects/"+pfxReno, `{"title":"X"}`, foreign), 403, "forbidden_origin")
	assertError(t, h.json("POST", "/api/v1/projects", `{"title":"X"}`, foreign), 403, "forbidden_origin")
	assertError(t, h.json("POST", "/api/v1/projects/"+pfxSite+"/complete", "", foreign), 403, "forbidden_origin")
	assertError(t, h.do(request{
		method: "POST", path: "/api/v1/projects/" + pfxEmpty + "/archive", headers: foreign,
	}), 403, "forbidden_origin")
	if string(h.storeBytes()) != before {
		t.Error("a foreign origin wrote to the store")
	}
}

// Every project write asks the store's health BEFORE it starts, so a schema this
// build must not interpret is refused with the diagnostic that names it rather
// than by whichever step happens to fail first.
func TestProjectWritesGateOnTheCheckedStore(t *testing.T) {
	cases := []struct{ name, method, path, body string }{
		{"create", "POST", "/api/v1/projects", `{"title":"X"}`},
		{"rename", "PATCH", "/api/v1/projects/" + pfxReno, `{"title":"X"}`},
		{"complete", "POST", "/api/v1/projects/" + pfxSite + "/complete", ""},
		{"archive", "POST", "/api/v1/projects/" + pfxEmpty + "/archive", ""},
	}
	for _, testCase := range cases {
		h := newProjectHarness(t)
		h.writeStore(strings.Replace(projectsFixture, `"version":2`, `"version":1`, 1))
		before := string(h.storeBytes())
		answered := h.json(testCase.method, testCase.path, testCase.body, nil)
		assertError(t, answered, 503, "unsupported_schema_version")
		details, _ := answered.dig("error", "details").(map[string]any)
		if details["supported_version"] != float64(2) {
			t.Errorf("%s: supported_version = %v", testCase.name, details["supported_version"])
		}
		if string(h.storeBytes()) != before {
			t.Errorf("%s: wrote to a store it refused to read", testCase.name)
		}
	}
}

// A second server over the same files sees every project write, which is the
// question that matters for a surface where the CLI, the TUI and another request
// are all writing the same pair of files.
func TestProjectWritesAreVisibleToASecondServer(t *testing.T) {
	first := newProjectHarness(t)
	second := newHarnessSharing(t, first)

	created := first.json("POST", "/api/v1/projects", `{"title":"Shared"}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)
	assertStatus(t, second.get("/api/v1/projects/"+id), 200)

	assertStatus(t, second.json("PATCH", "/api/v1/projects/"+id, `{"title":"Renamed elsewhere"}`, nil), 200)
	if first.get("/api/v1/projects/" + id).data()["title"] != "Renamed elsewhere" {
		t.Error("the first server did not see the second's rename")
	}

	assertStatus(t, second.postProject("/api/v1/projects/"+id+"/archive"), 200)
	assertError(t, first.get("/api/v1/projects/"+id), 404, "not_found")
}
