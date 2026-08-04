package api

import (
	"strings"
	"testing"
)

// These mirror test/api/test_app.rb's write half. Where the Go store cannot
// perform the operation the Ruby test exercises, the Go test asserts the
// REFUSAL and names what is missing — see TestRoutesThisBuildRefusesSayWhy.

func TestCreateUpdateAndNoopRoundTrip(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks", `{
		"title":"API-created task","priority":"B","tags":["api"],"contexts":["@desk"],
		"deferred":false,"state":"NEXT","project":"Work","parent_id":null,
		"body":["one","two"]}`, nil)
	assertStatus(t, created, 201)
	resource := created.data()
	id, _ := resource["id"].(string)
	if !taskIDPattern.MatchString(id) {
		t.Fatalf("created id = %q", id)
	}
	if got := created.Header.Get("location"); got != "/api/v1/tasks/"+id {
		t.Errorf("location = %q", got)
	}
	revision, _ := resource["revision"].(string)
	if created.etag() != `"`+revision+`"` {
		t.Errorf("etag %q does not quote revision %q", created.etag(), revision)
	}
	assertStrings(t, stringsOf(resource["contexts"]), []string{"@desk"}, "contexts")
	assertStrings(t, stringsOf(resource["tags"]), []string{"api"}, "tags")
	if resource["parent_id"] != nil {
		t.Errorf("parent_id = %v", resource["parent_id"])
	}
	assertStrings(t, stringsOf(resource["body"]),
		[]string{"Captured [2026-07-15].", "one", "two"}, "body")
	if resource["state"] != "NEXT" {
		t.Errorf("state = %v", resource["state"])
	}

	updated := h.json("PATCH", "/api/v1/tasks/"+id,
		`{"title":"API-updated task","priority":"A","contexts":["@home"],"tags":["changed"]}`,
		h.withIfMatch(created.etag()))
	assertStatus(t, updated, 200)
	updatedResource := updated.data()
	if updatedResource["title"] != "API-updated task" {
		t.Errorf("title = %v", updatedResource["title"])
	}
	assertStrings(t, stringsOf(updatedResource["contexts"]), []string{"@home"}, "contexts")
	if updated.etag() == created.etag() {
		t.Error("a real change must change the ETag")
	}

	// A no-op PATCH is a success that writes nothing — same ETag, no journal
	// entry, identical bytes.
	before := string(h.storeBytes())
	noop := h.json("PATCH", "/api/v1/tasks/"+id, `{"title":"API-updated task"}`,
		h.withIfMatch(updated.etag()))
	assertStatus(t, noop, 200)
	if noop.etag() != updated.etag() {
		t.Errorf("no-op changed the ETag: %q -> %q", updated.etag(), noop.etag())
	}
	if string(h.storeBytes()) != before {
		t.Error("a no-op PATCH rewrote the store")
	}
}

func TestCreateAddsConfiguredHostContextAndSupportsExplicitOptOut(t *testing.T) {
	h := newHarnessWith(t, fixtureOrg, fixtureArchive, "@home")

	inherited := h.json("POST", "/api/v1/tasks",
		`{"title":"API contextual task","contexts":["@desk"],"tags":["api"]}`, nil)
	assertStatus(t, inherited, 201)
	assertStrings(t, stringsOf(inherited.data()["contexts"]), []string{"@home", "@desk"}, "contexts")
	assertStrings(t, stringsOf(inherited.data()["tags"]), []string{"api"}, "tags")

	suppressed := h.json("POST", "/api/v1/tasks",
		`{"title":"API work-only task","contexts":["@work"],"apply_host_context":false}`, nil)
	assertStatus(t, suppressed, 201)
	assertStrings(t, stringsOf(suppressed.data()["contexts"]), []string{"@work"}, "contexts")

	invalid := h.json("POST", "/api/v1/tasks",
		`{"title":"Invalid policy","apply_host_context":"false"}`, nil)
	assertError(t, invalid, 422, "validation_failed")
}

func TestCreateRefusesAnUnresolvableParent(t *testing.T) {
	h := newHarness(t)
	answered := h.json("POST", "/api/v1/tasks", `{"title":"Orphan","parent_id":"deadbeef"}`, nil)
	assertError(t, answered, 404, "not_found")
	if got, _ := answered.dig("error", "details", "parent_id").(string); got != "deadbeef" {
		t.Errorf("details.parent_id = %v", got)
	}
	if answered.message() != "parent_id does not identify a live task." {
		t.Errorf("message = %q", answered.message())
	}
}

func TestPatchUpdatesProposalPresentationWithoutChangingItsState(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Notify the invented channel","state":"PROPOSED","body":"Original rationale."}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)

	updated := h.json("PATCH", "/api/v1/tasks/"+id, `{
		"title":"Notify the actual channel","priority":"B","contexts":["@desk"],
		"tags":["correction"],
		"body":["Original rationale.","Correction: use the actual channel."]}`,
		h.withIfMatch(created.etag()))
	assertStatus(t, updated, 200)
	task := updated.data()
	if task["state"] != "PROPOSED" {
		t.Errorf("a presentation patch changed the state to %v", task["state"])
	}
	if task["title"] != "Notify the actual channel" {
		t.Errorf("title = %v", task["title"])
	}
	if task["priority"] != "B" {
		t.Errorf("priority = %v", task["priority"])
	}
	assertStrings(t, stringsOf(task["body"]),
		[]string{"Original rationale.", "Correction: use the actual channel."}, "body")
}

func TestProposalScopeExcludesAProposalFromTheDefaultList(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Research backup providers","state":"PROPOSED"}`, nil)
	assertStatus(t, created, 201)
	task := created.data()
	id, _ := task["id"].(string)
	if task["state"] != "PROPOSED" {
		t.Errorf("state = %v", task["state"])
	}
	if task["available"] != false || task["availability_reason"] != "proposed" {
		t.Errorf("availability = %v/%v", task["available"], task["availability_reason"])
	}

	for _, listed := range h.get("/api/v1/tasks").ids() {
		if listed == id {
			t.Error("a proposal appears in the default open list")
		}
	}
	assertStrings(t, h.get("/api/v1/tasks?scope=proposed").ids(), []string{id}, "scope=proposed")
}

func TestPreconditionsStaleCurrentAndFreshETag(t *testing.T) {
	h := newHarness(t)
	missing := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"x"}`, nil)
	assertError(t, missing, 428, "missing_precondition")

	loaded := h.etagOf(fixPR)
	won := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"CLI won"}`, h.withIfMatch(loaded))
	assertStatus(t, won, 200)

	stale := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"title":"API loses"}`, h.withIfMatch(loaded))
	assertError(t, stale, 412, "stale_revision")
	current, _ := stale.dig("error", "details", "current").(map[string]any)
	if current["title"] != "CLI won" {
		t.Errorf("412 details.current.title = %v", current["title"])
	}
	revision, _ := current["revision"].(string)
	if stale.etag() != `"`+revision+`"` {
		t.Errorf("412 etag %q does not quote the current revision %q", stale.etag(), revision)
	}
}

func TestTemporalCreateAndPatchPairSemantics(t *testing.T) {
	h := newHarness(t)
	// The pair is set on an EXISTING task, because this build's store cannot
	// persist a date on a create (see TestRoutesThisBuildRefusesSayWhy).
	seeded := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"deadline":"2026-07-20","deadline_time":{"local":"17:00","timezone":"Europe/London","fold":0}}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, seeded, 200)
	deadlineTime, _ := seeded.data()["deadline_time"].(map[string]any)
	if deadlineTime["instant"] != "2026-07-20T16:00:00Z" {
		t.Errorf("instant = %v", deadlineTime["instant"])
	}
	if deadlineTime["effective_timezone"] != "Europe/London" {
		t.Errorf("effective_timezone = %v", deadlineTime["effective_timezone"])
	}

	moved := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"deadline":"2026-07-21"}`,
		h.withIfMatch(seeded.etag()))
	assertStatus(t, moved, 200)
	movedTime, _ := moved.data()["deadline_time"].(map[string]any)
	if moved.data()["deadline"] != "2026-07-21" {
		t.Errorf("deadline = %v", moved.data()["deadline"])
	}
	if movedTime["local"] != "17:00" {
		t.Errorf("a date-only PATCH must preserve the time intent, got %v", movedTime["local"])
	}

	allDay := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"deadline_time":null}`,
		h.withIfMatch(moved.etag()))
	assertStatus(t, allDay, 200)
	if allDay.data()["deadline_time"] != nil {
		t.Errorf("deadline_time = %v", allDay.data()["deadline_time"])
	}

	invalid := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"deadline":null,"deadline_time":{"local":"09:00"}}`, h.withIfMatch(allDay.etag()))
	assertError(t, invalid, 422, "validation_failed")

	orphan := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"scheduled_time":{"local":"09:00"}}`, h.withIfMatch(allDay.etag()))
	assertError(t, orphan, 422, "validation_failed")
}

func TestTemporalAPIRejectsDerivedFieldsUnknownZonesAndDSTGaps(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixFlight)
	for _, body := range []string{
		`{"deadline_time":{"local":"09:00","instant":"2026-07-20T09:00:00Z"}}`,
		`{"deadline_time":{"local":"09:00","timezone":"PST"}}`,
		`{"deadline":"2026-03-08","deadline_time":{"local":"02:30","timezone":"America/Los_Angeles"}}`,
		`{"deadline_time":{"local":"9:00"}}`,
		`{"deadline_time":{"local":"09:00","fold":2}}`,
		`{"deadline_time":[]}`,
	} {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, body, h.withIfMatch(current))
		assertError(t, answered, 422, "validation_failed")
	}
}

// Moving only the date can land a preserved wall time in a DST gap. That is a
// client-input 422, never a 503.
func TestPatchingOnlyTheDateOntoADSTGapIsAFieldErrorNotAServerError(t *testing.T) {
	h := newHarness(t)
	seeded := h.json("PATCH", "/api/v1/tasks/"+fixFlight,
		`{"deadline":"2026-03-01","deadline_time":{"local":"02:30","timezone":"America/Los_Angeles"}}`,
		h.withIfMatch(h.etagOf(fixFlight)))
	assertStatus(t, seeded, 200)

	answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, `{"deadline":"2026-03-08"}`,
		h.withIfMatch(h.etagOf(fixFlight)))
	assertError(t, answered, 422, "validation_failed")
}

func TestPatchRejectsMalformedAndMutuallyExclusivePlacements(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixFlight)
	cases := []struct {
		body  string
		field string
	}{
		{`{"placement":null}`, "placement"},
		{`{"placement":[]}`, "placement"},
		{`{"placement":{}}`, "placement.parent_id"},
		{`{"placement":{"parent_id":null}}`, "placement.parent_id"},
		{`{"placement":{"parent_id":"aaaa0003","before_id":"short"}}`, "placement.before_id"},
		{`{"placement":{"parent_id":"aaaa0003","after_id":"aaaa0006"}}`, "placement"},
	}
	for _, testCase := range cases {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, testCase.body, h.withIfMatch(current))
		assertError(t, answered, 422, "validation_failed")
		fields, _ := answered.dig("error", "details", "fields").(map[string]any)
		if _, present := fields[testCase.field]; !present {
			t.Errorf("%s: details.fields has %v, want %q", testCase.body, fields, testCase.field)
		}
	}

	exclusive := h.json("PATCH", "/api/v1/tasks/"+fixFlight,
		`{"parent_id":"aaaa0009","placement":{"parent_id":"aaaa0003"}}`, h.withIfMatch(current))
	assertError(t, exclusive, 422, "validation_failed")
	if exclusive.message() != "One or more fields are invalid." {
		t.Errorf("message = %q", exclusive.message())
	}
	fields, _ := exclusive.dig("error", "details", "fields").(map[string]any)
	if len(fields) != 2 || fields["parent_id"] == nil || fields["placement"] == nil {
		t.Errorf("details.fields = %v", fields)
	}

	missingPrecondition := h.json("PATCH", "/api/v1/tasks/"+fixFlight,
		`{"placement":{"parent_id":"aaaa0003","before_id":"aaaa0006"}}`, nil)
	assertError(t, missingPrecondition, 428, "missing_precondition")
}

func TestPatchRefusesUnknownFieldsAndEmptyChangesets(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixPR)

	unknown := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"notes":["x"]}`, h.withIfMatch(current))
	assertError(t, unknown, 422, "validation_failed")

	empty := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{}`, h.withIfMatch(current))
	assertError(t, empty, 422, "validation_failed")

	// A PATCH body may not be null: there is no "clear the note" on this route.
	nullBody := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"body":null}`, h.withIfMatch(current))
	assertError(t, nullBody, 422, "validation_failed")

	missing := h.json("PATCH", "/api/v1/tasks/deadbeef", `{"title":"x"}`, h.withIfMatch(current))
	assertError(t, missing, 404, "not_found")
	if got, _ := missing.dig("error", "details", "field").(string); got != "id" {
		t.Errorf("details.field = %v", got)
	}
}

// Two different 422 shapes, and the difference is load-bearing.
//
// An input the ADAPTER parses and refuses carries the engine's reason in
// `details.fields`, under the field that failed — the generic message plus a
// specific reason. A patch the adapter accepted and the DOMAIN refused has no
// per-field errors to hand back, so its own sentence becomes the message; the
// generic one would hide the only useful information.
func TestPatchRefusalsCarryTheReasonInTheRightHalfOfTheEnvelope(t *testing.T) {
	h := newHarness(t)
	current := h.etagOf(fixFlight)
	for _, testCase := range []struct{ body, field string }{
		{`{"recurrence":"every blorp"}`, "recurrence"},
		{`{"lead":"3 blorps"}`, "lead"},
	} {
		answered := h.json("PATCH", "/api/v1/tasks/"+fixFlight, testCase.body, h.withIfMatch(current))
		assertError(t, answered, 422, "validation_failed")
		if answered.message() != "One or more fields are invalid." {
			t.Errorf("%s: message = %q", testCase.body, answered.message())
		}
		fields, _ := answered.dig("error", "details", "fields").(map[string]any)
		reasons := stringsOf(fields[testCase.field])
		if len(reasons) != 1 || reasons[0] == "" {
			t.Errorf("%s: details.fields = %v", testCase.body, fields)
		}
	}

	// A lead window beside BOTH a deadline and an available-from date is a
	// domain refusal, not a field-shape one.
	semantic := h.json("PATCH", "/api/v1/tasks/"+fixPR,
		`{"scheduled":"2026-09-01","deadline":"2026-09-10","lead":"3d"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertError(t, semantic, 422, "validation_failed")
	if semantic.message() == "One or more fields are invalid." {
		t.Error("a domain refusal must carry the domain's own sentence")
	}
	if semantic.dig("error", "details", "fields") != nil {
		t.Errorf("a domain refusal has no per-field errors: %s", semantic.Body)
	}
}

// The full single-request field vocabulary, applied in one atomic changeset and
// then read back. This is the test that would catch a field the adapter accepts
// and silently drops.
func TestPatchAppliesEveryDocumentedFieldAndReadsBack(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{
		"title":"Everything","priority":"C","state":"TODO","deferred":true,
		"contexts":["@desk","@home"],"tags":["alpha","beta"],
		"body":["first","second"],
		"scheduled":"2026-09-01","scheduled_time":{"local":"08:15","timezone":"Europe/Berlin","fold":0},
		"recurrence":"weekly","lead":"3d"}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, answered, 200)

	task := h.get("/api/v1/tasks/" + fixPR).data()
	if task["title"] != "Everything" || task["priority"] != "C" || task["state"] != "TODO" {
		t.Errorf("scalar fields = %v/%v/%v", task["title"], task["priority"], task["state"])
	}
	if task["deferred"] != true {
		t.Errorf("deferred = %v", task["deferred"])
	}
	assertStrings(t, stringsOf(task["contexts"]), []string{"@desk", "@home"}, "contexts")
	assertStrings(t, stringsOf(task["tags"]), []string{"alpha", "beta"}, "tags")
	assertStrings(t, stringsOf(task["body"]), []string{"first", "second"}, "body")
	if task["scheduled"] != "2026-09-01" {
		t.Errorf("scheduled = %v", task["scheduled"])
	}
	scheduledTime, _ := task["scheduled_time"].(map[string]any)
	if scheduledTime["local"] != "08:15" || scheduledTime["timezone"] != "Europe/Berlin" {
		t.Errorf("scheduled_time = %v", scheduledTime)
	}
	if task["recurrence"] != ".+1w" {
		t.Errorf("recurrence = %v", task["recurrence"])
	}
	if task["lead"] != "3d" {
		t.Errorf("lead = %v", task["lead"])
	}

	// One changeset, one undo step: the journal must not have grown by twelve.
	cleared := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"recurrence":null,"lead":null}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, cleared, 200)
	if cleared.data()["recurrence"] != nil || cleared.data()["lead"] != nil {
		t.Errorf("clearing left %v/%v", cleared.data()["recurrence"], cleared.data()["lead"])
	}
}

// Every route this build cannot perform must refuse EXPLICITLY, with a reason,
// and must leave the store untouched. A silent success or a 503 that invites a
// retry would both be worse than a stated 501.
func TestRoutesThisBuildRefusesSayWhy(t *testing.T) {
	h := newHarness(t)
	before := string(h.storeBytes())
	tag := h.etagOf(fixPR)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		expect string
	}{
		{"delegate", "POST", "/api/v1/tasks/" + fixPR + "/delegate", `{"kind":"agent","mode":"refine"}`, "delegate over HTTP"},
		{"undelegate", "POST", "/api/v1/tasks/" + fixPR + "/undelegate", "", "undelegate over HTTP"},
		{"claim", "POST", "/api/v1/tasks/" + fixPR + "/claim", `{"worker":"w1"}`, "claim over HTTP"},
		{"release", "POST", "/api/v1/tasks/" + fixPR + "/release", `{"worker":"w1"}`, "release over HTTP"},
		{"work_ref", "PUT", "/api/v1/tasks/" + fixPR + "/work_ref", `{"work_ref":"https://x"}`, "work_ref over HTTP"},
	}
	for _, testCase := range cases {
		headers := h.withIfMatch(tag)
		answered := h.json(testCase.method, testCase.path, testCase.body, headers)
		assertError(t, answered, 501, "not_implemented")
		if !strings.Contains(answered.message(), testCase.expect) {
			t.Errorf("%s: message %q does not name %q", testCase.name, answered.message(), testCase.expect)
		}
	}

	if string(h.storeBytes()) != before {
		t.Error("a refused route wrote to the store")
	}
}

// The undoable hard delete, mirroring test_app.rb's
// `test_create_update_noop_and_delete_round_trip` and
// `test_preconditions_stale_current_and_delete_cascade_conflict`.
//
// This route used to be a stated 501 because `internal/api` had no application
// seam for it. `application.DeleteTask` performs it, and the If-Match this route
// makes mandatory is the precondition the store's own delete already guards the
// subtree with — so unlike the delegation routes there is no precondition the
// adapter would have to drop, and refusing became the dishonest answer.
func TestDeleteRemovesTheTaskAndAnswers204(t *testing.T) {
	h := newHarness(t)
	answered := h.json("DELETE", "/api/v1/tasks/"+fixGarden, "", h.withIfMatch(h.etagOf(fixGarden)))
	assertStatus(t, answered, 204)
	if answered.Body != "" {
		t.Errorf("204 carried a body: %q", answered.Body)
	}
	if got := answered.Header.Get("content-type"); got != "" {
		t.Errorf("204 carried a content-type: %q", got)
	}
	if got := answered.Header.Get("content-length"); got != "0" {
		t.Errorf("content-length = %q, want 0", got)
	}
	if strings.Contains(string(h.storeBytes()), fixGarden) {
		t.Error("the task survived the delete")
	}
	assertError(t, h.get("/api/v1/tasks/"+fixGarden), 404, "not_found")
}

// A task with descendants refuses without cascade, and the refusal carries the
// counts — they are what tell the caller what cascade=true would remove.
func TestDeleteRefusesDescendantsUntilCascade(t *testing.T) {
	h := newHarness(t)
	before := string(h.storeBytes())
	tag := h.etagOf(fixPR)
	conflict := h.json("DELETE", "/api/v1/tasks/"+fixPR, "", h.withIfMatch(tag))
	assertError(t, conflict, 409, "conflict")
	details, _ := conflict.dig("error", "details").(map[string]any)
	if details["descendants"] != float64(2) {
		t.Errorf("descendants = %v, want 2", details["descendants"])
	}
	if details["open_descendants"] != float64(2) {
		t.Errorf("open_descendants = %v, want 2", details["open_descendants"])
	}
	if !strings.Contains(conflict.message(), "cascade=true") {
		t.Errorf("the refusal does not name the remedy: %q", conflict.message())
	}
	if string(h.storeBytes()) != before {
		t.Error("a refused delete wrote to the store")
	}

	cascaded := h.json("DELETE", "/api/v1/tasks/"+fixPR+"?cascade=true", "", h.withIfMatch(tag))
	assertStatus(t, cascaded, 204)
	for _, id := range []string{fixPR, fixChild, fixGrand} {
		if strings.Contains(string(h.storeBytes()), id) {
			t.Errorf("%s survived the cascade", id)
		}
	}
	// The archived task sharing the PR id is untouched: the archive is a
	// different source, and a delete never reaches it.
	if !strings.Contains(string(h.archiveBytes()), fixPR) {
		t.Error("the cascade reached the archive")
	}
}

func TestDeleteRefusesAStaleOrMissingPrecondition(t *testing.T) {
	h := newHarness(t)
	stale := h.etagOf(fixEval)
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+fixEval, `{"title":"moved on"}`,
		h.withIfMatch(stale)), 200)

	refused := h.json("DELETE", "/api/v1/tasks/"+fixEval, "", h.withIfMatch(stale))
	assertError(t, refused, 412, "stale_revision")
	// The 412 carries the CURRENT resource and its ETag, which is what lets a
	// client decide again without a second round trip.
	if refused.dig("error", "details", "current", "title") != "moved on" {
		t.Errorf("details.current = %v", refused.dig("error", "details", "current"))
	}
	if refused.etag() == "" || refused.etag() == stale {
		t.Errorf("stale etag = %q", refused.etag())
	}
	if !strings.Contains(string(h.storeBytes()), fixEval) {
		t.Error("a stale delete removed the task")
	}

	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixEval, "", nil), 428, "missing_precondition")
	assertError(t, h.json("DELETE", "/api/v1/tasks/deadbeef", "",
		h.withIfMatch(stale)), 404, "not_found")
	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixEval+"?cascade=yes", "",
		h.withIfMatch(h.etagOf(fixEval))), 422, "validation_failed")
}

// Approve and reject, mirroring test_app.rb's
// `test_proposal_scope_and_approve_reject_actions`.
//
// These were a stated 501 for the same stale reason DELETE was:
// `application.DecideProposal` performs both and passes the caller's
// expected_revision straight to the store, which honours it — so nothing here
// drops a precondition a client set.
func TestApproveMovesAProposalIntoTheList(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks", `{"title":"Research backup providers","state":"PROPOSED"}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)
	if created.data()["state"] != "PROPOSED" || created.data()["available"] != false {
		t.Fatalf("fixture precondition: %s", created.Body)
	}

	// A proposal is outside the default scope and inside scope=proposed.
	if containsString(h.get("/api/v1/tasks").ids(), id) {
		t.Error("a proposal appeared in the default scope")
	}
	assertStrings(t, h.get("/api/v1/tasks?scope=proposed").ids(), []string{id}, "proposed ids")

	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/approve", "", nil), 428, "missing_precondition")

	approved := h.json("POST", "/api/v1/tasks/"+id+"/approve", "", h.withIfMatch(created.etag()))
	assertStatus(t, approved, 200)
	if approved.data()["state"] != "INBOX" {
		t.Errorf("state = %v, want INBOX", approved.data()["state"])
	}
	// The ETag is the revision this write produced, which is what lets the client
	// keep editing without a refetch.
	revision, _ := approved.data()["revision"].(string)
	if approved.etag() != `"`+revision+`"` {
		t.Errorf("etag %q does not match the resource revision %q", approved.etag(), revision)
	}
	if record := h.recordFor("Research backup providers"); record["state"] != "INBOX" {
		t.Errorf("the store still holds %v", record["state"])
	}
	// A body is refused rather than ignored, and an approve is not repeatable.
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/approve", `{"notes":"x"}`,
		h.withIfMatch(approved.etag())), 400, "malformed_request")
	again := h.json("POST", "/api/v1/tasks/"+id+"/approve", "", h.withIfMatch(approved.etag()))
	assertError(t, again, 422, "validation_failed")
	if !strings.Contains(again.message(), "not PROPOSED") {
		t.Errorf("the refusal does not name the state: %q", again.message())
	}
}

func TestRejectCancelsAProposalAndAppendsItsNotes(t *testing.T) {
	h := newHarness(t)
	created := h.json("POST", "/api/v1/tasks",
		`{"title":"Duplicate proposal","state":"PROPOSED","body":"Original rationale."}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)

	rejected := h.json("POST", "/api/v1/tasks/"+id+"/reject",
		`{"notes":["Duplicate — already rejected previously.","Same renewal mail."]}`,
		h.withIfMatch(created.etag()))
	assertStatus(t, rejected, 200)
	if rejected.data()["state"] != "CANCELLED" {
		t.Errorf("state = %v, want CANCELLED", rejected.data()["state"])
	}
	// Today's closed date comes from the pinned clock, not the wall clock.
	if rejected.data()["closed"] != "2026-07-15" {
		t.Errorf("closed = %v", rejected.data()["closed"])
	}
	// The notes are appended to the body in the SAME write, after the original.
	body := stringsOf(rejected.data()["body"])
	for _, want := range []string{
		"Original rationale.", "Duplicate — already rejected previously.", "Same renewal mail.",
	} {
		if !containsString(body, want) {
			t.Errorf("body %v is missing %q", body, want)
		}
	}
}

// The reject body is optional, and what it may carry is exactly `notes` — as one
// string or an ordered list.
func TestRejectValidatesItsOptionalNotes(t *testing.T) {
	proposal := func(h *harness, title string) (string, string) {
		created := h.json("POST", "/api/v1/tasks", `{"title":"`+title+`","state":"PROPOSED"}`, nil)
		assertStatus(t, created, 201)
		id, _ := created.data()["id"].(string)
		return id, created.etag()
	}

	// No body at all keeps the historical contract.
	h := newHarness(t)
	id, tag := proposal(h, "no body")
	assertStatus(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", "", h.withIfMatch(tag)), 200)

	// One string is one note.
	id, tag = proposal(h, "single note")
	single := h.json("POST", "/api/v1/tasks/"+id+"/reject", `{"notes":"Just the one."}`, h.withIfMatch(tag))
	assertStatus(t, single, 200)
	if !containsString(stringsOf(single.data()["body"]), "Just the one.") {
		t.Errorf("the single note was not appended: %s", single.Body)
	}

	// An empty list is the same as none, and still cancels.
	id, tag = proposal(h, "empty notes")
	assertStatus(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", `{"notes":[]}`, h.withIfMatch(tag)), 200)

	id, tag = proposal(h, "bad notes")
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", `{"notes":[1,2]}`,
		h.withIfMatch(tag)), 422, "validation_failed")
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", `{"reason":"nope"}`,
		h.withIfMatch(tag)), 422, "validation_failed")
	assertError(t, h.do(request{
		method: "POST", path: "/api/v1/tasks/" + id + "/reject",
		body: "notes", contentType: "text/plain", headers: h.withIfMatch(tag),
	}), 415, "unsupported_media_type")
	// The precondition is still mandatory, and still checked. A well-formed
	// If-Match that names no revision this store could have produced is a
	// malformed VALUE (422), while a real revision the task has moved past is a
	// stale one (412) — a client has to be able to tell those apart.
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", "", nil), 428, "missing_precondition")
	malformed := h.json("POST", "/api/v1/tasks/"+id+"/reject", "", h.withIfMatch(`"v1.dead.beef.cafe"`))
	assertError(t, malformed, 422, "validation_failed")
	if !strings.Contains(malformed.message(), "malformed expected_revision") {
		t.Errorf("message = %q", malformed.message())
	}
	assertStatus(t, h.json("PATCH", "/api/v1/tasks/"+id, `{"title":"moved on"}`, h.withIfMatch(tag)), 200)
	assertError(t, h.json("POST", "/api/v1/tasks/"+id+"/reject", "", h.withIfMatch(tag)),
		412, "stale_revision")
}

// A refusal must still run the transport gates first, so a client learns about
// its malformed request before it learns the route is unbuilt — and, for the
// routes that ARE built, before the write runs.
func TestRefusedRoutesStillEnforceTheirPreconditions(t *testing.T) {
	h := newHarness(t)
	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixPR, "", nil), 428, "missing_precondition")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/approve", "", nil), 428, "missing_precondition")
	assertError(t, h.json("POST", "/api/v1/tasks/"+fixPR+"/claim", `{"worker":"w"}`, nil), 428, "missing_precondition")
	assertError(t, h.json("GET", "/api/v1/tasks/NOT-AN-ID", "", nil), 400, "malformed_request")
	assertError(t, h.json("DELETE", "/api/v1/tasks/"+fixPR+"?unknown=1", "",
		h.withIfMatch(h.etagOf(fixPR))), 422, "validation_failed")
}

// The dated create now PERSISTS over HTTP. These four used to be a stated 501
// because the store could not write them; the refusal was the honest answer
// while that was true, and dropping a deadline a caller asked for would have
// lost data it believed it had stored.
func TestCreatePersistsTheDatedFields(t *testing.T) {
	for _, testCase := range []struct{ body, field, want string }{
		{`{"title":"x","scheduled":"2026-07-20"}`, "scheduled", "2026-07-20"},
		{`{"title":"x","deadline":"2026-07-20"}`, "deadline", "2026-07-20"},
		{`{"title":"x","recurrence":"weekly"}`, "recurrence", ".+1w"},
		{`{"title":"x","deadline":"2026-07-20","lead":"3d"}`, "lead", "3d"},
	} {
		h := newHarness(t)
		answered := h.json("POST", "/api/v1/tasks", testCase.body, nil)
		assertStatus(t, answered, 201)
		if got := answered.data()[testCase.field]; got != testCase.want {
			t.Errorf("%s: %s = %v, want %q", testCase.body, testCase.field, got, testCase.want)
		}
	}
}

// A create that names a time of day REFUSES rather than storing an all-day date
// and calling it a success. This is the only field pair the create path cannot
// carry, and the refusal names the application seam that is missing and the
// PATCH that does persist it.
func TestCreateRefusesATimeOfDayItCannotPersist(t *testing.T) {
	for _, testCase := range []struct{ name, body, expect string }{
		{"deadline_time", `{"title":"x","deadline":"2026-07-20","deadline_time":{"local":"17:00"}}`, "deadline"},
		{"scheduled_time", `{"title":"x","scheduled":"2026-07-20","scheduled_time":{"local":"09:30"}}`, "scheduled"},
	} {
		h := newHarness(t)
		before := string(h.storeBytes())
		answered := h.json("POST", "/api/v1/tasks", testCase.body, nil)
		assertError(t, answered, 501, "not_implemented")
		if !strings.Contains(answered.message(), testCase.expect) {
			t.Errorf("%s: message %q does not name the field", testCase.name, answered.message())
		}
		if string(h.storeBytes()) != before {
			t.Errorf("%s: the refused create wrote to the store", testCase.name)
		}
	}

	// A null time is not a time: it is the all-day spelling, and it still creates.
	h := newHarness(t)
	assertStatus(t, h.json("POST", "/api/v1/tasks",
		`{"title":"all day","deadline":"2026-07-20","deadline_time":null}`, nil), 201)

	// The shape checks still run FIRST, so a malformed time is a 422 about the
	// request rather than a 501 about the build.
	assertError(t, h.json("POST", "/api/v1/tasks",
		`{"title":"x","deadline":"2026-07-20","deadline_time":{"local":"5pm"}}`, nil),
		422, "validation_failed")
	assertError(t, h.json("POST", "/api/v1/tasks",
		`{"title":"x","deadline_time":{"local":"17:00"}}`, nil), 422, "validation_failed")

	// And the remedy in the refusal actually works.
	created := h.json("POST", "/api/v1/tasks", `{"title":"timed later","deadline":"2026-07-20"}`, nil)
	assertStatus(t, created, 201)
	id, _ := created.data()["id"].(string)
	patched := h.json("PATCH", "/api/v1/tasks/"+id,
		`{"deadline_time":{"local":"17:00","timezone":"Europe/London"}}`, h.withIfMatch(created.etag()))
	assertStatus(t, patched, 200)
	timed, _ := patched.data()["deadline_time"].(map[string]any)
	if timed == nil || timed["local"] != "17:00" {
		t.Errorf("the PATCH remedy did not persist the time: %s", patched.Body)
	}
}

// A placement over HTTP reaches the store as a placement, anchor included.
// Flattening the anchor away would be exactly the silent half-work a stated
// refusal exists to prevent.
func TestPlacementOverHTTPCarriesItsAnchor(t *testing.T) {
	h := newHarness(t)
	answered := h.json("PATCH", "/api/v1/tasks/"+fixChild,
		`{"placement":{"parent_id":"aaaa0001"}}`, h.withIfMatch(h.etagOf(fixChild)))
	assertStatus(t, answered, 200)
	if got := answered.data()["parent_id"]; got != nil {
		t.Errorf("parent_id = %v, want null after a move to a section", got)
	}

	unnest := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"parent_id":null}`,
		h.withIfMatch(h.etagOf(fixPR)))
	assertStatus(t, unnest, 200)
}
