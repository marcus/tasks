package api

import (
	"sort"
	"strings"
	"testing"
)

// These mirror test/api/test_app.rb's read half, keeping the Ruby test names so
// the mapping between the two suites is readable without a table.

func TestHealthReadinessMetaAndSections(t *testing.T) {
	h := newHarness(t)

	health := h.get("/healthz")
	assertStatus(t, health, 200)
	if health.Body != `{"status":"ok"}` {
		t.Errorf("healthz body = %s", health.Body)
	}
	if health.Header.Get("content-type") != "application/json" {
		t.Errorf("content-type = %q", health.Header.Get("content-type"))
	}
	if !strings.HasPrefix(health.Header.Get("x-request-id"), "req_") {
		t.Errorf("x-request-id = %q", health.Header.Get("x-request-id"))
	}
	if health.Header.Get("cache-control") != "no-store" {
		t.Errorf("cache-control = %q", health.Header.Get("cache-control"))
	}

	ready := h.get("/readyz")
	assertStatus(t, ready, 200)
	if ready.Body != `{"status":"ready"}` {
		t.Errorf("readyz body = %s", ready.Body)
	}

	meta := h.get("/api/v1/meta")
	assertStatus(t, meta, 200)
	if got, _ := meta.dig("data", "server_mode").(string); got != "loopback" {
		t.Errorf("server_mode = %q", got)
	}
	assertStrings(t, stringsOf(meta.dig("data", "states")),
		[]string{"PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"}, "states")
	assertStrings(t, stringsOf(meta.dig("data", "proposed_states")), []string{"PROPOSED"}, "proposed_states")
	assertStrings(t, stringsOf(meta.dig("data", "open_states")),
		[]string{"INBOX", "TODO", "NEXT", "WAITING"}, "open_states")
	assertStrings(t, stringsOf(meta.dig("data", "closed_states")),
		[]string{"DONE", "CANCELLED"}, "closed_states")
	if got, _ := meta.dig("data", "max_depth").(float64); got != 4 {
		t.Errorf("max_depth = %v", got)
	}
	capabilities, _ := meta.dig("data", "capabilities").(map[string]any)
	for name, want := range map[string]bool{
		"projects": true, "undo": false, "redo": false, "archive_sweep": false, "events": false,
	} {
		if capabilities[name] != want {
			t.Errorf("capability %s = %v, want %v", name, capabilities[name], want)
		}
	}
	revision, _ := meta.dig("meta", "store_revision").(string)
	if meta.etag() != `"`+revision+`"` {
		t.Errorf("meta etag = %q, store_revision = %q", meta.etag(), revision)
	}
	if strings.Contains(meta.Body, h.dir) {
		t.Error("meta body leaks the store directory")
	}

	sections := h.get("/api/v1/sections")
	assertStatus(t, sections, 200)
	assertStrings(t, sections.ids(), []string{fixInbox, fixWork, fixHome}, "section ids")
}

// The three unrouted capabilities must be advertised false AND really absent.
// A future PR that adds an endpoint has to flip the flag and delete the matching
// 404 assertion in the same change.
func TestUnroutedCapabilitiesAreAdvertisedAsFalseAndReallyAreAbsent(t *testing.T) {
	h := newHarness(t)
	capabilities, _ := h.get("/api/v1/meta").dig("data", "capabilities").(map[string]any)

	for capability, path := range map[string]string{
		"undo": "/api/v1/history/undo", "redo": "/api/v1/history/redo",
		"archive_sweep": "/api/v1/archive-sweeps",
	} {
		if capabilities[capability] != false {
			t.Errorf("%s is advertised but has no endpoint", capability)
		}
		if got := h.get(path).Status; got != 404 {
			t.Errorf("GET %s = %d, so %s must be advertised true", path, got, capability)
		}
		posted := h.json("POST", path, "{}", map[string]string{"Origin": "http://127.0.0.1:4747"})
		if posted.Status != 404 {
			t.Errorf("POST %s = %d, so %s must be advertised true", path, posted.Status, capability)
		}
	}
}

func TestHealthDoesNotTouchStoreAndReadinessRefusesInvalidStore(t *testing.T) {
	h := newHarness(t)
	h.writeStore("{not-json\n")

	assertStatus(t, h.get("/healthz"), 200)
	answered := h.get("/readyz")
	assertError(t, answered, 503, "store_invalid")
	if strings.Contains(answered.Body, h.org) {
		t.Error("refusal leaks the store path")
	}
}

func TestAnUnsupportedSchemaVersionIsRefusedOnReadAndOnWrite(t *testing.T) {
	h := newHarness(t)
	h.writeStore(strings.Replace(fixtureOrg, `"version":2`, `"version":1`, 1))

	answered := h.get("/readyz")
	assertError(t, answered, 503, "unsupported_schema_version")
	if got, _ := answered.dig("error", "details", "supported_version").(float64); got != 2 {
		t.Errorf("supported_version = %v", got)
	}
	if strings.Contains(strings.ToLower(answered.message()), "migrat") {
		t.Errorf("refusal offers a migration: %q", answered.message())
	}

	assertError(t, h.get("/api/v1/tasks"), 503, "unsupported_schema_version")

	created := h.json("POST", "/api/v1/tasks", `{"title":"Should not be written"}`, nil)
	assertError(t, created, 503, "unsupported_schema_version")
	if !strings.Contains(string(h.storeBytes()), `"version":1`) {
		t.Error("the refused write changed the store")
	}
}

func TestAFutureSchemaVersionIsRefusedTheSameWay(t *testing.T) {
	h := newHarness(t)
	h.writeStore(strings.Replace(fixtureOrg, `"version":2`, `"version":3`, 1))
	assertError(t, h.get("/readyz"), 503, "unsupported_schema_version")
}

func TestListSupportsEveryDocumentedFilterAndRejectsUnknownQueries(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		query string
		want  []string
	}{
		{"scope=done", []string{fixOld}},
		{"scope=all&state=DONE", []string{fixOld, fixPR, "dddd0001"}},
		{"context=computer", []string{fixFlight, fixPR, fixEval}},
		{"tag=important", []string{fixFlight, fixPR, fixEval}},
		{"priority=B", []string{fixPR}},
		{"scope=all&text=plants", []string{fixPlants}},
		{"text=Some%20note&body=true", []string{fixTravel}},
		{"deferred=true", []string{fixPlants}},
		{"available=false", []string{fixPlants}},
		{"available=true", []string{fixGarden, fixFlight, fixPR, fixChild, fixGrand, fixEval, fixTravel}},
		{"recurring=true", []string{fixFlight}},
	}
	for _, testCase := range cases {
		answered := h.get("/api/v1/tasks?" + testCase.query)
		assertStatus(t, answered, 200)
		assertStrings(t, answered.ids(), testCase.want, testCase.query)
	}

	assertError(t, h.get("/api/v1/tasks?unknown=true"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?body=1"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?scope=done&available=false"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?tag=%40desk"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?tag=defer"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?context=@"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?state=NOPE"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?priority=Z"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?scope=nope"), 422, "validation_failed")
}

// A repeated query parameter is a malformed REQUEST, not a validation failure:
// the server cannot know which of the two the client meant.
func TestRepeatedQueryParameterIsMalformed(t *testing.T) {
	h := newHarness(t)
	assertError(t, h.get("/api/v1/tasks?scope=open&scope=all"), 400, "malformed_request")
}

func TestDelegationScopesRefuseContradictoryCombinations(t *testing.T) {
	h := newHarness(t)
	assertStatus(t, h.get("/api/v1/tasks?scope=delegated"), 200)
	assertStatus(t, h.get("/api/v1/tasks?scope=agent_ready"), 200)
	assertError(t, h.get("/api/v1/tasks?scope=agent_ready&delegated=true"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?scope=delegated&delegated=false"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?scope=delegated&state=DONE"), 422, "validation_failed")
	// The boolean composes with every lifecycle scope, which is the whole point
	// of keeping it separate from the shorthand scope.
	assertStatus(t, h.get("/api/v1/tasks?scope=all&delegated=true"), 200)
}

func TestTaskRepresentationAndSourceExactLookup(t *testing.T) {
	h := newHarness(t)
	live := h.get("/api/v1/tasks/" + fixPR)
	assertStatus(t, live, 200)
	task := live.data()

	expected := []string{
		"archived", "availability_blocker_id", "availability_reason", "available", "available_at",
		"body", "child_count", "closed", "contexts", "deadline", "deadline_time", "deferred",
		"delegation", "depth", "descendant_count", "formal_links", "id", "lead", "lead_human", "lead_opens",
		"lead_opens_at", "links", "parent_id", "priority", "project", "recurrence",
		"recurrence_human", "rejected", "revision", "scheduled", "scheduled_time", "section_id", "source",
		"state", "tags", "title",
	}
	keys := make([]string, 0, len(task))
	for key := range task {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assertStrings(t, keys, expected, "task keys")

	if task["source"] != "live" {
		t.Errorf("source = %v", task["source"])
	}
	if task["parent_id"] != nil {
		t.Errorf("top-level tasks expose no task parent, got %v", task["parent_id"])
	}
	if task["section_id"] != fixWork {
		t.Errorf("section_id = %v", task["section_id"])
	}
	assertStrings(t, stringsOf(task["tags"]), []string{"important"}, "tags")
	if _, present := task["line"]; present {
		t.Error("the resource exposes the physical line")
	}
	if _, present := task["headline"]; present {
		t.Error("the resource exposes the CLI headline")
	}
	revision, _ := task["revision"].(string)
	if live.etag() != `"`+revision+`"` {
		t.Errorf("etag %q does not quote revision %q", live.etag(), revision)
	}

	archived := h.get("/api/v1/tasks/" + fixPR + "?source=archive")
	assertStatus(t, archived, 200)
	archivedTask := archived.data()
	if archivedTask["title"] != "Archived duplicate" {
		t.Errorf("archived title = %v", archivedTask["title"])
	}
	if archivedTask["archived"] != true {
		t.Errorf("archived = %v", archivedTask["archived"])
	}
	if archivedTask["project"] != "Archive" {
		t.Errorf("archived project = %v", archivedTask["project"])
	}

	assertError(t, h.get("/api/v1/tasks/deadbeef"), 404, "not_found")
	assertError(t, h.get("/api/v1/tasks/NOT-AN-ID"), 400, "malformed_request")
	assertError(t, h.get("/api/v1/tasks/"+fixPR+"?source=both"), 422, "validation_failed")
}

func TestTreeCountsAndDepthAreDerived(t *testing.T) {
	h := newHarness(t)
	parent := h.get("/api/v1/tasks/" + fixPR).data()
	if parent["child_count"].(float64) != 1 {
		t.Errorf("child_count = %v", parent["child_count"])
	}
	if parent["descendant_count"].(float64) != 2 {
		t.Errorf("descendant_count = %v", parent["descendant_count"])
	}
	child := h.get("/api/v1/tasks/" + fixChild).data()
	if child["parent_id"] != fixPR {
		t.Errorf("parent_id = %v", child["parent_id"])
	}
	if child["depth"].(float64) != 1 {
		t.Errorf("depth = %v", child["depth"])
	}
	grandchild := h.get("/api/v1/tasks/" + fixGrand).data()
	if grandchild["depth"].(float64) != 2 {
		t.Errorf("grandchild depth = %v", grandchild["depth"])
	}
}

func TestRecurrenceAndLeadAreRenderedAlongsideTheStoredValue(t *testing.T) {
	h := newHarness(t)
	flight := h.get("/api/v1/tasks/" + fixFlight).data()
	if flight["recurrence"] != ".+1w" {
		t.Errorf("recurrence = %v", flight["recurrence"])
	}
	if flight["recurrence_human"] != "every week from completion" {
		t.Errorf("recurrence_human = %v", flight["recurrence_human"])
	}
	plants := h.get("/api/v1/tasks/" + fixPlants).data()
	if plants["recurrence"] != nil || plants["recurrence_human"] != nil {
		t.Errorf("a task with no schedule renders null, got %v/%v",
			plants["recurrence"], plants["recurrence_human"])
	}
	if plants["deferred"] != true {
		t.Errorf("deferred = %v", plants["deferred"])
	}
	assertStrings(t, stringsOf(plants["tags"]), []string{}, "the defer marker is not an ordinary tag")
	assertStrings(t, stringsOf(plants["contexts"]), []string{"@home"}, "contexts")
}

func TestProjectsAreRolledUpAndAddressableByID(t *testing.T) {
	h := newHarness(t)
	list := h.get("/api/v1/projects")
	assertStatus(t, list, 200)
	revision, _ := list.dig("meta", "store_revision").(string)
	if list.etag() != `"`+revision+`"` {
		t.Errorf("projects etag = %q", list.etag())
	}

	one := h.get("/api/v1/projects/" + fixWork)
	assertStatus(t, one, 200)
	project := one.data()
	for _, key := range []string{
		"id", "title", "parent_id", "kind", "open_count", "next_count", "next_date",
		"next_time", "next_at", "stuck", "held_count", "body", "task_ids",
	} {
		if _, present := project[key]; !present {
			t.Errorf("project resource omits %q: %s", key, one.Body)
		}
	}
	if _, present := project["line"]; present {
		t.Error("the project resource exposes the physical line")
	}

	// A section that is neither a project nor an area is not a project resource.
	assertError(t, h.get("/api/v1/projects/"+fixInbox), 404, "not_found")
	assertError(t, h.get("/api/v1/projects/deadbeef"), 404, "not_found")
}

// The explain endpoint touches no store, so it is the one read that survives an
// unreadable one — and its envelope carries no store revision, because no store
// was read.
func TestRecurrenceExplainAnswersWithoutTheStore(t *testing.T) {
	h := newHarness(t)
	h.writeStore("{not-json\n")

	answered := h.get("/api/v1/recurrence/explain?input=weekly")
	assertStatus(t, answered, 200)
	if answered.dig("meta") != nil {
		t.Errorf("explain carries a store revision: %s", answered.Body)
	}
	if got, _ := answered.dig("data", "canonical").(string); got != ".+1w" {
		t.Errorf("canonical = %v", got)
	}
	if len(stringsOf(answered.dig("data", "next"))) != 5 {
		t.Errorf("default projection = %v", answered.dig("data", "next"))
	}

	// Three answer shapes, all 200: a schedule, `off`, and text that is not a
	// schedule at all.
	off := h.get("/api/v1/recurrence/explain?input=off")
	assertStatus(t, off, 200)
	if off.dig("data", "canonical") != nil {
		t.Errorf("off canonical = %v", off.dig("data", "canonical"))
	}
	nonsense := h.get("/api/v1/recurrence/explain?input=every%20blorp")
	assertStatus(t, nonsense, 200)
	if nonsense.dig("data", "error") == nil {
		t.Errorf("an unparseable input must carry a reason: %s", nonsense.Body)
	}

	// Only a malformed REQUEST is 4xx.
	assertError(t, h.get("/api/v1/recurrence/explain"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/recurrence/explain?input=%20"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/recurrence/explain?input=weekly&count=abc"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/recurrence/explain?input=weekly&unknown=1"), 422, "validation_failed")
}

// Out-of-range counts clamp rather than fail, matching the engine.
func TestRecurrenceExplainClampsItsProjectionWindow(t *testing.T) {
	h := newHarness(t)
	if got := len(stringsOf(h.get("/api/v1/recurrence/explain?input=weekly&count=999").dig("data", "next"))); got != 50 {
		t.Errorf("count=999 projected %d, want the 50 ceiling", got)
	}
	if got := len(stringsOf(h.get("/api/v1/recurrence/explain?input=weekly&count=0").dig("data", "next"))); got != 0 {
		t.Errorf("count=0 projected %d", got)
	}
	if got := len(stringsOf(h.get("/api/v1/recurrence/explain?input=weekly&count=-3").dig("data", "next"))); got != 0 {
		t.Errorf("count=-3 projected %d", got)
	}
}

// The log names the templated route, never the path, so a log a user pastes
// into an issue carries no task ids.
func TestRequestLoggingIsSafeAndStructured(t *testing.T) {
	h := newHarness(t)
	h.get("/api/v1/tasks/" + fixPR)
	line := strings.TrimSpace(h.logs.String())
	if !strings.Contains(line, `"route":"/api/v1/tasks/{id}"`) {
		t.Errorf("log line = %s", line)
	}
	if strings.Contains(line, fixPR) {
		t.Errorf("log line leaks the task id: %s", line)
	}
	if strings.Contains(line, h.dir) {
		t.Errorf("log line leaks the store path: %s", line)
	}
	for _, key := range []string{"event", "request_id", "method", "route", "status", "duration_ms"} {
		if !strings.Contains(line, `"`+key+`"`) {
			t.Errorf("log line omits %q: %s", key, line)
		}
	}
}
